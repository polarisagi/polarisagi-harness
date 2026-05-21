# 模块 5: Memory System

> 四层记忆（Working / Episodic / Semantic / Procedural），多存储引擎绑定，[Tier-0-Limit]
> Go（记忆管理器 + 检索路由 + Consolidation），Rust（Embedding 计算 via M1）
> [HE-Rule-4] [HE-Rule-5] [HE-Rule-6]
> **§跳读**: 0-bis:7 职责 / 0-ter:18 不变量速查 / 1:29 四层映射 / 2:38 L0 Working / 3:125 L1 Episodic / 4:219 L2 Semantic / 5:240 L3 Procedural / 6:250 写路径 / 7:262 HybridRetriever / 8:365 EffConn / 9:375 Consolidation / 10:393 Forgetting / 11:409 ContextAssembler / 12:438 Drift / 14:476 496(SOFT)降级 / 15:498 依赖
## 0-bis. 职责边界

- M5 **是**: 四层记忆（Working/Episodic/Semantic/Procedural）的读写管理器 | M5 **不是**: 记忆的物理存储引擎（那是 M2）
- M5 **是**: HybridRetriever 检索路由（BM25 + Dense + Graph → RRF） | M5 **不是**: Embedding 向量计算（那是 M1 Embedding API）
- M5 **是**: Consolidation 语义压缩（episodic→semantic） | M5 **不是**: Skill 技能执行（那是 M6）
- M5 **是**: ImmutableCore 用户长期偏好管理（永不裁剪） | M5 **不是**: 安全策略决策（那是 M11）
- M5 **是**: ContextAssembler 上下文组装（Slot 分离 + Taint 门控） | M5 **不是**: Agent 任务调度（那是 M4）
- M5 **是**: Forgetting 策略（效用衰减 + 冷归档） | M5 **不是**: 物理数据删除（委托 M2 Storage）

---

## 0-ter. 不变量速查表

- 编号: inv_M5_01 | 不变量: ImmutableCore 永不参与压缩——ContextWindow.Compress 跳过此区域 | 验证方式: CI `immutable_core_integrity` 测试
- 编号: inv_M5_02 | 不变量: ImmutableCore 写入必经 staging 审批——`provenance_id` CHECK 约束 | 验证方式: DDL CHECK + M11 审计
- 编号: inv_M5_03 | 不变量: embed_model_version 是一等字段——每 chunk/event 携带，跨版本检索走 OnlineReindexer | 验证方式: DDL NOT NULL 约束
- 编号: inv_M5_04 | 不变量: 默认归档不物理删除——`archived=1` 级联 chunks/entities，GDPR Art.17 例外 | 验证方式: M2 Forgetting 审计
- 编号: inv_M5_05 | 不变量: RRF 融合不裸加权——`weight/(k+rank+1)` k=60，防止不同检索器分数尺度不可比 | 验证方式: HybridRetriever 代码审计
- 编号: inv_M5_06 | 不变量: Taint 传播——chunks/events/RetrievedItem 均携带 5 级 TaintLevel，只升不降 | 验证方式: CI `taint_propagation` 测试

---

## 1. 四层记忆物理映射

- 记忆层: L0 Working | 物理存储: 进程内 theine-go cache + Immutable Core(永不裁剪) + NotesStore(SQLite 持久化) | 读写比: 1:1000 | 延迟要求: <1µs
- 记忆层: L1 Episodic | 物理存储: [Storage-SQLite] session_events + [Storage-SurrealDB-Core] embedding 列 | 读写比: 100:1 | 延迟要求: 写<100µs, 读<5ms
- 记忆层: L2 Semantic | 物理存储: Tier 0: [Storage-SQLite] 邻接表 + [Storage-SurrealDB-Core]; Tier 1+: [Storage-SurrealDB-Core] | 读写比: 1:50 | 延迟要求: <10ms
- 记忆层: L3 Procedural | 物理存储: [Storage-SurrealDB-Core] skill_id→blob + [Storage-SurrealDB-Core] 语义检索 + 文件系统 SKILL.md | 读写比: 1:500 | 延迟要求: <10µs / <5ms

---

## 2. L0 Working Memory

### 2.1 核心结构

WorkingMemory/ImmutableCore/ActiveContext/Task/Observation/MemoryFragment 类型定义见 `pkg/cognition/memory.go`。NotesStore 和 UserProfile 见同文件。

**写入权分离**: M11 Policy → ImmutableCore.SafetyConstraints; M9 PersonalizationWorker → ImmutableCore.UserPreferences + InteractionSummary; 用户显式 `/set` → UserPreferences; M5 Memory System → 仅读取，永不写入。

**ContextZone**:
```
ZoneImmutable=0      // 用户身份/安全约束/全局目标，仅用户显式 /set 可修改
ZoneMutableSkill=1   // SKILL.md 描述模板，M9 PromptOptimizer 合法优化靶点
ZoneTaintedData=2    // 外部数据，[TaintLevel] Tracked，永不进入指令区
```

**M9 → ZoneMutableSkill Taint Gate（双层）**:
1. **自动层**: PromptOptimizer 输出 → M11 `SanitizeBySchema` + `SanitizeByDeterministicTransform` → SIC 检测 → 阳性丢弃 + 审计 `prompt_opt_taint_rejected`
2. **HITL 层**: 前 5 次写入经 LLM-as-Judge 语义审核 → unsafe → [ESCALATE]。累计 5 次通过 → 概率抽查 (20%/次，安全随机源)，优先抽查"语义距离 >2σ"输出（历史 PromptOptimizer embedding 分布）。命中 + unsafe → 撤销该 task_type 全部豁免 + 语义漂移分析（余弦距离 >2σ → CRITICAL）。禁止永久豁免
3. **独立 LLM-as-Judge 二次审查**: 输出合并前必经独立 Judge（不同 Provider 模型）审查。不通过 → 丢弃 + HITL。防 SIC 对间接指令注入的 false negative

**ContextAssembler.Append**:
0. **InteractionSummary 特例**: M9 PersonaRefiner 生成的 InteractionSummary (`source='persona_refinement'` + Ed25519 签名) 写 ZoneImmutable 前执行 `SanitizeByDeterministicTransform`（保留 <200 tokens 摘要 + SHA-256 校验和），TaintLevel 强制 TaintLow。固定白名单——仅 `source='persona_refinement'` + 有效 M9 签名可写 ZoneImmutable
1. zone==ZoneImmutable 且内容 TaintLevel > TaintLow → panic 拒绝 (tainted 数据越界进不可变区)
2. zone==ZoneTaintedData 且内容 Tainted → 接受写入（正确归宿）; zone!=ZoneTaintedData 且 TaintLevel >= TaintMedium → 降级路由到 ZoneTaintedData + WARN + 审计事件 `context_assembler_taint_zone_routing`
3. zone==ZoneMutableSkill → 验证 Ed25519 ApprovalSignature（M9 签发）→ 签名无效则降级 ZoneTaintedData + WARN + 审计事件 `context_assembler_mutable_skill_integrity_failed`
4. 签名通过 → Monotonic Version Gate: 查询 `sys_config.min_skill_version`，version < min → 拒绝 + CRITICAL + 审计事件 `context_assembler_rollback_attack_blocked`
5. 写入对应 zone string builder

**ContextAssembler.Build**: 固定顺序 ZoneImmutable → ZoneMutableSkill → ZoneTaintedData。ZoneTaintedData 追加前，对 `[TaintLevel] >= TaintMedium` 的内容执行 M11 Spotlighting 包裹：
  `=== UNTRUSTED_DATA_{sha256(content)[:8]} ===\n{content}\n=== END_UNTRUSTED_DATA ===`
spotlight_hex 由内容 SHA-256 前 8 位派生（非随机）→ 同内容同标记，PromptFn 保纯函数性，M12 Eval 回放可验证。M4 上下文注入同此规则。

**SessionResume（崩溃后 ActiveContext 重建）**:
1. 从 M4 FSM Snapshot 恢复 FSM 状态 + DAG 进度
2. 从 [Storage-SQLite] `session_events` 按 AUTOINCREMENT 读取 Snapshot 后 Episodic Events
3. 加载 NotesStore 中关联活跃 Note（未过期/未删除）
4. 以 Snapshot WorkingMemorySummary 为基底，重放 Episodic Events → 重建 ActiveContext
5. ActiveContext 就绪 → Agent FSM 恢复执行
约束: <500ms 完成（Snapshot 后事件 <1000 条）。Snapshot 损坏 → 全量重建。Notes 懒加载（仅 5 条热 Note）。>1000 条事件按 200 条/批增量

### 2.2 NotesStore

工作记忆的持久化存储，为 Agent 提供跨 Session 的轻量笔记能力。实现见 `pkg/cognition/memory.go`。

**存储约束**: 单条 Note 上限 64KB，总容量硬上限 256KB，默认 TTL 为 7 天。所有写操作必须通过 MutationBus 投递 MutationIntent 至 DatabaseWriter 串行执行。

**CAS 乐观锁**: Note 通过 Version 字段实现乐观并发控制。Set 操作在 DatabaseWriter 事务内执行带版本校验的 UPDATE——CAS 失败时调用方最多重试 3 次。3 次全部失败则将冲突写入 notes_conflict_log shadow 表供人工裁决。

**容量管理**: 超出 256KB 硬上限时，按 LRU 策略淘汰最久未访问的 Note。过期的 Note 由 GC 定期清理（通过 MutationIntent OpDeleteBatch 提交）。

### 2.3 用户画像

```
**UserProfile** (`pkg/cognition/memory.go`): 优先级 SafetyRules(M11注入) > ExplicitPrefs(/set) > ImplicitPrefs(M9学习) > InteractionSummary(≤200 tokens LLM摘要)。

**Preference Learner（M9 PersonalizationWorker 触发，冷路径）**:
信号 → 偏好:
  - 显式 `/set` → 直接写 ExplicitPrefs
  - 被纠正输出 → LLM 判断风格/事实差异
  - 连续 3 次同一 Model Tier → 记录 ModelTierPref
  - 跳过解释 → "直接执行"模式
更新后 Version++，写 EventLog (`source_type='personalization'`)。

新用户冷启动时 InteractionSummary 为空，3-5 次交互后产出初步画像。

**PersonaCategory 与 PersonaDimension**: 11 维度（字段见 `memory.go`），每维度含属性/置信度/证据链，PrivacyTier 四级隐私分级，演化历史最多 50 个快照。

**PersonaRefiner（后台 worker）**: 每 Session 结束时汇总生成用户画像更新，最多 1 次/Session。auto_curriculum session 不触发。最小间隔 30 分钟。
- Token 预算: 纳入 [TokenBurnRate] 全局令牌桶，Stage1 挂起，Stage2 跳过
- DetectAndRefine（MaxLLMCallsPerCycle=5）:
  1. 信号不足（<MinSignalsForUpdate=3）或 PersonaGap.Severity<0.3 → 跳过
  2. LLM 生成≤3个更新方向（Preservation/Reflection/Advancement 三标准评分）→ 应用最佳
  3. 更新 Dimension+Version+PersonaSnapshot+InteractionSummary，写 [EventLog]，冷启动检测→ProactiveQuery

```
PersonaGap: Dimension, Predicted, Actual, Severity(0-1), Evidence

ColdStartState:
  UnknownDimensions, ProactiveQueries([]ProactiveQuery), MinInteractions(5)

ColdStartManager.ShouldAsk: UnknownDimensions 非空 → 过滤任务相关维度 → 30min 防抖 → LLM 生成询问 → 追加 ProactiveQueries

PersonaGuidedRetriever(explorationFactor=0.08): enrichQuery → 混合检索 20 候选 → rerankByPersona（编码示例×1.2/简洁文本×1.1/隐私边界过滤）→ 5-10% 探索注入防 filter bubble
```

---

## 3. L1 Episodic Memory

### 3.1 episodic_events 表

> `episodic_events` 是 M2 `events` 表（[EventLog] 真相源）的派生投影表。Agent 动作写入 M2 `events` → Outbox Worker 异步提取事件至 `episodic_events` 并填充 embedding/salience/decay_weight。

**可变字段白名单**: `episodic_events` 为派生投影表，允许受控字段变更（`archived`, `decay_weight`, `salience`, `archive_offset`），其余字段 append-only。每次受控字段变更须写入 `episodic_events_change_log` 表（再由该 log 表参与 M11 hash chain）。M2 `events` 表（真相源）仅 INSERT，绝不 UPDATE。M11 hash chain 覆盖 `events` 表全字段 + `episodic_events_change_log` 表。

**`[ReasoningState]` 列**（M4 §7.1 跨轮持久化）: `episodic_events.reasoning_state BLOB(nullable)` — Provider 返回的推理状态 blob，msgpack + AES-256-GCM 加密（key 由 [CredentialVault].persistent_key 派生）。同 task_id 30min 窗口内 M4 可读取最近一条注入下次 LLM 调用。SessionPIIVault.SecureZero 同步清零本字段。Tier 0 默认不写（关闭 `FeatureReasoningStateCarry`）。

DDL 和持续性记忆组映射表见 `internal/protocol/schema/003_episodic_memory.sql`。

Salience: LLM 输出 + 工具结果 → 低; 用户反馈 + 关键决策 + 失败/成功 → 高。基于 M9 SurpriseIndex 信号的边权重强化与时间衰减实现动态权重调整（见 §7.6）。

### 3.2 Session Compaction

Session 关闭时:
1. LLM 生成 3-5 句会话摘要（高 salience 合成事件）
2. 原始事件保留，不删除
3. 合成事件写入，标记 source='compaction'
4. 后续检索优先返回合成事件

### 3.3 Durative Memory

```
DurativeMemoryManager（后台 worker）:
  store        EpisodicStore
  llmJudge     轻量 LLM 判定语义连续性
  minGroupSize 3
  checkWindow  30d

DurativeGroup:
  ID(ULID), Label, Summary(~100 tokens), StartTime, EndTime,
  EventIDs[], TopicVector(embedding 质心), Status(active/closed/archived),
  TaintLevel  // [Taint-Prop]: max(成员事件 TaintLevel), floor=[Taint-Medium]
```

Consolidate（每小时 cron）:
1. 扫描 30 天内无 durative_group_id 的孤立事件
2. 按语义相似度 + 时间邻近度聚类
3. LLM 判定每个候选簇是否语义连续体
4. 创建 DurativeGroup → Append `memory_group_mapping_created` 事件（event_id → group_id）。禁止原位 UPDATE episodic_events——[EventLog] 受 M11 AuditTrail 哈希链保护
5. 关闭 >7 天无新事件的 active group

读时: LEFT JOIN memory_group_mapping 合成 durative_group_id

`memory_group_mapping` 表 DDL 见 `internal/protocol/schema/003_episodic_memory.sql`。

RetrieveWithDurative:
1. 检测时间意图关键词（"上周"/"三周前"/"当时"）
2. 命中 → 优先在 durative group 摘要层搜索，返回组摘要 + top-3 关键事件
3. 未命中 → 走常规检索

### 3.4 `[ReflectionMemory]` — 元认知反思层

> 对齐 Generative Agents (Park 2023+) 与 MemGPT 反思层共识。Episodic（"发生了什么"）与 Semantic（"普遍事实是什么"）之间的中间层——Agent 自身对**"我做了什么 + 学到什么"**的元认知摘要。

**区别**:

| 层 | 视角 | 内容 | 触发 |
|----|------|------|------|
| Episodic | 事件流水 | "用户问 X, Agent 调 tool_Y, 返回 Z" | Agent 动作即时 |
| **Reflection** | **元认知** | **"在该类任务上 tool_Y 比 tool_W 快 3 倍 + 失败模式 X 来自参数 P 越界"** | **任务终态 + Session 关闭 + 失败 reflection** |
| Semantic | 普遍事实 | "事实图谱: X is_a Y" | Consolidation 合并 |

**与 M9 PersonaRefiner 区别**: PersonaRefiner 更新**用户画像**；ReflectionMemory 更新 **Agent 自身经验**。

**表结构** (`reflection_memory`，DDL 见 `internal/protocol/schema/003_episodic_memory.sql`):
- ID (ULID), TaskID (关联触发任务), TaskType
- ReflectionType ∈ {success_pattern, failure_mode, efficiency_insight, cross_task_principle}
- Content (≤500 tokens, [TaintLevel]=TaintLow——LLM 系统自生成 source='reflection'，[Taint-Floor-Medium] 豁免名单)
- EvidenceEventIDs[] — 支持该反思的 Episodic 事件
- Embedding (复用 M1 Embedder), Salience (0-1)
- CreatedAt, AccessedCount, LastAccessedAt

**触发** (后台 worker `ReflectionWorker`):
1. **任务终态触发**: S_COMPLETE / S_FAILED 进入时，若 task_type 在白名单（复杂任务，非简单查询）→ 投递 reflection job
2. **Session 关闭批量触发**: 单 session 内 ≥3 个完成任务 → 跨任务模式提取
3. **失败深度反思**: ReplanCount ≥ 2 触发失败模式 LLM 提取（"为什么这条路径反复失败"）

**写入流程**:
1. 收集 Evidence Episodic Events（同 task_id 或 task_type）
2. LLM 提取（Budget Pool）→ 严格 JSON schema 输出
3. M11 [FactualityGuard] 抽样核验（引用 Evidence 必须真实包含主张）
4. 经 MutationBus 写入 `reflection_memory` 表

**读取** (M5 HybridRetriever 第 4 路召回，权重 0.15):
- task 启动时 M4 S_PERCEIVE 拉取相同 task_type 的 top-3 reflection 注入 ZoneImmutable（标记 source='reflection'）
- 与 [HeuristicsMemory] (M9 §2.1) 互补——后者是 task_type→prompt 模板，前者是 task_type→经验摘要

**HT0 限制**: 表大小硬上限 5MB（约 5000 条 reflection），LRU 淘汰最久未访问。LLM 提取仅在 idle 期间执行（M9 BackgroundTaskScheduler [Priority-2]）。

---

## 4. L2 Semantic Memory

### 4.1 表结构（[Storage-SQLite] 邻接表）

DDL 见 `internal/protocol/schema/004_semantic_memory.sql`。Tier 0 使用 SQLite 邻接表；Tier 1+ 升级 [Storage-SurrealDB-Core]。

### 4.2 UpsertFact

1. searchSimilar(阈值 0.95) → 同一事实，UPDATE 属性 + version++
2. 相似度 > 0.80 → LLM 冲突解决（判断更新 vs 新事实）
3. 相似度 < 0.80 → INSERT 新事实
4. version++ 不可变版本 + source_event_id provenance + 信念修正（矛盾时优先保留更近期/更高证据强度事实）+ Prospective Indexing（写入时预生成未来查询并索引）

### 4.3 QueryClassifier + RetrievalRouter

查询首先经过两级分类器——第一阶段基于中文关键词规则匹配（<10µs）: 时间关键词（"上周"/"三周前"）→ temporal_reasoning，操作关键词（"怎么做"/"如何"）→ how_to，事实关键词（"是什么"/"谁的"）→ factual_lookup，推理关键词（"为什么"/"分析"）→ complex_reasoning。规则未命中时进入第二阶段——查询 embedding 与 4 个查询类型原型向量做余弦相似度比较，置信度 <0.3 时回退 default（全搜）。

根据分类结果，RetrievalRouter 分派到不同的记忆层: factual_lookup → Semantic Layer，temporal_reasoning → Episodic Layer（含 DurativeMemory 持续性记忆），how_to → Procedural Layer，complex_reasoning → Semantic + Episodic 并行检索。default 类型全搜，通过 RRF 融合自然淘汰低相关结果。

---

## 5. L3 Procedural Memory

程序记忆（技能库）采用三层存储架构: SurrealKV KV 作为热路径的签名级精确查找（skill_id → Wasm 二进制，延迟 <10µs），SurrealDB-Core 作为语义搜索路径（用于 System 2 的 embedding-based 相似技能检索），文件系统 SKILL.md 作为 Ground Truth（技能源码和契约的权威定义，受 Git 版本控制）。

双轨检索: System 1 路径通过 IntentSignature 在 SurrealKV 中做 O(1) 精确匹配（亚毫秒级），命中后由 M6 WasmSkillCache 缓存编译产物直接执行。System 2 路径在 SurrealDB-Core 中做 KNN 语义搜索，返回候选技能后按成功率排序，渐进披露注入 LLM prompt。

L3 Procedural 技能索引相关 DDL 实质托管于 M2 SurrealKV KV 引擎（`pkg/substrate/storage/SurrealKV.go`），SKILL.md 元数据从文件系统懒加载。M5 skillKV 与 M6 WasmSkillCache 的关系见 M6 §5.1。

---

## 6. Write Path: Hot/Cold 分离

Agent 动作完成后，写入路径拆分为两条线:

**热路径（同步，<10ms）**: 纯文本事件日志写入 EventLog（SQLite WAL，约 100µs），同步更新 Working Memory 缓存，触发 TokenBurnRate 计数。不调用 LLM、不操作图、不等 embedding API——保证 Agent 回复不受后台处理延迟影响。

**冷路径（M2 Outbox Worker 异步）**: Outbox Worker 消费事件日志后异步执行——调用 M1 Embedding API 生成向量、写入 SurrealDB-Core 索引、提取实体和关系 Upsert Semantic Memory、检查 Consolidation 阈值并触发压缩、更新 Skill 成功率统计。

热路径仅做纯文本日志写入，不调用 LLM、不操作图、不等 embedding。冷路径在后台静默运行。

---

## 7. Read Path: HybridRetriever

```
HybridRetriever (共享接口见 `pkg/substrate/hybrid_retrieve.go`):
  bm25      BM25Index
  denseVec  VectorIndex
  graphDB   GraphTraverser
  embedder  查询向量化
  reranker  BM25Reranker (Tier 0: SQLite FTS5 BM25; Tier 1+: SurrealDB-Core BM25, 与 M10 同源)
  config    RetrievalConfig
```

### 7.1 BM25Index

- 存储: Tier 0 [Storage-SQLite] FTS5; Tier 1+ [Storage-SurrealDB-Core] (k1=1.2, b=0.75)
- Tokenizer: Tier 0 → SQLite FTS5 + jieba 中文分词扩展（Go 纯实现）；Tier 1+ → SurrealDB-Core + jieba-rs / lindera 多语言分词器。M3 启动期检测系统 locale，自动选择默认分词器
- Search: 分词 → FTS5 MATCH → BM25 分数降序 topK → 批量加载完整事件
- 冷路径异步 Index(event): Unicode 标准化 + 词干提取 + 停用词过滤 → 写入倒排索引

### 7.2 VectorIndex + Simhash

- 存储: [Storage-SurrealDB-Core] vec0(float32[4096])，k-means 分区 (k=sqrt(N), 每 10K 向量重分区)
- Search: embedder.Embed(query) → KNN 余弦距离 → topK
- **Simhash 备选**: 64-bit Simhash 指纹（纯 Go, <10µs/text），embedding API 不可用时降级为 Simhash 扫描（汉明距离 ≤8）→ BM25 + Simhash + GraphTraverse 三路融合替代 DenseVec 路径。非替代，为廉价近似

### 7.3 GraphTraverser + Spreading Activation

Traverse（BFS）:
1. 种子实体: semantic_entities 中与 query embedding 最相似 top-5
2. 有界 BFS (depth=2)，entity_type 加权: Person ×2.0, Project ×1.5, Tool ×1.0
3. 去重，按路径权重之和降序
4. 限制: maxNeighborsPerHop=20, maxTotalNodes=200

Spreading Activation（关联发现模式）:
1. top-3 种子实体，activation_energy=1.0
2. 扩散: energy × edge.weight 传播至邻居，自身 ×0.5 衰减。≤0.05 停止
3. 最多 5 轮
4. 按最终 energy 降序 topK
5. 模式选择: query 含"为什么"/"原因"/"关联"/"影响" → Spreading Activation; 否则 BFS

### 7.4 RRF 融合 + BM25 精排

```
Stage 0 — 隐私门控:
  ctx.max_privacy_tier_allowed (M11 Policy Gate 注入)
  → 传递至 Stage 1 所有子检索器 WHERE 硬约束 (fail-closed: 默认 PrivacySession)

Stage 1 — 并行宽召回 (errgroup goroutine):
  (a) bm25.Search(query, OversampleN × FinalTopK)    — 关键词召回
  (b) denseVec.Search(queryEmb, OversampleN × FinalTopK) — 语义召回
  (c) graphDB.Traverse(queryEmb, depth=2)             — 图遍历召回

Stage 2 — RRF 融合:
  weight / (k + rank + 1), k=60, 三路累加后降序

Stage 3 — 重排:
  1. BM25 精排: 取融合 topM, reranker.Rerank (Tier 0: SQLite FTS5; Tier 1+: SurrealDB-Core)
  2. Cross-encoder 神经重排: M1 LocalProvider.Rerank(query, documents) → llama.cpp GGUF bge-reranker (~50MB, <50ms)
     - 本地模型已加载: 交叉编码打分 → BM25 ×0.3 + CrossEncoder ×0.7
     - 本地模型未加载: 纯 BM25 精排
     - 远程模式: 可选 Cohere/Jina Rerank API（同接口，不同 Provider）

Stage 4 — 截断: FinalTopK=10 (M5) | 5 (M10)
  默认过滤: `WHERE session_type != 'auto_curriculum' OR explicit_include=true`（防止 Auto-Curriculum 失败轨迹污染 Agent 推理上下文）
  仅 M9 自身评估显式 explicit_include=true 才能召回 auto_curriculum session
```
M5/M10 共享 `pkg/substrate/hybrid_retrieve.go` 底层引擎。检索范围不同 (M5: episodic+semantic, M10: doc_nodes)，参数差异见 M10 §2.2 配置对照表。

### 7.5 Evidence Subgraph Extraction

```
EvidenceSubgraphExtractor:
  graphDB, maxDepth(3), maxNodes(50), alpha(0.85)
Extract:
  1. bounded BFS: 种子实体, 每层 ≤10 邻居, maxDepth 层, 累计 ≤ maxNodes×2
  2. Personalized PageRank: 随机游走计算节点分数
  3. Top-K 截断 + 边收集
ToPromptText: "Knowledge Graph Context:" 段 — 节点 [type] name: properties + 边 from --[relation]--> to
```

### 7.6 Edge Weight Reinforcement & Decay

> 七筛第 1 条剥离神经科学比喻命名（LTP/LTD/突触可塑性），保留可计算机制本身：使用驱动的边权重 + 时间衰减。

```
EdgeWeightManager:
  graphDB, reinforceRate(0.05), decayRate(0.8), pruneThreshold(0.1), decayWindowDays(30)

ReinforcePath: traversedEdge.weight += reinforceRate (上限 1.0), 更新 last_accessed_at

FeedbackCalibrate（反馈校准）:
  1. successTrajectory 节点出边: 被使用的强化 weight + applicability (+0.03)
  2. 未使用但同情境候选边降低 applicability (-0.02)
  3. 更新 last_accessed_at

DecayUnused (读时衰减, 防 WAL 写放大):
  effective_weight = weight × decayRate^(days_since_last_access / decayWindowDays)
  公式在 BFS/Spreading Activation 读边时 O(1) 计算, weight 原始值不变
  物理修剪 (< pruneThreshold): 每日凌晨 3:00 cron DELETE-only, 不执行批量 UPDATE
```

---

## 8. Effective Connectivity（冷后台预计算）

`semantic_connectivity_cache` 表 DDL 见 `internal/protocol/schema/004_semantic_memory.sql`（派生数据缓存，非事实源）。

Effective Connectivity 被预计算为可 O(1) 查询的缓存表。ConnectivityPrecomputer 由 M9 BackgroundTaskScheduler 在每日凌晨 4:30 触发（与 Consolidation 3:00 错开 ≥90 分钟）。Tier 0 最多计算 200 个种子实体（约 20MB 内存），Tier 1+ 扩展到 1000 个。分批 50 实体/批，批间释放 CPU（Gosched），CPU 占用 >30% 或空闲内存 <2GB 时挂起。采用 INSERT OR REPLACE 全量覆盖旧缓存。

ActivationMaximization 查询时 O(1) 完成——任务 embedding 搜索最相似的 20 个实体 → 按预计算的 effective_weight 排序 → 取 topK + BFS+PPR 构建最小激活子图。

---

## 9. Consolidation

**触发条件**:
- 主题转换检测到 shift → 立即触发
- eventCount ≥ 50 → 触发，计数归零
- sessionClosed → 强制触发

**4-Stage Pipeline**:

- Stage: 1 LLM 提取 | 操作: 实体/关系/事实提取 + 矛盾检测 | 输出: 结构化事实列表
- Stage: 2 Upsert Semantic | 操作: same → UPDATE version++; conflict → mark superseded; new → INSERT | 输出: [Storage-SQLite] 邻接表
- Stage: 3 Session Summary | 操作: LLM 生成 3-5 句摘要, source='compaction' | 输出: [Storage-SurrealDB-Core] 高 salience 合成事件
- Stage: 4 Update Procedural | 操作: 成功执行的任务 → Logic Collapse → Skill Library | 输出: [Storage-SurrealDB-Core]

关键约束: version++ 不可变版本 + source_event_id provenance + 信念修正 + Prospective Indexing

---

## 10. Forgetting: 双层策略

### 10.1 热删除：效用衰减

记忆的效用随时间按指数衰减。衰减公式: `salience × exp(-decayRate × ageHours/24)`，其中 decayRate 为 0.01/日。衰减权重低于阈值时标记 Forgettable=true。Q-Learning 熵门控动态调整阈值——高熵任务（不确定性大）降低阈值保留更多历史，低熵任务提高阈值加速遗忘。

Forgettable 事件不物理删除，而是进入下一阶段的冷归档。

### 10.2 冷归档

标记为 Forgettable 且年龄超过 30 天的事件，批量序列化为 Parquet 文件（按 session_id + 月份分区，存储于 `~/.polaris-harness/archive/`）。episodic_events 派生投影表标记 archived=1 + archive_offset，写入 episodic_events_change_log（参与 M11 hash chain）。M2 events 真相源不参与归档标记，保持不可变。

SQLite 文件 > 500MB 或已归档记录 > 100K 时触发物理压缩——先校验 Parquet SHA-256 完整性，确认无误后 SQLite 物理 DELETE（仅删除派生表行，真相源不动），最后执行 VACUUM 回收磁盘空间。归档 Parquet 无限保留，通过 DuckDB 按需回查。

---

## 11. ContextAssembler

BuildContext 5 Zone 布局和 SessionCompressor 实现见 `pkg/cognition/context_assembler.go`。

**Prompt 组装顺序与 KV Cache 优化规范**:
为了最大化利用支持 Prompt Caching (如 Anthropic/DeepSeek) 模型的 KV Cache 命中率，Prompt 的内部块组装必须遵循严格的静态从长到短顺序，保证最久不变的块位于最前部：
`ImmutableCore → Procedural (Skills) → Semantic (Knowledge) → Episodic (Recent Events) → Working (Scratchpad) / TaintedData`
1. **ImmutableCore**: 系统级常量，永不改变，置于首位。
2. **Procedural**: 当前 Agent 挂载的工具和技能声明，在特定任务会话内保持稳定。
3. **Semantic & Episodic**: 随会话推进按步增量追加，更新频次中等。
4. **Working / TaintedData**: 高频读写的临时草稿和不可信外部输入，置于最后，不参与 Cache。
*注：M1 Provider Adapter 会自动探测该顺序，向稳定块的末尾段落注入 `cache_control: {"type":"ephemeral"}` 以激活缓存（详见 M1 §3）。*

Layout Zone → ContextZone 映射表、安全约束和不变量见上文 §2.1。

### SessionCompressor

**SessionCompressor** 实现见 `pkg/cognition/context_assembler.go:SessionCompressor`。

> 与 M4 ContextWindowManager 协同：M4 §7 持有热路径阈值（>70% salience 排序候选 / >90% 语义结构感知逐出），M5 SessionCompressor 在 M4 调用时执行实际的冷压缩算法（本节定义）。

压缩 Stage（由 M4 ContextWindowManager 调用，不独立设阈值）:
- **Stage 1**: 清除可重取的超过 10KB 的工具输出——这些数据可从 git 或文件系统重新获取
- **Stage 2**: 仍超阈值时执行锚定迭代总结。LLM 以 currentSummary 为锚点追加新事件，产生增量摘要

锚定策略: 架构决策、失败原因、修复方案、用户风格偏好永久保留；当前进度和待办事项允许更新；具体工具输出允许丢弃。

---

## 12. Embedding Drift 对策

### 12.1 维度切换无缝过渡

当 M1 Embedder 维度发生变化（或切换为 local_only）时：
- 检索路由: M5 检测当前 Provider 维度，动态将查询路由至 SurrealDB-Core 对应维度的独立隔离表（如 index_remote_4096 或 index_local_384）。
- 避免降级: 维持 DenseVec 默认权重，禁止因为维度不兼容而退化为纯 BM25。
- M6 同步: 同理，M6 通过访问独立维度表维系 L1 vecIndex 权重，避免回退到 FTS5/BM25 替代方案。

### 12.2 Blue-Green Index Swap

Tier 0 前提条件: 空闲内存 > 2 × 当前向量索引占用 (通过 M3 sysinfo.FreeMemory() 校验)。不满足时退化为原地增量更新。

```
1. 后台构建: SurrealDB-Core 创建新版本索引 (index_v{N+1})，查询仍用 index_v{N}
2. 质量验证: 锚定样本(100 条) Recall@5 ≤ 旧索引 90% → ABORT + WARN
3. 原子切换: SurrealDB-Core 索引版本指针原子更新 → 查询路由至新索引 (<1ms)
4. 旧索引: 异步回收 index_v{N}
5. FTS5/BM25 始终在线
```

### 12.3 DriftDetector

```
DriftDetector:
  anchors(100 AnchorSample), checkInterval(7d), driftThreshold(0.05)
Detect:
  1. sampleCount<5 任务类型跳过 (标记 unknownCount++)
  2. 重新检索 → 计算 Top-5 变化率 + 余弦距离变化
  3. changeRate>0.4 且 cosineDelta>driftThreshold → 记录漂移
  4. unknownRatio>0.30 → 系统级告警
EmbeddingVersionTracker:
  每索引维护 P50/P95/P99/Min/Max 滚动统计(EWMA alpha=0.01)
  跨版本检索: min-max 归一化 → RRF 融合
```

---

## 14. 降级与失败模式

- 故障场景: HybridRetriever 单路检索失败 | 降级路径: 其余路权重接管（DenseVec 失败 → BM25×0.7 + Graph×0.3） | 恢复策略: 故障路恢复后自动切回默认权重
- 故障场景: Embedding API 不可用 | 降级路径: Simhash 64-bit 指纹备选（汉明距离 ≤8）+ BM25 + 图遍历三路融合 | 恢复策略: API 恢复后 DenseVec 权重切回
- 故障场景: SurrealDB-Core 维度切换 | 降级路径: 动态路由查询至对应维度表（如 index_local_384），无需强制降级 BM25 | 恢复策略: 后台静默回填增量
- 故障场景: Consolidation LLM 调用超时 | 降级路径: 跳过本轮 Consolidation，事件保留在 episodic_events 等待下一轮 | 恢复策略: 下个 cron 周期自动重试
- 故障场景: NotesStore CAS 乐观锁冲突超限（>3 次） | 降级路径: 写入 notes_conflict_log shadow 表供人工裁决 | 恢复策略: —
- 故障场景: Episodic 冷路径 Outbox 积压 | 降级路径: 暂停非关键 Consolidation + WARN | 恢复策略: 积压降至 <200 恢复正常
- 故障场景: Mem-L3 SurrealKV 引擎故障 | 降级路径: 降级 SQLite 备份索引 (skill_id→metadata，不含 blob) | 恢复策略: SurrealKV 恢复后切回
- 故障场景: DurativeMemory 聚类 LLM 超时 | 降级路径: 按纯向量余弦相似度聚类（跳过 LLM 语义判定） | 恢复策略: LLM 恢复后追加语义连续性标注
- 故障场景: ContextAssembler 组装超时 (>500ms) | 降级路径: 跳过 ZoneMutableSkill 组装，仅发 ZoneImmutable + 摘要 | 恢复策略: 下次 context refresh 完整组装
- 故障场景: EdgeWeightManager 图边修剪 LLM 超时 | 降级路径: 仅物理 DELETE < pruneThreshold 的边，跳过 FeedbackCalibrate | 恢复策略: 下个 cron 周期重新校准
- 故障场景: Embedding DriftDetector 检测到漂移 | 降级路径: 该 task_type 降级纯 BM25，其余不受影响 | 恢复策略: Blue-Green 重嵌完成后切回

与 OSMemoryGuard 协同: L1 预警 → 暂停 Consolidation 冷路径 / L2 紧急 → 暂停 Episodic 冷路径 Outbox 处理、限制 WorkingMemory 容量 / L3 临界 → 全部冷路径暂停，仅热路径写入 + L0 WorkingMemory 读取可用。

---

## 默认参数

完整阈值与重评触发条件: `spec/state.yaml §thresholds.m5_memory`。最终值落 `config/m05.toml`。

## 15. 跨模块依赖与契约

- 关联模块: M1 Inference | 关键契约: Embedding API（向量生成）、LLM 调用（摘要/Consolidation） | 位置: M1 §6.1, §5
- 关联模块: M2 Storage | 关键契约: Store 接口、EventLog 真相源（events 表 → episodic_events 派生投影） | 位置: M2 §1.1, §2.1
- 关联模块: M4 Agent Kernel | 关键契约: ContextAssembler、HybridRetriever 上下文检索 | 位置: M4 §2, §10
- 关联模块: M6 Skill Library | 关键契约: L3 Procedural Memory 技能索引 + SurrealKV 缓存 | 位置: M6 §7
- 关联模块: M9 Self-Improve | 关键契约: M9→ZoneMutableSkill Taint Gate（双层）、Preference Learner、PersonaRefiner | 位置: M9 §1.1, M5 §2.1
- 关联模块: M10 Knowledge RAG | 关键契约: HybridRetriever 共享引擎（pkg/substrate/hybrid_retrieve.go）、检索配置差异 | 位置: M10 §2.2
- 关联模块: M11 Policy Safety | 关键契约: SafetyRules 注入 ImmutableCore、TaintGate 写入 Zone 校验 | 位置: M11 §2, M5 §2.1
- 关联模块: 全局字典 | 关键契约: HE-Rule-4 数据驱动迭代、HybridRetriever/RRF 定义 | 位置: 00-Global-Dictionary §2, §9-bis
- 关联模块: DDL | 关键契约: 001_events（真相源）、003_episodic_memory（派生投影）、004_semantic_memory（语义层） | 位置: internal/protocol/schema/001-004_*.sql
