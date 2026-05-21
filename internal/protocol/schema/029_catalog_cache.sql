-- 029_catalog_cache.sql
-- Table to cache marketplace catalog items after syncing
CREATE TABLE IF NOT EXISTS catalog_cache (
    id             TEXT PRIMARY KEY,
    marketplace_id TEXT NOT NULL,
    type           TEXT NOT NULL, -- mcp, skill, plugin, app
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    publisher      TEXT NOT NULL DEFAULT '',
    trust_tier     INTEGER NOT NULL DEFAULT 1,
    url            TEXT NOT NULL DEFAULT '',
    payload        TEXT NOT NULL, -- JSON for command, args, env, manifest_url etc.
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
