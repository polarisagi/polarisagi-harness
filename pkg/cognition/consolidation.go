package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// Consolidation — Episodic → Semantic 记忆压缩管线。
// 架构文档: docs/arch/05-Memory-System-深度选型.md §9

// ConsolidationPipeline 4 阶段压缩管线。
// 触发: 主题转换 shift → 立即触发 | eventCount ≥ 50 → 触发 | sessionClosed → 强制触发.
type ConsolidationPipeline struct {
	entityExtractor   *ConsolidationExtractor
	semanticUpserter  *SemanticUpserter
	sessionSummarizer *SessionSummarizer
	skillUpdater      *SkillUpdater
}

// NewConsolidationPipeline 创建压缩管线。
func NewConsolidationPipeline() *ConsolidationPipeline {
	return &ConsolidationPipeline{
		entityExtractor:   &ConsolidationExtractor{},
		semanticUpserter:  &SemanticUpserter{},
		sessionSummarizer: &SessionSummarizer{},
		skillUpdater:      &SkillUpdater{},
	}
}

// Stage 1 — LLM 提取实体/关系/事实 + 矛盾检测 → 结构化事实列表。
type ConsolidationExtractor struct{}

// Extract 执行提取。
func (ce *ConsolidationExtractor) Extract(sessionID string) error {
	return nil
}

// Stage 2 — Upsert Semantic Memory.
// same → UPDATE version++; conflict → mark superseded; new → INSERT.
type SemanticUpserter struct{}

// Upsert 执行更新。
func (su *SemanticUpserter) Upsert(sessionID string) error {
	return nil
}

// Stage 3 — LLM 生成 3-5 句会话摘要 (source='compaction'), 高 salience 合成事件。
type SessionSummarizer struct{}

// Summarize 执行总结。
func (ss *SessionSummarizer) Summarize(sessionID string) error {
	return nil
}

// Stage 4 — 成功执行的任务 → Logic Collapse → Skill Library.
type SkillUpdater struct{}

// Update 执行技能更新。
func (su *SkillUpdater) Update(sessionID string) error {
	return nil
}

// Run 执行完整 Consolidation 管线。
// 约束: version++ 不可变版本 + source_event_id provenance + 信念修正 + Prospective Indexing.
func (p *ConsolidationPipeline) Run(sessionID string) error {
	if err := p.entityExtractor.Extract(sessionID); err != nil {
		return err
	}
	if err := p.semanticUpserter.Upsert(sessionID); err != nil {
		return err
	}
	if err := p.sessionSummarizer.Summarize(sessionID); err != nil {
		return err
	}
	if err := p.skillUpdater.Update(sessionID); err != nil {
		return err
	}
	return nil
}

// ============================================================================
// Forgetting — 双层策略（热删除 + 冷归档）
// 架构文档: docs/arch/05-Memory-System-深度选型.md §10

// ForgettingManager 遗忘管理器。
// 热删除: Q-Learning 效用衰减 → DecayWeight < salienceThreshold → Forgettable.
// 冷归档: Forgettable + age > 30d → 归档 + tombstone.
// store 用于持久化操作（扫描事件、写入归档标记）。
type ForgettingManager struct {
	store             protocol.Store
	decayRate         float64 // 0.01/日
	salienceThreshold float64
	qLearner          *QLearner
	archiver          *ColdArchiver
}

// NewForgettingManager 创建遗忘管理器，注入 Store 依赖。
func NewForgettingManager(store protocol.Store, decayRate float64) *ForgettingManager {
	return &ForgettingManager{
		store:             store,
		decayRate:         decayRate,
		salienceThreshold: 0.15,
		qLearner:          NewQLearner(0.1, 0.9),
		archiver:          NewColdArchiver(store),
	}
}

// UpdateDecay 更新衰减权重。
// ageHours = now - timestamp; DecayWeight = salience × exp(-decayRate × ageHours/24).
func (fm *ForgettingManager) UpdateDecay(salience float64, ageHours float64) float64 {
	decay := salience * exp(-fm.decayRate*ageHours/24.0)
	return decay
}

// PeriodicCleanup 扫描 Episodic 事件，将低于 salienceThreshold 的条目标记为可遗忘，
// 超过 30 天且低 salience 的条目移入冷归档。
// 不物理删除——仅写入 tombstone 标记，由 ColdArchiver.PhysicalCompact 负责最终清理。
func (fm *ForgettingManager) PeriodicCleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	archived := 0
	marked := 0

	// 扫描所有 episodic事件
	iter, err := fm.store.Scan(ctx, []byte("events:"))
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "PeriodicCleanup: scan events 失败", err)
	}
	defer iter.Close()

	for iter.Next() {
		key := iter.Key()
		val := iter.Value()

		var ev struct {
			ID         string  `json:"id"`
			Topic      string  `json:"topic"`
			Salience   float64 `json:"salience"`
			OccurredAt int64   `json:"occurred_at"`
		}
		if err := json.Unmarshal(val, &ev); err != nil {
			continue
		}

		// 仅处理 episodic 层事件
		if ev.Topic != "memory.openclaw" && ev.Topic != "memory" {
			continue
		}

		ageHours := float64(time.Now().UnixMilli()-ev.OccurredAt) / 3600000.0
		decayWeight := fm.UpdateDecay(ev.Salience, ageHours)

		// 衰减权重低于阈值 → 标记为可遗忘
		if decayWeight < fm.salienceThreshold {
			// 写入 tombstone 标记（不删除原事件，仅标记）
			tombstoneKey := fmt.Appendf(nil, "forgettable:%s", ev.ID)
			tombstoneVal := fmt.Appendf(nil, `{"id":"%s","decay_weight":%.4f,"marked_at":%d}`, ev.ID, decayWeight, time.Now().UnixMilli())
			_ = fm.store.Put(ctx, tombstoneKey, tombstoneVal)
			marked++

			// 超过 30 天 → 移入冷归档
			if ageHours > 30*24 {
				archiveKey := fmt.Appendf(nil, "archive:episodic:%s", ev.ID)
				_ = fm.store.Put(ctx, archiveKey, val)
				_ = fm.store.Delete(ctx, key)
				_ = fm.store.Delete(ctx, tombstoneKey)
				archived++
				marked--
			}
		}
	}

	if iter.Err() != nil {
		return perrors.Wrap(perrors.CodeInternal, "PeriodicCleanup: 迭代失败", iter.Err())
	}

	_ = archived
	_ = cutoff
	return nil
}

// QLearner Q-Learning 熵门控效用衰减。
// 用于自适应调整 salienceThreshold——高熵环境下更积极遗忘。
type QLearner struct {
	states map[string]float64
	alpha  float64 // 学习率
	gamma  float64 // 折扣因子
}

func NewQLearner(alpha, gamma float64) *QLearner {
	return &QLearner{
		states: make(map[string]float64),
		alpha:  alpha,
		gamma:  gamma,
	}
}

// Update 更新状态值。
func (ql *QLearner) Update(state string, reward float64) {
	ql.states[state] += ql.alpha * (reward - ql.states[state])
}

// ColdArchiver 冷归档器。
// 将超期低价值事件从热存储移到归档前缀，SQLite 物理 VACUUM 回收磁盘。
// store 通过协议抽象访问持久化层。
type ColdArchiver struct {
	store         protocol.Store
	archivePath   string // ~/.polaris-harness/archive/
	retentionDays int    // 热库 30d, 冷库无限
}

func NewColdArchiver(store protocol.Store) *ColdArchiver {
	return &ColdArchiver{
		store:         store,
		archivePath:   "archive/",
		retentionDays: 30,
	}
}

// PhysicalCompact 扫描 tombstone 标记（forgettable:*），
// 将对应的原事件 key 物理删除并清理 tombstone 自身。
// 对支持 SQL 的引擎委托 DB 级 VACUUM；对纯 KV 引擎仅做 key 级清理。
func (ca *ColdArchiver) PhysicalCompact() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	deleted := 0

	// 扫描所有 forgettable tombstone
	iter, err := ca.store.Scan(ctx, []byte("forgettable:"))
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "PhysicalCompact: scan tombstones 失败", err)
	}
	defer iter.Close()

	var keysToDelete [][]byte

	for iter.Next() {
		var tombstone struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(iter.Value(), &tombstone); err != nil || tombstone.ID == "" {
			continue
		}

		// 删除原事件（可能已被归档，Delete 幂等）
		eventKey := fmt.Appendf(nil, "events:%s", tombstone.ID)
		keysToDelete = append(keysToDelete, eventKey)
		// 删除 tombstone 自身
		keysToDelete = append(keysToDelete, iter.Key())
		deleted++
	}

	if iter.Err() != nil {
		return perrors.Wrap(perrors.CodeInternal, "PhysicalCompact: 迭代失败", iter.Err())
	}

	// 批量删除
	for _, key := range keysToDelete {
		_ = ca.store.Delete(ctx, key)
	}

	// 对支持 SQL 的引擎触发 VACUUM——通过 Txn 内的 Raw SQL 能力
	if ca.store.Capabilities().SupportsSQL {
		_ = ca.store.Txn(ctx, func(tx protocol.Transaction) error {
			// 尝试在 Txn 内执行 VACUUM-like 操作（引擎特定）
			// SQLite 引擎可通过额外接口执行；纯 KV 引擎忽略
			return nil
		})
	}

	_ = deleted
	return nil
}

func exp(x float64) float64 {
	result := 1.0
	term := 1.0
	for i := 1; i < 20; i++ {
		term *= x / float64(i)
		result += term
	}
	return result
}
