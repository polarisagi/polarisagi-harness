package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// buildPerceiveContext 基于当前状态上下文（包含用户的原始任务描述/Intent）
// 从 EpisodicMemory 与 WorkingMemory 组装感知阶段所需的 LLM 提示词。
// 注：Agent 启动时的原始意图可以通过 context 传入，当前暂无单独的 Intent 字段，
// 我们假设它在 S_PERCEIVE 阶段是从黑板或用户输入获取的，暂时用一段说明占位。
func buildPerceiveContext(ctx context.Context, memory protocol.Memory, sCtx *StateContext) ([]protocol.Message, error) {
	if memory == nil {
		return []protocol.Message{
			{Role: "system", Content: "将用户意图结构化为 TaskModel JSON。"},
		}, nil
	}

	// 1. 查询相关的历史 Episodic 事件（如相似的任务意图）
	query := protocol.EpisodicQuery{
		Semantic: "agent task intent", // 临时检索词
		K:        3,                   // 获取 Top-3 历史相关事件
	}
	events, err := memory.Episodic().Query(ctx, query)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "failed to query episodic memory", err)
	}

	var episodicCtx string
	if len(events) > 0 {
		episodicCtx = "相关历史记忆：\n"
		for _, e := range events {
			episodicCtx += fmt.Sprintf("- [%s] %s: %s\n", e.Event.CreatedAt.Format(time.RFC3339), e.Event.Type, string(e.Event.Payload))
		}
	}

	// 2. 组装 WorkingMemory（ImmutableCore 规范等）
	// 此处仅做示例拼接，实际由 ImmutableCore.PrependToMessages 统一处理
	baseContent := "将用户意图结构化为 TaskModel JSON。\n\n"
	if len(sCtx.ReasoningState) > 0 {
		baseContent += "上一轮的推理状态（ReasoningState）：\n" + string(sCtx.ReasoningState) + "\n\n"
	}
	if episodicCtx != "" {
		baseContent += episodicCtx + "\n"
	}

	msgs := []protocol.Message{
		{Role: "system", Content: baseContent},
	}

	// 注入不可变核心区规则
	if wm := memory.Working(); wm != nil {
		msgs = wm.Immutable().PrependToMessages(msgs)
	}

	return msgs, nil
}

// buildPlanContext 基于已解析的 TaskModel
// 从 Memory 系统组装生成 DAG 计划所需的 LLM 提示词。
func buildPlanContext(ctx context.Context, memory protocol.Memory, sCtx *StateContext) ([]protocol.Message, error) {
	if memory == nil {
		return []protocol.Message{
			{Role: "system", Content: "基于 TaskModel 生成执行 DAG。"},
		}, nil
	}

	// 查询与任务目标或已有子任务相关的历史记忆
	var queryStr string
	if sCtx.TaskModel != nil {
		queryStr = sCtx.TaskModel.Goal
	}
	query := protocol.EpisodicQuery{
		Semantic: queryStr,
		K:        5,
	}
	events, err := memory.Episodic().Query(ctx, query)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "failed to query episodic memory", err)
	}

	var episodicCtx string
	if len(events) > 0 {
		episodicCtx = "可供参考的历史执行经验：\n"
		for _, e := range events {
			episodicCtx += fmt.Sprintf("- [%s] %s: %s\n", e.Event.CreatedAt.Format(time.RFC3339), e.Event.Type, string(e.Event.Payload))
		}
	}

	baseContent := "基于 TaskModel 生成执行 DAG。\n\n"
	if episodicCtx != "" {
		baseContent += episodicCtx + "\n"
	}

	if sCtx.TaskModel != nil {
		taskJson, _ := json.Marshal(sCtx.TaskModel)
		baseContent += "已解析的任务模型：\n" + string(taskJson)
	}

	msgs := []protocol.Message{
		{Role: "system", Content: baseContent},
	}

	if wm := memory.Working(); wm != nil {
		msgs = wm.Immutable().PrependToMessages(msgs)
	}

	return msgs, nil
}

// buildReflectContext 组装反思阶段的 Prompt。
func buildReflectContext(ctx context.Context, memory protocol.Memory, sCtx *StateContext) ([]protocol.Message, error) {
	if memory == nil {
		return []protocol.Message{
			{Role: "system", Content: "反思执行结果，评估目标达成度。"},
		}, nil
	}

	baseContent := "反思执行结果，评估目标达成度。\n\n"

	if len(sCtx.ExecuteResult) > 0 {
		baseContent += "执行结果摘要：\n" + string(sCtx.ExecuteResult) + "\n\n"
	}

	msgs := []protocol.Message{
		{Role: "system", Content: baseContent},
	}

	if wm := memory.Working(); wm != nil {
		msgs = wm.Immutable().PrependToMessages(msgs)
	}

	return msgs, nil
}
