-- ============================================================================
-- 007_tasks: Agent 任务状态核心列
-- ============================================================================
-- 架构角色: 记录多 Agent 任务的完整生命周期状态。与 events 表和 Blackboard 联动。
-- ============================================================================

CREATE TABLE IF NOT EXISTS tasks (
    task_id                  TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL,
    status                   TEXT NOT NULL,
    priority                 INTEGER NOT NULL DEFAULT 1,
    claimed_by               TEXT,
    claimed_at               TEXT,
    expires_at               TEXT,
    version                  INTEGER NOT NULL DEFAULT 0,
    replan_count             INTEGER NOT NULL DEFAULT 0,
    toxicity                 INTEGER NOT NULL DEFAULT 0,
    depends_on               TEXT,
    suspend_reason           TEXT,
    pii_vault_blob           TEXT,
    provider_suspended_count INTEGER NOT NULL DEFAULT 0,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);
