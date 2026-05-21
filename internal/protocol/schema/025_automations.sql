-- 025_automations.sql
-- 建立自动化任务表，支持沙盒边界控制
CREATE TABLE IF NOT EXISTS automations (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    prompt           TEXT NOT NULL,
    cron_schedule    TEXT NOT NULL,  -- e.g., '0 9 * * 1-5'
    workspace_dir    TEXT NOT NULL,  -- e.g., 项目根目录或特定的 Git Worktree
    env_type         TEXT NOT NULL DEFAULT 'local', -- local | worktree | chat
    model_id         TEXT NOT NULL DEFAULT 'auto',
    reasoning_effort TEXT NOT NULL DEFAULT 'medium', -- low | medium | high | ultra
    sandbox_level    INTEGER NOT NULL DEFAULT 2, -- 对应 [Sandbox-L2]
    cedar_rules_json TEXT NOT NULL,  -- 显式授权的特殊规则 (网络/外部文件权限)
    is_template      INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       TEXT NOT NULL
);
