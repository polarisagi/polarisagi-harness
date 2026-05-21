-- ============================================================================
-- 017_cron_jobs: 定时任务（Cron Job）持久化
-- 格式说明：
--   schedule 支持 @hourly | @daily | @weekly | @every <N>m | @every <N>h
-- ============================================================================
CREATE TABLE IF NOT EXISTS cron_jobs (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    prompt      TEXT NOT NULL,
    schedule    TEXT NOT NULL,               -- @daily | @every 30m 等
    session_id  TEXT,                        -- 关联会话（NULL=每次新建）
    enabled     INTEGER NOT NULL DEFAULT 1,  -- 0=禁用
    last_run_at TEXT,                        -- ISO8601，NULL=从未执行
    next_run_at TEXT NOT NULL,               -- ISO8601，调度器维护
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_cron_next ON cron_jobs(enabled, next_run_at);
