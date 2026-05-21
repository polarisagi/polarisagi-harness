package swarm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// SQLiteRolloutStore 实现 StagingPipeline 接口，State-in-DB。
// 架构文档: docs/arch/M09-Self-Improvement-Engine.md §2.3
//
// 表结构（由 storage 层 schema 创建，此处使用 CREATE TABLE IF NOT EXISTS）:
//   rollout_states (version TEXT PRIMARY KEY, baseline TEXT, current_gate INT,
//                   canary_percent INT, status TEXT, started_at INT, last_advanced_at INT,
//                   metadata TEXT)

const createRolloutTable = `
CREATE TABLE IF NOT EXISTS rollout_states (
	version          TEXT PRIMARY KEY,
	baseline         TEXT    NOT NULL,
	current_gate     INTEGER NOT NULL DEFAULT 0,
	canary_percent   INTEGER NOT NULL DEFAULT 0,
	status           TEXT    NOT NULL DEFAULT 'pending',
	started_at       INTEGER NOT NULL,
	last_advanced_at INTEGER NOT NULL,
	metadata         TEXT    NOT NULL DEFAULT '{}'
)`

// SQLiteRolloutStore 持久化渐进发布状态。
type SQLiteRolloutStore struct {
	db      *sql.DB
	rollout *ProgressiveRollout
}

// NewSQLiteRolloutStore 创建 RolloutStore 并确保表存在。
func NewSQLiteRolloutStore(db *sql.DB) (*SQLiteRolloutStore, error) {
	if _, err := db.Exec(createRolloutTable); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "rollout_store: create table", err)
	}
	return &SQLiteRolloutStore{
		db:      db,
		rollout: NewProgressiveRollout(),
	}, nil
}

// SubmitCandidate 提交新候选版本，初始化为 Gate 0 Pending。
func (s *SQLiteRolloutStore) SubmitCandidate(ctx context.Context, snap *AgentVersionSnapshot) error {
	now := time.Now().Unix()
	meta, _ := json.Marshal(snap)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rollout_states
			(version, baseline, current_gate, canary_percent, status, started_at, last_advanced_at, metadata)
		VALUES (?, 'baseline', 0, 1, 'pending', ?, ?, ?)
		ON CONFLICT(version) DO NOTHING
	`, snap.Version, now, now, string(meta))
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "rollout_store.SubmitCandidate", err)
	}
	return nil
}

// AdvanceGate 根据当前统计数据推进或回滚。
// 规则：
//   - CheckHardStop → 触发 → 自动 Rollback
//   - 当前 Gate 最后推进时间 < 24h → 跳过（防止快速跳过）
//   - CanaryPercent 按 canarySteps 推进
func (s *SQLiteRolloutStore) AdvanceGate(ctx context.Context, version string, stats RolloutStats) (*RolloutState, error) {
	state, err := s.GetState(ctx, version)
	if err != nil {
		return nil, err
	}
	if state.Status == RolloutStatusRolledBack || state.Status == RolloutStatusCommitted {
		return state, nil
	}

	// 硬停止检查
	if s.rollout.CheckHardStop(stats) {
		return s.Rollback(ctx, version, "hard stop: error rate / latency / safety violation exceeded threshold")
	}

	// 24h 稳定期检查（防止快速推进）
	if time.Since(time.Unix(state.LastAdvancedAt, 0)) < 24*time.Hour {
		return state, nil // 尚未稳定，维持当前状态
	}

	// 推进 Canary 百分比
	nextPercent, nextGate := s.rollout.NextStep(state.CanaryPercent, int(state.CurrentGate))
	newStatus := RolloutStatusRunning
	if nextPercent >= 100 {
		newStatus = RolloutStatusCommitted
	}

	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		UPDATE rollout_states
		SET current_gate = ?, canary_percent = ?, status = ?, last_advanced_at = ?
		WHERE version = ?
	`, nextGate, nextPercent, string(newStatus), now, version)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "rollout_store.AdvanceGate", err)
	}

	return s.GetState(ctx, version)
}

// Rollback 将版本状态设为 rolled_back。
func (s *SQLiteRolloutStore) Rollback(ctx context.Context, version string, reason string) (*RolloutState, error) {
	meta := fmt.Sprintf(`{"rollback_reason":%q,"at":%d}`, reason, time.Now().Unix())
	_, err := s.db.ExecContext(ctx, `
		UPDATE rollout_states SET status = 'rolled_back', metadata = ? WHERE version = ?
	`, meta, version)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "rollout_store.Rollback", err)
	}
	return s.GetState(ctx, version)
}

// GetState 从 SQLite 读取当前 RolloutState。
func (s *SQLiteRolloutStore) GetState(ctx context.Context, version string) (*RolloutState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT version, baseline, current_gate, canary_percent, status, started_at, last_advanced_at
		FROM rollout_states WHERE version = ?
	`, version)

	var st RolloutState
	var baseline string
	if err := row.Scan(&st.CandidateVersion, &baseline, &st.CurrentGate,
		&st.CanaryPercent, &st.Status, &st.StartedAt, &st.LastAdvancedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("rollout_store: version %q not found", version))
		}
		return nil, err
	}
	st.BaselineVersion = baseline
	return &st, nil
}

// NextStep 根据当前 CanaryPercent 返回下一步的 (percent, gate)。
func (pr *ProgressiveRollout) NextStep(currentPercent, currentGate int) (int, int) {
	for i, step := range pr.canarySteps {
		if step > currentPercent {
			return step, currentGate + 1
		}
		_ = i
	}
	return 100, currentGate + 1 // 全量
}
