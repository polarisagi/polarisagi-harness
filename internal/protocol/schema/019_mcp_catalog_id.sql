-- 为 mcp_servers 表追加 catalog_id 列，记录安装来源（插件目录条目 ID）
ALTER TABLE mcp_servers ADD COLUMN catalog_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_mcp_servers_catalog ON mcp_servers(catalog_id);
