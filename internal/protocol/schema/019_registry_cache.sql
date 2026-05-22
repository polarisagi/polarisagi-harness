-- ============================================================================
-- 019_registry_cache: 市场同步缓存（Layer 0 只读快照）
-- ============================================================================
-- 架构角色: 存储市场同步后的目录条目快照。只读缓存，不驱动运行时执行。
--   payload: JSON，含 command/args/env/manifest_url 等类型特定字段。
-- 关联: M13-bis(Extension Registry) §1
-- ============================================================================

CREATE TABLE IF NOT EXISTS registry_cache (
    id             TEXT PRIMARY KEY,            -- "{publisher}/{name}" slug
    marketplace_id TEXT NOT NULL,               -- plugin_marketplaces.id
    type           TEXT NOT NULL,               -- 'mcp' | 'skill' | 'plugin' | 'app'
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    publisher      TEXT NOT NULL DEFAULT '',
    trust_tier     INTEGER NOT NULL DEFAULT 1,
    url            TEXT NOT NULL DEFAULT '',
    payload        TEXT NOT NULL,               -- JSON 全字段快照
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_registry_cache_mp   ON registry_cache(marketplace_id);
CREATE INDEX IF NOT EXISTS idx_registry_cache_type ON registry_cache(type);
