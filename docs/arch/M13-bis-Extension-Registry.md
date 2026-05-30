# 模块 13-bis: Extension Registry

> 扩展系统的市场、安装、路由三层模型。覆盖 MCP / Skill / Plugin / App / Automation / Agent 六类扩展。[HE-Rule-3] [HE-Rule-6]
> **§跳读**: 0:8 职责边界 / 1:22 能力分层 / 2:45 扩展类型 / 3:83 技能执行模式 / 4:106 工具懒加载 / 5:138 安装流 / 6:255 信任门控 / 7:297 文件系统 / 8:326 调用路由 / 9:381 自动化 / 10:481 跨代理协作 / 11:516 学习技能归并 / 12:532 表引用

---

## 0. 职责边界

- **是**: 市场同步、目录展示、安装/卸载 API、安装状态追踪
- **是**: `extension_instances` 作为所有已安装扩展的单一事实来源（SSoT）
- **是**: 安装后运行时绑定（写 `mcp_servers` / `skills` / `plugins` / `automations`）
- **是**: 工具能力发现（ToolSearch 懒加载、Extension Card 元数据）
- **不是**: MCP 进程生命周期管理（M7 MCPManager）
- **不是**: Wasm 执行与沙箱（M7 WazeroRuntime）
- **不是**: Skill 检索与 Logic Collapse（M6）
- **不是**: 信任策略制定（M11 Cedar-Gate）
- **不是**: 自动化任务调度（M13 Scheduler，但 automation 扩展类型的元数据在此注册）

---

## 1. 能力分层模型

```
Layer 0  Market（目录层）
  plugin_marketplaces   市场来源注册；builtin 内置，用户可追加
  extension_catalog     市场同步快照；只读缓存，不驱动执行

Layer 1  Instances（安装层）← SSoT
  extension_instances   所有已安装扩展的统一记录（含子组件 parent_id）

Layer 2  Runtime（运行时层）
  mcp_servers（015）    MCP 进程连接配置；MCPManager 唯一消费方
  skills（008）         script/wasm 执行元数据 + instructions 全文
  plugins（021）        Bundle 入口元数据（entrypoint/env）
  automations（017）    触发器 + Agent 任务配置；M13 Scheduler 消费方
```

**数据流**：`plugin_marketplaces → 同步 → extension_catalog → 安装 → extension_instances → 绑定 → Runtime 表`

`extension_instances` 是唯一跨层视图。前端查询、卸载全走此表。

---

## 2. 扩展类型

| ext_type | 核心能力 | 运行时绑定 | 典型来源 |
|----------|---------|-----------|---------|
| `mcp` | 外部工具进程（JSON-RPC 2.0 over stdio/HTTP） | `mcp_servers` → MCPManager | marketplace / user |
| `skill` | 行为指令集（SKILL.md）或 Wasm 执行单元 | `skills`（008） | marketplace / learned |
| `plugin` | Skills + MCP + Hooks 的打包分发单元 | `plugins`（021）+ 子组件各自绑定 | marketplace |
| `app` | URL 应用，通过 WebProxy HTTP 代理访问 | 无独立表（URL 存 extension_instances） | marketplace / user |
| `automation` | 触发器 + Agent 任务（cron/webhook/both/manual；规划：event/github） | `automations`（017） | user / marketplace |
| `agent` | 外部 AI Agent 端点（A2A 协议）暴露为工具 | `mcp_servers`（transport=a2a） | marketplace / user |

### 2.1 多厂商格式适配

市场插件包（`.tar.gz`）内的清单文件通过 `pkg/extensions/marketplace/adapter.go` 统一解析为 `RegistryEntry`：

| 清单文件 | 厂商 | 安装结果 |
|---------|------|---------|
| `ai-plugin.json`（api.type=mcp） | OpenAI | mcp_servers，启动 MCP 进程 |
| `ai-plugin.json`（api.type=openapi） | OpenAI | app 类型，URL + OpenAPI schema 存储 |
| `.claude-plugin/plugin.toml` / `plugin.toml`（含 command） | Anthropic | mcp_servers |
| `.claude-plugin/plugin.json` | Anthropic | plugin 类型 |
| `skills.yaml` / `agent-manifest.yaml`（含 command） | Google | mcp_servers |
| `skills.yaml`（无 command，含 name） | Google | skills（script runtime） |

Polaris 原生格式（`SKILL.md` / `plugin.json`）由 `pkg/extensions/marketplace/loader.go` 处理。

### 2.2 origin 枚举

| origin | 含义 | trust_tier 默认值 |
|--------|------|-----------------|
| `builtin` | 程序内嵌生存工具（bash, search_extension, install_extension） | 4 TrustSystem |
| `official` | 官方市场推荐包 | 3 TrustOfficial |
| `marketplace` | 第三方社区市场 | 继承 extension_catalog |
| `user` | 用户手动创建/配置 | 1 TrustLocal |
| `learned` | M9 自演化 promote | 1 TrustLocal |

---

## 3. 技能执行模式

Skill 有两种执行模式，在 SKILL.md frontmatter 的 `mode` 字段声明：

| mode | 机制 | 触发时机 | 适用场景 |
|------|------|---------|---------|
| `tool`（默认） | 暴露为 `skill:{name}` LLM 工具；LLM 主动 tool_use 调用 | 按需，LLM 决策 | 专项任务技能（代码审查、PR 规范） |
| `ambient` | 将 instructions 注入每次请求的 system prompt | 会话开始时自动加载 | 全局行为规范（输出格式、安全检查） |
| `both` | 同时暴露为工具 + 注入 system prompt | 双路径 | 同时影响行为且可显式调用 |

**ambient 加载规则**：
- 查询 `skills WHERE mode IN ('ambient','both') AND deprecated=0`，按 trust_tier 排序
- 注入位置：system prompt ImmutableCore 区末尾，TaintedData 区之前
- 总字符限制：ambient skills 合计 ≤ 4000 字符（不得占用超过 ~10% 上下文窗口）
- 超限时优先保留 trust_tier 高的，其余截断并 WARN

**代码约束**：
- `server.go injectSystemPrompt()` 负责 ambient 注入
- `buildToolSchemas()` 负责 tool 模式的 schema 构建
- 两条路径互不干扰

---

## 4. 工具发现与懒加载

当已安装工具总数超过阈值（默认 40 个），切换到懒加载模式，避免 context 爆炸：

```
正常模式（tools ≤ 40）：
  buildToolSchemas() 全量返回所有 builtin + mcp + skill:tool 的 schema

懒加载模式（tools > 40）：
  buildToolSchemas() 仅返回：
    1. 核心 builtin 工具（trust_tier=4）
    2. search_tools 元工具（固定暴露）
  LLM 使用 search_tools(query) 按需发现并加载具体工具
```

**search_tools 元工具**（builtin, trust_tier=4）：

```json
{
  "name": "search_tools",
  "description": "搜索并激活可用工具/技能。返回匹配的工具 schema，激活后本次对话可调用。",
  "parameters": {
    "query": "string",
    "type": "string? // mcp|skill|builtin|any"
  }
}
```

执行：`SELECT name,description FROM (mcp_schemas UNION skills UNION builtins) WHERE ... LIKE '%query%' LIMIT 10`，将命中结果的完整 schema 注入后续 tool_use 可用列表。

---

## 5. 安装流

### 5.1 MCP

```
POST /v1/plugins/install {catalog_id, type=mcp}
  1. Manager.InstallExtension() → Cedar Gate 拦截（trust_tier / permission_mode）
  2. 写 extension_instances (status=installing)
  3. INSERT mcp_servers（继承 trust_tier）
  4. MCPManager.startMCPServer() → goroutine 连接 + 工具注册到 InProcessSandbox
  5. UPDATE extension_instances SET status=installed, runtime_id=mcp_servers.id
```

### 5.2 Skill

```
POST /v1/plugins/install {catalog_id, type=skill}
  1. Manager.InstallExtension() → Cedar Gate
  2. 写 extension_instances (status=downloading)
  3. HTTP 下载 tar.gz → 解压 → install_path
  4. 读 SKILL.md → 解析 frontmatter（name, description, mode）
  5. INSERT INTO skills(runtime='script', mode=?, instructions=SKILL.md全文, ...)
  6. UPDATE extension_instances SET status=installed

  mode='tool': 下次 buildToolSchemas() 自动包含
  mode='ambient': 下次请求的 injectSystemPrompt() 自动注入
```

### 5.3 Plugin Bundle

```
POST /v1/plugins/install {catalog_id, type=plugin}
  1. Manager.InstallExtension() → Cedar Gate（含 hooks 安全检查）
  2. 写 extension_instances (status=downloading, parent)
  3. HTTP 下载 tar.gz → 解压 → install_path
  4. 解析 plugin.json (PluginBundleManifest)：
     mcp_inline{} → installBundleMCP() → mcp_servers + 子 extension_instances
     mcp_servers（.mcp.json）→ 同上（safeJoin 路径校验）
     skills[] → installBundleSkill() → skills + 子 extension_instances（强制追加插件名前缀，形如 `pluginName-skillName`，实现 LLM 工具调用命名空间隔离）
     hooks{} → 写入 ~/.polarisagi/harness/hooks/，注册到 M7 HookRunner
     外部格式 → adapter.ParseManifestDir() → 按类型分发
  5. INSERT plugins (021) 写 bundle 入口元数据
  6. UPDATE parent extension_instances SET status=installed
```

### 5.4 Automation

```
POST /v1/plugins/install {catalog_id, type=automation}
  1. Manager.InstallExtension() → Cedar Gate
  2. 写 extension_instances (ext_type=automation)
  3. INSERT automations(022)：trigger_type, trigger_config, action_type, action_ref
     action_type: 'skill' | 'mcp_tool' | 'agent'
     action_ref:  对应 skill name / mcp tool name / agent id
  4. Scheduler.Register(automation_id) → 按 trigger_type 注册调度

  trigger_type='cron'    → 写 cron 表达式到 scheduler
  trigger_type='webhook' → 生成 /v1/automations/{id}/trigger 端点
  trigger_type='event'   → 订阅 outbox event type
  trigger_type='manual'  → 仅 POST /v1/automations/{id}/run 触发
```

### 5.5 Agent（外部 AI Agent）

```
POST /v1/plugins/install {catalog_id, type=agent}
  1. Manager.InstallExtension() → Cedar Gate（TrustTier 严格校验）
  2. 写 extension_instances (ext_type=agent)
  3. INSERT mcp_servers（transport='a2a', url=AgentCard URL）
  4. MCPManager 通过 A2A Client Discover 获取 Agent Card → 转换为 MCP tool schema
  5. Agent 以 "agent:{id}" 工具名注入 InProcessSandbox
```

### 5.6 市场同步（只同步不安装）

启动时 `bootMarketplaceInit` 后台拉取 `is_builtin=1` 市场源至 `extension_catalog`，仅作前端展示缓存。**不静默安装任何外部扩展**。

**边界探测 (Bundle Root Detection)**：同步爬虫（`discoverMarketplaceEntries`）在扫描市场仓库时，一旦探测到合法的插件清单文件（如 `plugin.json`、`plugin.toml`、`mcp.json`、`skills.yaml` 等），即判定该目录为一个**原子级插件包（Plugin Bundle）**，将其整体作为单个条目录入，并强制停止向下钻取其子目录。这避免了内部依附的零碎动作（如 `SKILL.md`）被摊平暴露到全局市场，彻底杜绝列表污染与大模型工具的全局同名冲突。

### 5.7 彻底卸载

```
DELETE /v1/plugins/{ext_id}
  1. 查 extension_instances（含 parent_id=ext_id 的子记录）
  2. 按 ext_type 清理运行时：
     mcp    → MCPManager.Remove() + DELETE mcp_servers
     skill  → SkillRegistry.Deprecate() 或 DELETE skills
     plugin → DELETE plugins + 递归卸载子组件
     automation → Scheduler.Deregister() + DELETE automations
     agent  → MCPManager.Remove()
  3. safeRemoveAll(install_path)（禁止 HTTP Handler 裸写 os.RemoveAll）
  4. DELETE extension_instances（含子记录）
  5. 非 builtin 第三方扩展 → 级联 DELETE extension_catalog
```

### 5.8 Plugin 自动生成（PluginCreator）

用户以自然语言描述意图，`PluginCreator` 调用 LLM 生成 TypeScript MCP 插件并写入本地文件系统。

```
用户意图（自然语言）
  → PluginCreator.GeneratePlugin()
  → LLM.Generate(systemPrompt, intent)
     返回 JSON: { name, description, typescript_code }
  → 写入 ~/.polarisagi/harness/extensions/local/{name}/
       src/index.ts          # TypeScript MCP 服务器（@modelcontextprotocol/sdk）
       package.json          # npm 清单（tsx + sdk 依赖，无需预编译）
       .codex-plugin/        # Polaris 原生 Plugin Bundle 清单
         plugin.json
       .mcp.json             # { command: "npx", args: ["tsx", "src/index.ts"] }
  → 返回 pluginDir（调用方负责后续注册到 extension_instances）
```

**语言约定**：官方插件仓库（polarisagi-plugins-official）及 PluginCreator 生成的插件均采用 TypeScript + `@modelcontextprotocol/sdk`，通过 `npx tsx` 直接运行，无需预编译。社区插件可使用任意语言，加载层（loader/marketplace）格式无关，仅读 `.mcp.json` 的 `command/args`。

---

## 6. 信任门控

> 策略制定见 M11 Cedar-Gate。本节仅描述触发点。

**核心约束**：所有安装路径（手动、Agent 自治、AI 生成）必须通过 `Manager.InstallExtension` 中央网关，不可绕过。

| trust_tier | 安装时 | 运行时 |
|------------|-------|-------|
| 4 System   | 不走此流（程序内嵌） | 直接执行 |
| 3 Official | 自动确认 | Sbx-L2，TaintMedium |
| 2 Community | 自动确认 | Sbx-L1，TaintHigh |
| 1 Local    | 用户确认 | Sbx-L1，TaintHigh |
| 0 Untrusted | 拒绝 | — |

安装时 trust_tier 强制从 extension_catalog 继承，禁止客户端覆盖。Plugin hooks 存在时 trust_tier < 3 触发 HITL 审批。

### 6.1 所有安装入口必须过门——禁止并行旁路

系统存在多条写入 `mcp_servers` / `extension_instances` 的 HTTP 端点，**每一条**都必须独立调用 `Manager.InstallExtension`，不得以"父路径已审查"为由跳过：

| 端点 | 必须过门 | 常见违规写法 |
|------|---------|------------|
| `POST /v1/plugins/install` | ✅ | — |
| `POST /v1/mcp/create` | ✅ | — |
| `POST /v1/mcp-servers`（运维管理接口） | ✅ **不可例外** | 直接写库，无 PolicyGate |
| Plugin Bundle 内 `installBundleMCP()` | ✅ **每个子 MCP 独立过门** | 父插件通过后子 MCP 无审查 |
| `PUT /v1/mcp-servers/{id}`（更新） | ✅ | — |

### 6.2 安全门 nil 不等于可选

`Manager` 通过依赖注入传入。**`if installMgr != nil { gate }` 之后继续执行的写法是 R1.14 反模式**——nil 时必须返回 503，不得静默绕过。安全门是强制路径，不是可选优化。

### 6.3 Plugin Bundle 子组件门控

Plugin Bundle（`§5.3`）安装时会展开子 MCP / 子 Skill。子组件不能继承父插件的门控结果——**每个子 MCP 必须独立调用 `Manager.InstallExtension`**，失败则跳过该子组件并记录 Warn，不中断父插件安装整体。

### 6.4 HasHooks 判断规则

市场安装路径在下载前无法读取 plugin.json，因此 hooks 存在性无法确认。**保守策略**：`plugin` 类型且 `trust_tier < 3` 时，`HasHooks` 置 `true`，强制触发 HITL 审批。trust_tier ≥ 3（Official）的插件方可豁免。

---

## 7. 文件系统布局

```
~/.polarisagi/harness/
├── extensions/
│   ├── skill/{ext_id}/         # script/wasm 技能安装目录
│   │   ├── SKILL.md            # frontmatter: name, description, mode
│   │   └── impl.wasm           # wasm runtime 时存在
│   ├── plugin/{ext_id}/        # Plugin Bundle 解压（市场安装）
│   │   ├── plugin.json         # PluginBundleManifest
│   │   ├── skills/             # Bundle 内技能
│   │   └── hooks/              # Bundle 内钩子脚本
│   ├── local/{name}/           # PluginCreator 自动生成（TypeScript）
│   │   ├── src/index.ts        # MCP 服务器实现
│   │   ├── package.json        # npm 清单（tsx + @modelcontextprotocol/sdk）
│   │   ├── .codex-plugin/
│   │   │   └── plugin.json     # Polaris 原生清单
│   │   └── .mcp.json           # { command:"npx", args:["tsx","src/index.ts"] }
│   └── agent/{ext_id}/         # Agent Card 缓存
│       └── agent-card.json
├── hooks/                      # 全局钩子（来自 Plugin Bundle 安装 + 用户配置）
├── cache/{marketplace_id}/     # 市场下载临时区（安装后清理）
└── polaris.db
```

`extension_instances.install_path`：skill/plugin 为绝对路径，mcp/automation/agent 为空字符串。

---

## 8. 调用路由

### 8.1 工具列表构建（每次推理请求）

```go
func buildToolSchemas() []ToolSchema {
  if totalTools() <= LazyLoadThreshold {
    // 正常模式：全量
    return builtin + mcpMgr.ListToolSchemas() + skillToolSchemas()
  }
  // 懒加载模式
  return builtinCore + []ToolSchema{searchToolsMeta}
}
```

### 8.2 工具执行路由（toolExec closure）

```
LLM tool_use {name, input}
  → toolExec(ctx, name, args)
  ├── "skill:{name}"   → DB 读 skills.instructions + input → 返回给 LLM 执行
  ├── "agent:{id}"     → A2A Client → 外部 Agent 端点 → 返回结果
  ├── "search_tools"   → 查询工具库 → 返回命中工具 schema（激活到当前会话）
  └── 其他            → sandboxRouter.Execute → InProcessSandbox.Execute(name)
                           ├── builtin 工具（startup 注入）→ 直接执行
                           └── mcp 工具（MCPManager 注入）→ CallToolTainted()
```

### 8.3 Ambient Skill 注入（每次推理请求）

```go
func injectSystemPrompt(basePrompt string) string {
  skills := db.Query(`SELECT instructions FROM skills
                      WHERE mode IN ('ambient','both') AND deprecated=0
                      ORDER BY trust_tier DESC`)
  ambient := truncateToLimit(join(skills.instructions), 4000)
  return basePrompt + "\n\n## Active Skills\n" + ambient
}
```

### 8.4 MCP Async Tasks（MCP spec 2025-11-25）

对耗时 MCP 工具（预估 > 5s），MCPManager 支持异步任务模式：

```
toolExec "mcp_tool_xxx" → MCPManager.CallToolAsync()
  → 立即返回 {task_id, status=pending}
LLM 收到 task_id → 调用 get_task_result(task_id) 轮询
MCPManager 内部 goroutine 监控任务完成 → 写入 tasks 缓存
```

`tasks_cache` 为内存 map（task_id → result），超时 TTL = 300s。

---

## 9. 自动化（Automation Extension）

自动化是**有触发器的 Agent 任务**，是第一类扩展类型（ext_type='automation'）。设计参考 Codex Automations + Claude Code Routines 理念：**automation prompt 是自包含的任务规约**（须声明目标与成功标准），Agent 在独立上下文中执行，结果推送至指定目标。这与"对话延续"根本不同——每次执行产生独立 session，与主聊天互相隔离。

### 9.1 数据模型

DDL 见 `internal/protocol/schema/017_automations.sql`。核心字段：`prompt`（自包含任务规约）、`trigger_type`（cron/webhook/both/manual）、`cron_schedule`、`working_dir`、`reasoning_effort`、`result_action`（session/channel:{id}/silent）、`sandbox_level`、`cedar_rules_json`、`next_run_at`（cronTick 预计算索引）、`last_run_status`（ok/error/running 防重入）、`created_at`/`updated_at`（审计时间戳，自动生成）。

**执行历史表** `automation_runs`（同 017 文件）：每次触发产生一条 run 记录，包含 `trigger`（触发类型）、`status`（running/ok/error/timeout）、`session_id`（关联 chat_sessions，可跳入查看执行过程）、`prompt_snapshot`（执行时 prompt 快照，防 prompt 修改导致追溯困难）。

### 9.2 执行环境（env_type）

参考 Codex Automations 的三种执行模式（`worktree / local / direct`）与 Claude Code Routines 的 `repositories` 概念：

| env_type | 说明 | 工作目录 | Git 隔离 | 对应 Sandbox |
|----------|------|---------|---------|------------|
| `chat` | 纯 Agent 对话，无文件访问 | 无 | 无 | L1 InProcess |
| `local` | 读写 working_dir（项目文件） | `working_dir` | 无（直写主分支） | L2 Wasm |
| `worktree` | Git worktree 隔离，执行后可生成 PR | 自动创建临时 worktree | ✓ `auto/{date}/{task_id}` | L2 Wasm + Git |

> `env_type` 当前通过 `working_dir` 隐式表达（空=chat，非空=local）。`worktree` 模式为目标设计，需在 DDL 增加 `env_type TEXT NOT NULL DEFAULT 'chat'`，代码实现时同步创建 worktree 并在完成后生成 PR。

**禁止**：`model_id` 不对 automation 暴露——系统根据 `reasoning_effort` 自动映射 model_roles（用户不感知模型名）。

### 9.3 触发路径

```
trigger_type='cron'    → cronTick(60s 轮询，防重入: last_run_status != 'running')
                         → next_run_at <= NOW() → go executeAutomation(ctx, a, "cron")

trigger_type='webhook' → POST /v1/webhooks/{channelType}/{channelID}
                         → HMAC-SHA256 验签（密钥存 CredentialVault）
                         → go executeAutomation(ctx, a, "webhook")   // 与 dispatchChannelMessage 并行

trigger_type='both'    → cron + webhook 两路均可独立触发，互不阻塞

trigger_type='manual'  → POST /v1/automations/{id}/trigger → executeAutomation(ctx, a, "manual")
                         → 响应 202 Accepted + {run_id}，异步执行

// 规划中：
trigger_type='api'     → POST /v1/automations/{id}/trigger {text: "外部上下文"}
                         → text 字段追加注入 prompt，作为 API-driven 触发的上下文
trigger_type='event'   → Outbox Worker 订阅 events.type → go executeAutomation(ctx, a, "event")
trigger_type='github'  → Webhook + GitHub event 过滤（PR/Release + author/label/branch/regex）
```

calcNextRun 支持：5 字段 cron 表达式（含 `*/n` 步长）+ 别名（@hourly/@daily/@weekly/@monthly）+ 完整 day/weekday 匹配。

### 9.4 执行流（executeAutomation）

```
executeAutomation(ctx, a, trigger):
  1. INSERT automation_runs (id=run_{hex}, status='running', prompt_snapshot=a.Prompt, trigger=trigger)
  2. UPDATE automations SET last_run_status='running', next_run_at=calcNextRun(cron_schedule)
  3. go (bgCtx, timeout 按 reasoning_effort 动态: low=5m/medium=15m/high=30m/ultra=60m):
       4. 创建独立 chat_sessions（source='automation', automation_id=a.ID）→ sessionID
       5. 注入 ImmutableCore（含 env_type、working_dir、cedar_rules_json 安全上下文）
       6. p.StreamInfer(bgCtx, sessionID, a.Prompt)   // 独立推理上下文，禁污染主会话
       7. 处理 result_action：
            'session'       → 记录留在步骤4的 session（用户在会话列表可见🤖标记，可继续对话）
            'channel:{id}'  → dispatchChannelMessage(channelID, assistantText)
            'silent'        → 仅落库，不通知
       8. UPDATE automation_runs SET session_id=sessionID, status=ok/error, finished_at=NOW()
       9. UPDATE automations SET last_run_status, run_count+1, last_run_error=errMsg
```

**不变量**：automation 必须使用独立 sub-inference 上下文（`inv_M13_03` cron pool 隔离），禁止注入主聊天上下文。

### 9.5 工作流（Workflow）

当前实现通过单一 prompt 指令 Agent 内部完成多步任务（Agent 自主调用工具→技能→MCP 形成流程）。这是"隐式工作流"——Agent 是流程编排器。

结构化工作流（显式 DAG）为目标设计，将多个 Action 按依赖图顺序编排，每步输出作为下一步输入：

```json
{
  "steps": [
    { "id": "s1", "type": "mcp_tool", "ref": "github:list_prs", "input": {} },
    { "id": "s2", "type": "skill",    "ref": "code_review",     "input": { "prs": "{{s1.output}}" }, "depends_on": ["s1"] },
    { "id": "s3", "type": "channel",  "ref": "slack:notify",    "input": { "summary": "{{s2.output}}" }, "depends_on": ["s2"] }
  ],
  "on_error": "stop"
}
```

DAG 执行器复用 M4 `dag_executor.go`（`pkg/cognition/kernel/dag_executor.go`）。实现时 automations 表新增 `workflow_json TEXT DEFAULT ''`，非空时走 DAG 路径替代 StreamInfer。

### 9.6 防重入与 HITL 审批

**防重入**（cronTick 查询加条件）：
```sql
AND last_run_status != 'running'
```

**HITL 审批**：automation 执行触发危险操作（WriteNetwork / Privileged / 超预算）→ M11 Cedar-Gate 拦截 → automation_runs.status = 'suspended' → SSE push `event:approval_pending` → 用户在 `/automation` 页"待办审批"Tab 处理 → POST /v1/approvals/{id}/resolve → 恢复或取消执行。

**禁止**：automation 不得自动降级绕过 Cedar-Gate（`inv_M11_02`）。

---

## 10. 跨代理协作（Agent Extension + A2A）

`agent` 扩展类型将外部 AI Agent 以工具形式暴露给本地 LLM：

```
安装 agent 扩展 → 获取远端 Agent Card（/.well-known/agent-card.json）
  → 解析 capabilities / skills / authentication
  → INSERT mcp_servers(transport='a2a', url=AgentCard.url)
  → MCPManager.Add(serverID, A2AClientConfig)
  → 以 "agent:{serverID}" 注册到 InProcessSandbox

LLM tool_use "agent:{serverID}" {task: "...", context: {...}}
  → toolExec → A2A Client → POST {AgentCard.url}/tasks/send
  → 等待 A2A response（支持 streaming / async）
  → 返回 ToolResult
```

**Agent Card 标准字段**（遵循 A2A Protocol）：

```json
{
  "name": "string",
  "description": "string",
  "url": "https://...",
  "version": "1.0.0",
  "capabilities": { "streaming": true, "pushNotifications": false },
  "skills": [{ "id": "skill_id", "name": "...", "description": "..." }],
  "authentication": { "schemes": ["Bearer"] }
}
```

本地 Agent 对外暴露 Agent Card：`GET /.well-known/agent-card.json` → 由 M13 Gateway 自动生成（基于已安装 skills + mcp_servers 的能力描述）。

---

## 11. 学习技能归并（M9 → Extension Registry）

M9 Self-Improvement Engine promote 候选技能时：

1. 写 `extension_instances`（ext_type=skill, origin=learned, trust_tier=1）
2. 直接 INSERT `skills` 表（runtime='script'，instructions=生成的 SKILL.md，mode='tool'）
3. install_path 指向 `extensions/skill/learned/{ext_id}/`

**禁止**：M9 不得绕过 extension_instances 直写 skills 表（inv_M6_02）。

技能经过足够次数成功调用后，Logic Collapse 将其编译为 wasm runtime（M6 §2.2）：
- wasm 编译完成 → UPDATE skills SET runtime='wasm'，instructions 清空
- Wasm 技能不再走 tool_use 返回 instructions 路径，改走 SkillExecutor.ExecuteSkill()

---

## 12. 表引用速查

| 表 | 迁移文件 | 消费方 |
|----|---------|-------|
| `plugin_marketplaces` | 018 | M13 API（市场注册） |
| `extension_catalog` | 019 | M13 API（目录缓存） |
| `extension_instances` | 020 | M13 API（SSoT） |
| `mcp_servers` | 015 | M7 MCPManager |
| `skills` | 008 | M6 SkillRegistry + server.buildToolSchemas() |
| `plugins` | 021 | plugin_catalog.go（bundle 元数据） |
| `automations` | 017 | M13 Scheduler（`pkg/gateway/server/cron.go`） |
| `automation_runs` | 017 | M13 Scheduler — 执行历史 |
| `cron_jobs` | 014 | 旧版定时任务表，由 017_automations 接管，逐步废弃 |

**已删除**（不再存在）：`skill_sources`、`apps`——职责归入 `extension_instances`（020）。
