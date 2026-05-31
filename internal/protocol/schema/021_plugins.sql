-- ============================================================================
-- 021_plugins: 已安装插件的运行时状态表
-- ============================================================================
-- 架构角色: 对应 Codex config.toml 的 [plugins."xxx"] 配置段。
--   每条记录 = 一个已安装插件的运行时状态。
--   install_path 是运行时唯一来源：
--     - MCPManager   启动时调用 LoadFromPlugins()，读此表扫描各插件的
--                    install_path/.mcp.json，按 mcp_policy 启动子 MCP。
--     - Ambient Skill sse.go 读 enabled=1 插件，扫描 install_path/skills/
--                    目录，将 exec_mode=ambient 的 SKILL.md 注入 system prompt。
--   插件内的 MCP/Skill 不写 mcp_servers/skills 全局表，避免跨边界数据同步问题。
--   mcp_servers/skills 全局表仅接收独立安装的 MCP 和 Skill（parent_id=''）。
-- 消费方:
--   MCPManager    - LoadFromPlugins() 按 mcp_policy 动态启动子 MCP
--   SSE handler   - 扫描 install_path/skills/ 注入 ambient skill
--   UI/API 层     - GET /v1/plugins/catalog 展示已安装插件列表及开关状态
-- 关联: extension_instances.runtime_id → plugins.id (ext_type='plugin')
-- ============================================================================

CREATE TABLE IF NOT EXISTS plugins (
    id           TEXT    PRIMARY KEY,           -- "pl_{8hex}"
    name         TEXT    NOT NULL UNIQUE,        -- kebab-case，Codex plugin namespace
    version      TEXT    NOT NULL DEFAULT '1.0.0',
    display_name TEXT    NOT NULL DEFAULT '',    -- interface.displayName
    description  TEXT    NOT NULL DEFAULT '',
    publisher    TEXT    NOT NULL DEFAULT '',    -- author.name / plugin.json author.name
    homepage     TEXT    NOT NULL DEFAULT '',    -- plugin.json homepage
    install_path TEXT    NOT NULL DEFAULT '',    -- 插件根目录绝对路径，运行时加载唯一来源
    enabled      INTEGER NOT NULL DEFAULT 1,     -- 全局开关；0 时 MCPManager/SSE 跳过此插件
    trust_tier   INTEGER NOT NULL DEFAULT 1,     -- 0-4，继承自 extension_instances
    catalog_id   TEXT    NOT NULL DEFAULT '',    -- extension_catalog.id；用户手动安装时为空
    -- 子 MCP 运行时策略（对应 Codex [plugins.xxx.mcp_servers.yyy]）
    -- JSON map: { "server-name": { "enabled": true, "approval_mode": "prompt", "enabled_tools": ["x"] } }
    mcp_policy   TEXT    NOT NULL DEFAULT '{}',
    -- plugin.json 完整快照（运行时缓存；权威来源始终是 install_path/.codex-plugin/plugin.json）
    manifest     TEXT    NOT NULL DEFAULT '{}',
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_plugins_enabled ON plugins(enabled);
CREATE INDEX IF NOT EXISTS idx_plugins_catalog ON plugins(catalog_id) WHERE catalog_id != '';
CREATE INDEX IF NOT EXISTS idx_plugins_trust   ON plugins(trust_tier);
