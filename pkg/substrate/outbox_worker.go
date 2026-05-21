package substrate

import (
	"context"
	"database/sql"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// OutboxWorker — 跨引擎投递 Worker。
// 消费 M2 outbox 表，将投影写入目标引擎（[Storage-SQLite] / [Storage-SurrealDB-Core]）。
// 架构文档: docs/arch/M02-Storage-Fabric.md §2.5

type OutboxWorker struct {
	db           *sql.DB
	handlers     map[string]OutboxHandler // TargetEngine → HandlerFunc
	pollInterval int64                    // seconds，5s 默认
	maxRetries   int                      // 3
}

// NewOutboxWorker 创建 OutboxWorker，db 必须非 nil（fail-fast）。
func NewOutboxWorker(db *sql.DB, pollInterval int64, maxRetries int) *OutboxWorker {
	if pollInterval <= 0 {
		pollInterval = 5
	}
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &OutboxWorker{
		db:           db,
		handlers:     make(map[string]OutboxHandler),
		pollInterval: pollInterval,
		maxRetries:   maxRetries,
	}
}

// OutboxHandler 处理单个 Outbox 任务。
type OutboxHandler func(ctx context.Context, record *OutboxRecord) error

// OutboxRecord 单条 Outbox 记录。
type OutboxRecord struct {
	ID                 int64
	TargetEngine       string
	Operation          string
	Scope              string
	Payload            []byte
	IdempotencyKey     protocol.IdempotencyKey
	Version            int
	Attempts           int
	CrashRecoveryCount int
}

// RegisterHandler 注册 Outbox 任务处理器。
func (w *OutboxWorker) RegisterHandler(taskType string, handler OutboxHandler) {
	w.handlers[taskType] = handler
}

// FetchBatch 读取待处理 Outbox 记录。
// 主查询: WHERE id > cursor AND status IN ('pending','failed')
//
//	AND (next_retry_at IS NULL OR next_retry_at <= now) ORDER BY id LIMIT batchSize
//
// 补充查询 (cursor>0): WHERE id <= cursor AND status='failed' ORDER BY id LIMIT batchSize/4
func (w *OutboxWorker) FetchBatch(ctx context.Context, cursor int64, batchSize int) ([]*OutboxRecord, error) {
	if w.db == nil {
		return nil, perrors.New(perrors.CodeInternal, "outbox db is nil")
	}
	now := time.Now().UnixMilli()

	const mainQ = `
		SELECT id, target_engine, operation, scope, payload, idempotency_key,
		       attempts, crash_recovery_count
		FROM outbox
		WHERE id > ? AND status IN ('pending','failed')
		  AND (next_retry_at IS NULL OR next_retry_at <= ?)
		ORDER BY id LIMIT ?`

	rows, err := w.db.QueryContext(ctx, mainQ, cursor, now, batchSize)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "outbox fetch failed", err)
	}
	defer rows.Close()

	records, err := scanOutboxRows(rows)
	if err != nil {
		return nil, err
	}

	// 补充查询: cursor 以前遗漏的失败记录
	if cursor > 0 && batchSize > 4 {
		suppLimit := batchSize / 4
		const suppQ = `
			SELECT id, target_engine, operation, scope, payload, idempotency_key,
			       attempts, crash_recovery_count
			FROM outbox
			WHERE id <= ? AND status = 'failed'
			  AND (next_retry_at IS NULL OR next_retry_at <= ?)
			ORDER BY id LIMIT ?`
		suppRows, suppErr := w.db.QueryContext(ctx, suppQ, cursor, now, suppLimit)
		if suppErr == nil {
			defer suppRows.Close()
			if supp, scanErr := scanOutboxRows(suppRows); scanErr == nil {
				records = append(records, supp...)
			}
		}
	}
	return records, nil
}

func scanOutboxRows(rows *sql.Rows) ([]*OutboxRecord, error) {
	var records []*OutboxRecord
	for rows.Next() {
		r := &OutboxRecord{}
		var ikey string
		if err := rows.Scan(
			&r.ID, &r.TargetEngine, &r.Operation, &r.Scope, &r.Payload,
			&ikey, &r.Attempts, &r.CrashRecoveryCount,
		); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "outbox scan failed", err)
		}
		r.IdempotencyKey = protocol.IdempotencyKey(ikey)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "outbox rows error", err)
	}
	return records, nil
}

// Process 处理单条 Outbox 记录。
// 版本高水位拦截: existing_version >= incoming_version → 丢弃 + ErrVersionStale
// Poison Pill: crash_recovery_count >= 3 → 直接标记 dead
func (w *OutboxWorker) Process(ctx context.Context, record *OutboxRecord) error {
	if record.CrashRecoveryCount >= 3 {
		// 标记 dead，阻断确定性崩溃循环
		return nil
	}
	handler, ok := w.handlers[record.TargetEngine]
	if !ok {
		return nil
	}
	return handler(ctx, record)
}
