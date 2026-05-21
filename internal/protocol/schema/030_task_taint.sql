-- ============================================================================
-- 030_task_taint: 为 tasks 表补齐 TaintLevel 污点字段
-- ============================================================================
-- 背景: TaskEntry 跨 Agent Blackboard 传递时需随 Intent/Result 携带 TaintLevel，
--       防止 Prompt Injection 在 Agent 边界处"洗白"（inv_M8_05）。
-- 注意: TaintLevel 使用 0=TaintNone … 4=TaintHigh，对应 internal/protocol/types.go TaintLevel 枚举。
-- ============================================================================

ALTER TABLE tasks ADD COLUMN intent_taint INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN result_taint INTEGER NOT NULL DEFAULT 0;
