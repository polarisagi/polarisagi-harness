-- +goose Up
-- 谬误记录 (MEMF) 表
CREATE TABLE IF NOT EXISTS fallacy_records (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    failure_type TEXT NOT NULL,
    -- 由于 Tier 0 不使用向量检索，我们存储关键词的 JSON 数组或者合并文本用于 LIKE 匹配
    keywords_json TEXT NOT NULL DEFAULT '[]',
    reflection TEXT NOT NULL,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    node_quality_score REAL NOT NULL DEFAULT 1.0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_fallacy_task_type ON fallacy_records(task_type);
CREATE INDEX IF NOT EXISTS idx_fallacy_score ON fallacy_records(node_quality_score);

-- 启发式成功规则表 (Heuristics Memory)
CREATE TABLE IF NOT EXISTS heuristics_memory (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    content TEXT NOT NULL,
    success_rate REAL NOT NULL DEFAULT 1.0,
    use_count INTEGER NOT NULL DEFAULT 0,
    keywords_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_heuristics_task_type ON heuristics_memory(task_type);

-- Prompt 演化版本表
CREATE TABLE IF NOT EXISTS prompt_versions (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL,
    task_type TEXT NOT NULL,
    prompt_text TEXT NOT NULL,
    score REAL NOT NULL DEFAULT 0.0,
    cost REAL NOT NULL DEFAULT 0.0,
    source TEXT NOT NULL,
    parent_version INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_prompt_versions_active ON prompt_versions(task_type, is_active);

-- Staging 演化状态表
CREATE TABLE IF NOT EXISTS rollout_states (
    candidate_version TEXT PRIMARY KEY,
    baseline_version TEXT NOT NULL,
    current_gate INTEGER NOT NULL DEFAULT 0,
    canary_percent INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    last_advanced_at INTEGER NOT NULL
);

-- 工具描述符变体池 (Skill Variant Pool)
CREATE TABLE IF NOT EXISTS skill_variant_pool (
    id TEXT PRIMARY KEY,               -- UUIDv7
    skill_id TEXT NOT NULL,            -- 关联 sys_skills.id
    generation INTEGER NOT NULL,       -- 代数，0 = 原始描述，>0 = 突变/交叉生成
    fitness_score REAL DEFAULT 0.0,    -- 适应度得分（越高越优，由 EvolverDaemon 低负载期批量更新）
    desc_variant TEXT NOT NULL,        -- 候选描述符文本
    mutation_source TEXT CHECK(mutation_source IN ('mutate', 'crossover', 'seed')),              -- 生成来源: 'mutate' | 'crossover' | 'seed'
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_variant_skill ON skill_variant_pool(skill_id, generation);
CREATE INDEX IF NOT EXISTS idx_variant_fitness ON skill_variant_pool(skill_id, fitness_score);

-- +goose Down
DROP TABLE IF EXISTS skill_variant_pool;
DROP TABLE IF EXISTS rollout_states;
DROP TABLE IF EXISTS prompt_versions;
DROP TABLE IF EXISTS heuristics_memory;
DROP TABLE IF EXISTS fallacy_records;
