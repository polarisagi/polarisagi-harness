-- ============================================================================
-- 003_episodic_memory: 情景记忆层 —— events 的派生投影表
-- ============================================================================
-- 架构角色: 为 M5 HybridRetriever 提供优化的记忆检索入口。events 表（真相源）
--           为不可变日志，本表为其派生投影——增加 embedding/salience/decay_weight
--           等检索优化字段，独立索引，时序分区。两者通过 idempotency_key 关联。
-- 生产者:    M2 OutboxWorker（事件写入 events → Outbox 异步投影至本表）
-- 消费者:    M5 HybridRetriever（BM25 + Dense Vector + 图遍历三路检索）、
--           M4 ContextAssembler（上下文组装时检索近期情景记忆）、
--           M9 MEMF（失败轨迹检索相似度过滤）
-- 可变字段白名单: archived、decay_weight、salience、archive_offset（仅此 4 字段允许 UPDATE）。
--               每次受控字段变更须写入 episodic_events_change_log 表（参与 M11 hash chain）。
--               其余字段 append-only。
-- 不变量:
--   1. UNIQUE(session_id, seq) 保证单 session 内事件序号唯一
--   2. idempotency_key 与 events 表关联，保证投影一致性
--   3. 本表允许有限 UPDATE（仅白名单字段），events 真相源绝不 UPDATE
-- 写入路径: M2 OutboxWorker 异步投影（消费侧）。OUTBOX 模式不直接走 MutationBus。
-- 关联模块: M5(Memory) §3.1, M2(Storage) §2.1, M4(Agent) §9, M9(Self-Improve) §2.1
-- ============================================================================

CREATE TABLE IF NOT EXISTS episodic_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    -- ↑ 本表自增主键。不承担跨表关联职责——跨表引用使用 idempotency_key。

    session_id    TEXT NOT NULL,
    -- ↑ 所属会话标识。与 M4 Agent Session ID 对应。

    seq           INTEGER NOT NULL,
    -- ↑ 会话内事件序号，从 1 递增。与 session_id 联合唯一，保证会话内事件定序。

    timestamp     INTEGER NOT NULL,
    -- ↑ 事件物理发生时间（Unix 毫秒）。

    event_type    TEXT NOT NULL,
    -- ↑ 事件类型。state_transition | tool_call | observation | reflection | system。

    source        TEXT NOT NULL,
    -- ↑ 事件来源。'agent'（Agent 执行产生）| 'compaction'（Session 压缩合成）|
    --   'consolidation'（Consolidation 提取）| 'persona_refinement'（PersonaRefiner 生成）。
    --   source='compaction' 的合成事件在检索时优先返回（高信息密度摘要）。

    content       TEXT NOT NULL,
    -- ↑ 事件内容（纯文本）。人类可读的事件描述。嵌入计算基于此字段。

    embedding     BLOB,
    -- ↑ 事件向量表示（float16 量化，4096 维）。M2 OutboxWorker 异步填充。
    --   用于 M5 HybridRetriever Dense Vector 语义检索路径。

    salience      REAL DEFAULT 0.5,
    -- ↑ 事件显著性 0.0-1.0。初始值 0.5 表示未评估。
    --   LLM 输出 + 工具结果 → 低显著性；用户反馈 + 关键决策 + 失败/成功 → 高显著性。
    --   初始规则设定，后续 M9 RL 学习调整。允许 UPDATE（白名单字段）。

    decay_weight  REAL DEFAULT 1.0,
    -- ↑ 时效衰减权重。1.0 = 全新，随年龄衰减。M5 ForgettingManager 每日更新。
    --   公式: salience × exp(-decayRate × ageHours/24)。允许 UPDATE。

    occurred_at   INTEGER,
    -- ↑ 事件物理发生时间，与 M2 events.occurred_at 对应。

    UNIQUE(session_id, seq)
    -- ↑ 会话内序号唯一 —— 保证幂等插入，M2 OutboxWorker 投影时防重复。
);

-- 按会话 + 时间检索（M5 HybridRetriever 最常见的访问模式）
CREATE INDEX IF NOT EXISTS idx_ep_time
    ON episodic_events(session_id, timestamp);

-- 按物理发生时间检索（M5 DurativeMemory 时间邻近度聚类）
CREATE INDEX IF NOT EXISTS idx_ep_occurred
    ON episodic_events(occurred_at, session_id) WHERE occurred_at IS NOT NULL;

-- ----------------------------------------------------------------------------
-- memory_group_mapping: 持续性记忆组映射
-- ----------------------------------------------------------------------------
-- 架构角色: 将多个 episodic_events 行关联到同一个 DurativeGroup（持续性记忆组）。
--           读时 LEFT JOIN 合成 durative_group_id，避免原位 UPDATE episodic_events。
-- 生产者:    M5 DurativeMemoryManager（每小时 cron 聚类后写入）
-- 消费者:    M5 RetrieveWithDurative（检测时间意图关键词时优先在组摘要层搜索）
-- ============================================================================

CREATE TABLE IF NOT EXISTS memory_group_mapping (
    event_id  INTEGER PRIMARY KEY,
    -- ↑ 对应 episodic_events.id。

    group_id  TEXT NOT NULL,
    -- ↑ DurativeGroup.ID (ULID 格式)。

    mapped_at INTEGER NOT NULL
    -- ↑ 映射创建时间（Unix 毫秒）。
);

CREATE INDEX IF NOT EXISTS idx_mgm_group
    ON memory_group_mapping(group_id);
