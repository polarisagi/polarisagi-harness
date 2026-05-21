package eval

import (
	"context"
	"encoding/json"
	"fmt"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// SQLiteEvalStore 实现了 protocol.EvalAPI，用于管理 EvalCase。
// 数据持久化基于 protocol.Store (SQLite 驱动)。
// 架构文档: docs/arch/M12-Eval-Harness.md §5
type SQLiteEvalStore struct {
	store protocol.Store
}

var _ protocol.EvalAPI = (*SQLiteEvalStore)(nil)

func NewSQLiteEvalStore(store protocol.Store) *SQLiteEvalStore {
	return &SQLiteEvalStore{store: store}
}

// GetTrainingCases 获取用于训练和优化的评测用例 (Training Set)。
func (s *SQLiteEvalStore) GetTrainingCases(ctx context.Context, agentRole string, signature []byte) ([]any, error) {
	// MVP: 忽略签名校验，直接从 store 前缀扫描
	// 架构约束：DataSplitter 分区，SourceSynthetic → Training
	return s.scanCasesByPrefix(ctx, "eval:case:training:"+agentRole+":")
}

// GetValidationCases 获取用于泛化验证的评测用例 (Holdout Set)。
func (s *SQLiteEvalStore) GetValidationCases(ctx context.Context, agentRole string, signature []byte) ([]any, error) {
	return s.scanCasesByPrefix(ctx, "eval:case:validation:"+agentRole+":")
}

// PutCase 保存一个新的 EvalCase 到指定分区 (training 或 validation)。
func (s *SQLiteEvalStore) PutCase(ctx context.Context, partition, agentRole string, c EvalCase) error {
	if partition != "training" && partition != "validation" {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("eval_store: invalid partition %s", partition))
	}
	key := fmt.Sprintf("eval:case:%s:%s:%s", partition, agentRole, c.ID)
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, []byte(key), data)
}

func (s *SQLiteEvalStore) scanCasesByPrefix(ctx context.Context, prefix string) ([]any, error) {
	iter, err := s.store.Scan(ctx, []byte(prefix))
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var cases []any
	for iter.Next() {
		var c EvalCase
		if err := json.Unmarshal(iter.Value(), &c); err == nil {
			cases = append(cases, c)
		}
	}
	return cases, nil
}
