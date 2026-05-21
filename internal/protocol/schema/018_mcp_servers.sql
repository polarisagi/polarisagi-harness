-- MCP Server 连接配置表（pkg/action/MCPManager 使用）
CREATE TABLE IF NOT EXISTS mcp_servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    transport   TEXT NOT NULL DEFAULT 'stdio', -- 'stdio' | 'sse' | 'streamable_http'
    command     TEXT NOT NULL DEFAULT '',      -- stdio: 可执行命令（如 npx）
    args        TEXT NOT NULL DEFAULT '[]',    -- stdio: JSON array
    env         TEXT NOT NULL DEFAULT '{}',    -- stdio: JSON object
    url         TEXT NOT NULL DEFAULT '',      -- sse / streamable_http: 端点 URL
    enabled     INTEGER NOT NULL DEFAULT 1,
    timeout     INTEGER NOT NULL DEFAULT 30,   -- 单次请求超时（秒）
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);
