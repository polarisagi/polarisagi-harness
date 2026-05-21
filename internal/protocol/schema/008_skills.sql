-- M6 Skill Library DDL
-- 记录 Agent 的技能元数据，支持 M4 调度执行与 M6 演化

CREATE TABLE IF NOT EXISTS skills (
    name            TEXT PRIMARY KEY,
    version         TEXT NOT NULL,
    runtime         TEXT NOT NULL DEFAULT 'wasm',
    risk_level      TEXT NOT NULL DEFAULT 'high',
    sandbox         INTEGER NOT NULL DEFAULT 1,
    capabilities    TEXT NOT NULL,
    signature_valid BOOLEAN NOT NULL DEFAULT 0,
    idempotent      BOOLEAN NOT NULL DEFAULT 0,
    benchmarks      TEXT NOT NULL DEFAULT '{}',
    deprecated      BOOLEAN NOT NULL DEFAULT 0,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_skills_deprecated ON skills(deprecated);
CREATE INDEX IF NOT EXISTS idx_skills_risk_level ON skills(risk_level);
