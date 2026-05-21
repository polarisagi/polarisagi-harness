-- 迁移 providers 表：vertex → google_agent_platform，新增 role 列
-- SQLite 不支持 ALTER COLUMN / DROP CONSTRAINT，使用重建策略
CREATE TABLE IF NOT EXISTS providers_v2 (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK(type IN ('openai_compat','anthropic','google_agent_platform','ollama')),
    base_url    TEXT NOT NULL DEFAULT '',
    api_key     TEXT NOT NULL DEFAULT '',
    model_id    TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    sa_key_json TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL DEFAULT 'general' CHECK(role IN ('general','default','reasoning')),
    enabled     INTEGER NOT NULL DEFAULT 1,
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
INSERT OR IGNORE INTO providers_v2
    SELECT id, name,
        CASE WHEN type='vertex' THEN 'google_agent_platform' ELSE type END,
        base_url, api_key, model_id, project_id, location, sa_key_json,
        'general', enabled, is_default, created_at, updated_at
    FROM providers;
DROP TABLE providers;
ALTER TABLE providers_v2 RENAME TO providers;
CREATE INDEX IF NOT EXISTS idx_providers_type    ON providers(type);
CREATE INDEX IF NOT EXISTS idx_providers_enabled ON providers(enabled);
CREATE INDEX IF NOT EXISTS idx_providers_role    ON providers(role);
