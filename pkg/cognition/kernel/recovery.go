package kernel

import (
	"context"
	"encoding/json"
	"log/slog"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// ProviderRecoveryHandler 处理 M1 CircuitBreaker 恢复后的唤醒逻辑。
// 此 handler 注册到 M2 全局 Outbox Worker (pkg/substrate/outbox_worker.go)。
// 调用 M11.SessionPIIVault.RestoreFromSnapshot -> M8.Blackboard.ResumeFromSuspended
type ProviderRecoveryHandler struct {
	// 实际应注入 M11.SessionPIIVault 和 M8.Blackboard 接口
}

func NewProviderRecoveryHandler() *ProviderRecoveryHandler {
	return &ProviderRecoveryHandler{}
}

func (h *ProviderRecoveryHandler) Handle(ctx context.Context, payload []byte) error {
	var data struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return perrors.Wrap(perrors.CodeInternal, "failed to unmarshal payload", err)
	}

	// 1. 调用 M11.SessionPIIVault.RestoreFromSnapshot(ctx, data.TaskID)
	// 2. 调用 M8.Blackboard.ResumeFromSuspended(ctx, data.TaskID)

	// 示例日志
	slog.Info("kernel: recovering task after provider exhaustion", "task_id", data.TaskID)
	return nil
}
