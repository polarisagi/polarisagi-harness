-- 024_plugin_marketplaces.sql
-- 记录插件市场（Marketplaces）配置，作为插件的上游源头。
CREATE TABLE IF NOT EXISTS plugin_marketplaces (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL, -- 'skill' | 'mcp'
    publisher   TEXT NOT NULL,
    repo_url    TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_builtin  INTEGER NOT NULL DEFAULT 0, -- 1 为内置，不可删除
    trust_tier  INTEGER NOT NULL DEFAULT 2, -- 3=Official, 2=Community
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL
);
