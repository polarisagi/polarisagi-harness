-- ============================================================================
-- 017_automations: 自动化任务（沙箱边界 + 推理配置）
-- ============================================================================

CREATE TABLE IF NOT EXISTS automations (
    id               TEXT    PRIMARY KEY,
    name             TEXT    NOT NULL,
    prompt           TEXT    NOT NULL,
    cron_schedule    TEXT    NOT NULL,   -- e.g. '0 9 * * 1-5'
    workspace_dir    TEXT    NOT NULL,
    env_type         TEXT    NOT NULL DEFAULT 'local',   -- local | worktree | chat
    model_id         TEXT    NOT NULL DEFAULT 'auto',
    reasoning_effort TEXT    NOT NULL DEFAULT 'medium',  -- low | medium | high | ultra
    sandbox_level    INTEGER NOT NULL DEFAULT 2,         -- Sandbox-L2
    cedar_rules_json TEXT    NOT NULL,
    is_template      INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
