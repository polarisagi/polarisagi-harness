-- 026_skills.sql
-- Dedicated table for Skills (atomic functions executed in a sandbox)
CREATE TABLE IF NOT EXISTS skills (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    repo_url    TEXT NOT NULL,                     -- GitHub 仓库 URL
    entrypoint  TEXT NOT NULL DEFAULT '',          -- e.g., 'python main.py'
    publisher   TEXT NOT NULL DEFAULT '',
    trust_tier  INTEGER NOT NULL DEFAULT 1,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
