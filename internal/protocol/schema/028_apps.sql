-- 028_apps.sql
-- Dedicated table for Apps (independent interactive capabilities)
CREATE TABLE IF NOT EXISTS apps (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL,                     -- App web endpoint or repository
    publisher    TEXT NOT NULL DEFAULT '',
    trust_tier   INTEGER NOT NULL DEFAULT 1,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
