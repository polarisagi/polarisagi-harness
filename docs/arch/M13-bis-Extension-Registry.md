# 模块 13-bis: Extension Registry

> 扩展系统的市场、安装、运行时三层模型。覆盖 MCP / Skill / Plugin / App 全类型。[HE-Rule-3] [HE-Rule-6]
> **§跳读**: 0:职责边界 / 1:三层模型 / 2:类型定义 / 3:安装流 / 4:信任门控 / 5:文件系统 / 6:调用路由 / 7:学习技能归并 / 8:表引用速查

---

## 0. 职责边界

- **是**: 市场同步、目录展示、安装/卸载 API、安装状态追踪
- **是**: `extension_instances` 作为所有已安装扩展的单一事实来源（SSoT）
- **是**: 安装后运行时绑定（写 `mcp_servers` 或 `skills`）
- **不是**: MCP 进程生命周期管理（M7 MCPManager）
- **不是**: Wasm 执行与沙箱（M7 WazmRuntime）
- **不是**: Skill 检索与 Logic Collapse（M6）
- **不是**: 信任策略制定（M11 Cedar-Gate）

---

## 1. 三层模型

```
Layer 0  Market（目录层）
  plugin_marketplaces   市场来源注册；builtin=4条，用户可追加
  registry_cache        市场同步快照；只读缓存，不驱动执行

Layer 1  Instances（安装层）← SSoT
  extension_instances   所有已安装扩展的统一记录
                        origin 区分来源，status 追踪异步安装进度

Layer 2  Runtime（运行时层）
  mcp_servers           MCP 进程连接配置；MCPManager 唯一消费方
  skills（008）         Wasm/Script 执行元数据；SkillExecutor 唯一消费方
```

**数据流方向**：`plugin_marketplaces → 同步 → registry_cache → 安装 → extension_instances → 绑定 → mcp_servers / skills`

`extension_instances` 是唯一跨层视图。前端安装状态查询、卸载、刷新全走此表，不直接查运行时表。

---

## 2. 扩展类型

| ext_type | 运行时绑定 | 文件下载 | 典型来源 |
|----------|-----------|---------|---------|
| `mcp`    | `mcp_servers` | 否（进程自管理） | marketplace / user |
| `skill`  | `skills`（008） | 是（wasm/script） | marketplace / learned |
| `plugin` | `mcp_servers` + `skills`（一对多） | 是（bundle 解压） | marketplace |
| `app`    | 无（URL 直记） | 否 | marketplace / user |

**origin 枚举**：

| origin | 含义 | trust_tier 默认值 |
|--------|------|-----------------|
| `builtin`     | 程序内嵌，启动 UPSERT | 4 TrustSystem |
| `marketplace` | 市场安装（catalog_id 非空） | 继承 registry_cache |
| `user`        | 用户手动创建/配置 | 1 TrustLocal |
| `learned`     | M9 自演化 promote | 1 TrustLocal |

---

## 3. 安装流

### 3.1 MCP

```
POST /v1/plugins/install {catalog_id, type=mcp}
  1. 写 extension_instances (status=installing)
  2. INSERT mcp_servers（继承 trust_tier）
  3. go MCPManager.startMCPServer()
  4. UPDATE extension_instances SET status=installed, runtime_id=mcp_servers.id
```

### 3.2 Skill

```
POST /v1/plugins/install {catalog_id, type=skill}
  1. 写 extension_instances (status=downloading)
  2. go downloadAndInstallSkill():
     a. git clone / fetch wasm → install_path
     b. 解析 SKILL.md / skill.json → SkillMeta
     c. INSERT skills（008）via SkillRegistry.Register()
     d. UPDATE extension_instances SET status=installed, runtime_id='skill:'+name
```

### 3.3 Plugin Bundle

```
POST /v1/plugins/install {catalog_id, type=plugin}
  1. 写 extension_instances (status=downloading, parent)
  2. go downloadAndInstallPlugin():
     a. 下载并解压 → install_path
     b. 解析 plugin.json：
        skills[]      → 各自走 3.2 子流，写子 extension_instances
        mcp_servers[] → 各自走 3.1 子流，写子 extension_instances
        hooks[]       → 写 policies/ 目录（M11 Hook 框架）
     c. UPDATE parent extension_instances SET status=installed
```

### 3.4 卸载

```
DELETE /v1/plugins/{catalog_id}
  1. 查 extension_instances WHERE catalog_id=?
  2. 按 ext_type 清理运行时绑定（MCPManager.Remove / skills 表 deprecate）
  3. 删除 install_path 目录（若有）
  4. DELETE extension_instances（含子记录）
```

---

## 4. 信任门控

> 策略制定见 M11 Cedar-Gate。本节只描述 Extension Registry 的触发点。

安装时 `trust_tier` 从 `registry_cache` 继承，写入 `extension_instances` 和运行时表（`mcp_servers.trust_tier` / `skills.trust_tier`）。

**禁止**：安装请求的 `trust_tier` 字段不允许客户端覆盖（server 端强制忽略）。

TrustTier 影响：

| trust_tier | 安装时 | 运行时 |
|------------|-------|-------|
| 4 System   | 不走此流（程序内嵌） | 直接执行 |
| 3 Official | 自动确认 | Sbx-L2，TaintMedium |
| 2 Community | 自动确认 | Sbx-L1，TaintHigh |
| 1 Local    | 用户确认弹窗 | Sbx-L1，TaintHigh，每次提示 |
| 0 Untrusted | 拒绝安装 | — |

---

## 5. 文件系统布局

```
~/.polaris-harness/
├── extensions/
│   ├── skill/
│   │   ├── marketplace/{ext_id}/   # 市场安装的 Skill
│   │   │   ├── SKILL.md
│   │   │   ├── impl.wasm           # 或 main.py / main.sh
│   │   │   └── SIGNATURE
│   │   └── learned/{ext_id}/       # M9 自演化 promote 的 Skill
│   └── plugin/{ext_id}/            # Plugin Bundle 解压
│       ├── plugin.json
│       ├── skills/
│       └── hooks/
├── cache/{marketplace_id}/         # 市场同步临时下载区（安装完成后清理）
└── polaris.db
```

`extension_instances.install_path` 记录绝对路径。MCP 和 App 的 `install_path` 为空字符串。

---

## 6. 调用路由

```
Agent 请求工具 T
  → ToolRouter 按 URI scheme 分发
  ├── skill://*       → M6 SkillSelector → SkillExecutor（M7）
  ├── mcp://*         → MCPManager（M7）→ 运行时进程
  ├── builtin://*     → 内置工具直接调用（trust_tier=4，跳过门控）
  └── app://*         → AppRunner → extension_instances 查 URL → WebView
```

ToolRouter 查询来源：`extension_instances WHERE enabled=1` + `status=installed`。
内置工具（`origin=builtin`）在程序启动时注入，不经市场流程。

---

## 7. 学习技能归并（M9 → Extension Registry）

M9 Self-Improvement Engine 在 `L2SkillGeneration` 阶段 promote 候选技能时：

1. 写 `extension_instances`（`ext_type=skill, origin=learned, trust_tier=1`）
2. 调用 `SkillRegistry.Register()`，写 `skills`（008）表
3. `install_path` 指向 `extensions/skill/learned/{ext_id}/`

**禁止**：M9 直接写 `skills` 表（inv_M6_02）。必须经 `extension_instances` → `SkillRegistry` 路径。

前端"技能"Tab 通过 `origin=learned` 显示"AI 生成"标签，与"市场安装""内置"并列展示。

---

## 8. 表引用速查

| 表 | 迁移文件 | 消费方 |
|----|---------|-------|
| `plugin_marketplaces` | 018 | M13 API |
| `registry_cache` | 019 | M13 API |
| `extension_instances` | 020 | M13 API（SSoT） |
| `mcp_servers` | 015 | M7 MCPManager |
| `skills` | 008 | M6 SkillRegistry |

**已删除**（不再存在）：`skill_sources`、`plugins`、`apps`——职责统一归入 `extension_instances`（020）。
