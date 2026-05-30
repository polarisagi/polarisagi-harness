package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// buildPlanContext 基于已解析的 TaskModel 和可用工具列表
// 从 Memory 系统组装生成 DAG 计划所需的 LLM 提示词。
// tools 为 nil 时跳过工具注入（测试环境）。
func buildPlanContext(ctx context.Context, memory protocol.Memory, sCtx *StateContext, tools protocol.ToolRegistry) ([]protocol.Message, error) {
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
		baseContent += "已解析的任务模型：\n" + string(taskJson) + "\n\n"
	}

	// 注入可用工具列表，LLM 必须仅使用列表中的工具名称（action 字段）
	if tools != nil {
		baseContent += buildToolListSection(tools)
	}

	msgs := []protocol.Message{
		{Role: "system", Content: baseContent},
	}

	if wm := memory.Working(); wm != nil {
		msgs = wm.Immutable().PrependToMessages(msgs)
	}

	return msgs, nil
}

// buildToolListSection 将注册表中所有工具格式化为 LLM 可读的工具定义段落。
// 格式与 DAGNode.Action + DAGNode.Params 字段对齐，便于 LLM 直接引用。
func buildToolListSection(tools protocol.ToolRegistry) string {
	list := tools.List()
	if len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("可用工具列表（DAG 节点的 action 字段必须为以下名称之一）：\n")
	for _, t := range list {
		sb.WriteString(fmt.Sprintf("- %s: %s", t.Name, t.Description))
		if t.InputSchema != nil {
			if schemaBytes, err := json.Marshal(t.InputSchema); err == nil {
				sb.WriteString(fmt.Sprintf("（参数 schema: %s）", string(schemaBytes)))
			}
		}
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	return sb.String()
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
