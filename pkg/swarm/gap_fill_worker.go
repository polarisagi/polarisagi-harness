package swarm

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// GapFillWorker 监听 m9_capability_gap Outbox 事件，执行能力补全。
// 架构文档: docs/arch/M09-Self-Improvement.md
type GapFillWorker struct {
	mem      protocol.Memory
	provider protocol.Provider
	registry protocol.ToolRegistry
}

func NewGapFillWorker(mem protocol.Memory, provider protocol.Provider, registry protocol.ToolRegistry) *GapFillWorker {
	return &GapFillWorker{
		mem:      mem,
		provider: provider,
		registry: registry,
	}
}

// Run 启动 Worker，轮询或监听事件。
func (w *GapFillWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.processGaps(ctx)
		}
	}
}

func (w *GapFillWorker) processGaps(ctx context.Context) {
	query := protocol.EpisodicQuery{
		// 实际上查询条件应包含类型和状态
	}
	events, err := w.mem.Episodic().Query(ctx, query)
	if err != nil {
		return
	}

	for _, e := range events {
		if e.Event.Type != "m9_capability_gap" || e.Event.Status != protocol.StatusPending {
			continue
		}

		var payload struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(e.Event.Payload, &payload); err != nil {
			continue
		}

		missingTool := w.extractMissingTool(payload.Error)
		if missingTool == "" {
			_ = w.markEvent(ctx, e.Event.ID, protocol.StatusFailed)
			continue
		}

		// 1. 本地合成
		err = w.synthesizeSkill(ctx, missingTool)
		if err == nil {
			_ = w.markEvent(ctx, e.Event.ID, protocol.StatusDone)
		} else {
			_ = w.markEvent(ctx, e.Event.ID, protocol.StatusFailed)
		}
	}
}

func (w *GapFillWorker) extractMissingTool(errStr string) string {
	if idx := strings.Index(errStr, "tool not found: "); idx != -1 {
		return strings.TrimSpace(errStr[idx+16:])
	}
	if idx := strings.Index(errStr, "tool \""); idx != -1 {
		end := strings.Index(errStr[idx+6:], "\"")
		if end != -1 {
			return errStr[idx+6 : idx+6+end]
		}
	}
	return "unknown"
}

func (w *GapFillWorker) synthesizeSkill(ctx context.Context, toolName string) error {
	gen := NewSyntheticSkillGen(w.provider)
	skill, err := gen.Generate(ctx, toolName, "Auto-synthesized skill for "+toolName)
	if err != nil {
		return err
	}
	if w.registry != nil {
		_ = w.registry.Register(skill)
	}
	return nil
}

func (w *GapFillWorker) markEvent(ctx context.Context, eventID string, status protocol.EventStatus) error {
	return w.mem.Episodic().Append(ctx, protocol.Event{
		ID:        eventID + "_update",
		Type:      "m9_capability_gap_update",
		Status:    status,
		CreatedAt: time.Now(),
	})
}
