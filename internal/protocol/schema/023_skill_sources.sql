-- 023_skill_sources.sql
-- 记录已安装的 Skill/Plugin 来源仓库（ADR-0016 §3）。
-- 与 mcp_servers 表平行：mcp_servers 记录运行时服务，skill_sources 记录技能仓库。
CREATE TABLE IF NOT EXISTS skill_sources (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL DEFAULT 'skill',     -- 'skill' | 'plugin' | 'app'
    publisher  TEXT NOT NULL DEFAULT '',
    trust_tier INTEGER NOT NULL DEFAULT 1,
    repo_url   TEXT NOT NULL,                     -- GitHub 仓库 URL
    catalog_id TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_skill_sources_catalog
    ON skill_sources(catalog_id) WHERE catalog_id != '';
