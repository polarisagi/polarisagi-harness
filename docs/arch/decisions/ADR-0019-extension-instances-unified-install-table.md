# ADR-0019: extension_instances 统一安装实例表

**状态**: Accepted
**日期**: 2026-05-22
**取代**: skill_sources（023）、plugins（027）、apps（028）三表散乱安装记录
**相关**: ADR-0016（TrustTier）、M13-bis-Extension-Registry.md

---

## 背景

现有安装记录散落在四个表中：`skill_sources`、`plugins`、`apps`、`mcp_servers`（通过 `catalog_id` 标记已安装）。导致三个结构性问题：

1. **安装状态查询需 UNION 四表**：`getInstalledCatalogIDs` 发四条 SQL 才能得到完整视图。
2. **安装断层**：`installSkillSource` 只写 `skill_sources`，未下载文件、未写 `skills`（008）运行时表，SkillExecutor 永远找不到该 skill。
3. **026_skills.sql 是死代码**：008 已建 `skills` 表，026 的 `CREATE TABLE IF NOT EXISTS skills` 永远不执行，但其意图（目录级 skill 记录）无人承接。

---

## 决策

新增 `extension_instances` 表（迁移文件 `020`），作为所有已安装扩展的单一事实来源。

**关键字段**：
- `ext_type`：`mcp` | `skill` | `plugin` | `app`
- `origin`：`builtin` | `marketplace` | `user` | `learned`
- `catalog_id`：关联 `extension_catalog.id`；user/learned 时为空
- `runtime_id`：安装完成后写入，指向 `mcp_servers.id` 或 `skills.name`
- `install_path`：文件系统绝对路径；MCP/App 为空字符串
- `status`：`downloading` | `installed` | `error` | `disabled`

删除 `skill_sources`、`apps` 两表 DDL。`plugins` 表曾一度被删除，但后续（参见 021_plugins.sql）被重新引入，专门作为独立脚本型应用插件的运行时记录。Schema 整体重整为 001-021 标准编号。

---

## 被拒绝的方案

| 方案 | 拒绝原因 |
|------|---------|
| 按类型分四个安装表（mcp_installs / skill_installs ...） | 前端需 UNION 查询，安装状态无法单表追踪 |
| 直接用 `mcp_servers` / `skills` 表的 `catalog_id` 标记安装 | 运行时配置表与安装元数据职责混淆，skill 无法记录 `install_path` 和 `status` |
| 保留 `skill_sources`，只修复安装流 | 不消除表语义重叠，`getInstalledCatalogIDs` 仍需多表 UNION |

---

## 影响

- `pkg/interface/server/plugin_catalog.go`：`installSkillSource` / `handleUninstallPlugin` / `appendCustomCatalogs` 重写
- `internal/protocol/schema/`：新增 034、035、036 迁移文件
- `M13-bis-Extension-Registry.md`：安装流完整描述
- M9 Self-Improvement Engine：promote 路径必须经 `extension_instances` → `SkillRegistry`（inv_M6_02）
