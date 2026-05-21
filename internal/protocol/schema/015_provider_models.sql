-- 两层架构迁移：providers（凭据） → provider_models（具体模型+角色）
-- 从 providers 表拆出 model_id / role / is_default，新建 provider_models

CREATE TABLE IF NOT EXISTS provider_models (
    id          TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    model_id    TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL DEFAULT 'general' CHECK(role IN ('general','default','reasoning')),
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    FOREIGN KEY(provider_id) REFERENCES providers(id) ON DELETE CASCADE
);

-- 迁移：把现有 providers.model_id / role 搬进 provider_models（仅当 model_id 非空）
INSERT OR IGNORE INTO provider_models(id, provider_id, model_id, name, role, enabled, created_at, updated_at)
    SELECT 'mdl_' || substr(id, 6), id, model_id, model_id, role, enabled, created_at, updated_at
    FROM providers WHERE model_id != '';

-- 重建 providers 表，去除 model_id / role / is_default
CREATE TABLE providers_v3 (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK(type IN ('openai_compat','anthropic','google_agent_platform','ollama')),
    base_url    TEXT NOT NULL DEFAULT '',
    api_key     TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    sa_key_json TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
INSERT INTO providers_v3(id, name, type, base_url, api_key, project_id, location, sa_key_json, enabled, created_at, updated_at)
    SELECT id, name, type, base_url, api_key, project_id, location, sa_key_json, enabled, created_at, updated_at
    FROM providers;
DROP TABLE providers;
ALTER TABLE providers_v3 RENAME TO providers;

CREATE INDEX IF NOT EXISTS idx_providers_type           ON providers(type);
CREATE INDEX IF NOT EXISTS idx_providers_enabled        ON providers(enabled);
CREATE INDEX IF NOT EXISTS idx_provider_models_provider ON provider_models(provider_id);
CREATE INDEX IF NOT EXISTS idx_provider_models_role     ON provider_models(role);
CREATE INDEX IF NOT EXISTS idx_provider_models_enabled  ON provider_models(enabled);
