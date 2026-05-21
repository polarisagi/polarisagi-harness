-- 027_plugins.sql
-- Dedicated table for Plugins (based on OpenAI ai-plugin.json manifest)
CREATE TABLE IF NOT EXISTS plugins (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    manifest_url TEXT NOT NULL,                     -- API/Plugin URL for ai-plugin.json
    publisher    TEXT NOT NULL DEFAULT '',
    trust_tier   INTEGER NOT NULL DEFAULT 1,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
