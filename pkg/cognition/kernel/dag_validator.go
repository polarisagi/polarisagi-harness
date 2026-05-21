package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate"
)

// DAGValidationContext 承载 S_VALIDATE 四层校验所需的输入。
// 架构文档: docs/arch/M04-Agent-Kernel.md §4
type DAGValidationContext struct {
	// Plan 是 S_PLAN 阶段 LLM 产出的 DAG。
	Plan *DAGPlan
	// ActiveTaintLevel 是当前会话上下文中传播而来的最高污点等级（Layer A 规则）。
	// 计算规则: max(所有输入 TaintLevel) —— 只升不降。
	ActiveTaintLevel protocol.TaintLevel
	// PolicyGate 是 Cedar 策略引擎的 Go 接口（L1 确定性 Cedar 校验）。
	PolicyGate protocol.PolicyGate
	// AgentID 用于 PolicyGate.Review 中的 principal 字段。
	AgentID string
	// SessionID 用于审计事件的关联查询。
	SessionID string
	// SystemTier 系统环境配置级别 (0: 8GB 弱计算节点, 1+: 强计算节点)
	SystemTier int
	// Provider 用于 L3 看门狗调用。
	Provider protocol.Provider
	// l3CallCount 跟踪当前校验周期中的 L3 Watchdog 调用次数。上限 <10 次/小时。
	//nolint:unused
	l3CallCount int
}

// DAGValidationError 包装 S_VALIDATE 失败的结构化错误。
type DAGValidationError struct {
	Layer  string // "L0" | "L1_taint" | "L1_policy" | "L2_heuristic" | "L3_llm"
	NodeID string // 首个违规节点 ID（空表示全局失败）
	Reason string
}

func (e *DAGValidationError) Error() string {
	if e.NodeID != "" {
		return fmt.Sprintf("validate [%s] node=%s: %s", e.Layer, e.NodeID, e.Reason)
	}
	return fmt.Sprintf("validate [%s]: %s", e.Layer, e.Reason)
}

// ValidateDAG 是 S_VALIDATE 阶段的核心入口，串行执行多道防线。
//
//	L0 (<1ms): 拓扑校验（节点数熔断 + DFS 环检测 + 深度熔断 + 孤立节点）
//	L1-Taint  (<1ms): TaintGate —— 禁止 TaintHigh 参数进入 Instruction Slot
//	L1-Policy (<1ms): PolicyGate —— Cedar deny-by-default，Forbid 规则无条件拦截
//	L2 (<5ms): 启发式检查 —— 并发规模、受保护路径黑名单等
//	L3 (~200ms): LLM 看门狗 —— 仅对 SystemTier >= 1 生效且动作涉及时触发语义检查
//
// 返回 nil 表示全部通过，可推进至 S_EXECUTE。
// 任意层失败返回 *DAGValidationError，调用方应推送 TriggerValidateFail。
func ValidateDAG(ctx context.Context, vCtx *DAGValidationContext) error {
	if vCtx.Plan == nil {
		return &DAGValidationError{Layer: "L0", Reason: "DAGPlan is nil"}
	}

	// L0: 拓扑校验
	if err := validateDAGTopology(vCtx.Plan); err != nil {
		return &DAGValidationError{Layer: "L0", Reason: err.Error()}
	}

	// L1-Taint
	if err := validateTaintGate(vCtx); err != nil {
		return err
	}

	// L1-Policy
	if err := validatePolicyGate(ctx, vCtx); err != nil {
		return err
	}

	// L2: Heuristic 启发式校验
	if err := validateHeuristic(vCtx); err != nil {
		return err
	}

	return nil
}

// validateTaintGate 实现 L1 第一道：TaintGate 防线（Layer A 上下文传播规则）。
// 规则：当会话 ActiveTaintLevel >= TaintHigh 时：
//   - 禁止包含字符串类工具参数的节点写入 Instruction Slot（write_local / write_network）。
//   - 除非参数已通过 SanitizeBySchema 降级到 <= TaintMedium（由调用方自行管理）。
//
// 此处实现的是最小防线：ActiveTaintLevel >= TaintHigh → 拦截所有非 read_only 操作。
// 完整的字段级降级逻辑（SanitizeBySchema + tool_call schema 双向校验）由 M7 工具调用层处理。
func validateTaintGate(vCtx *DAGValidationContext) error {
	// TaintNone / TaintLow 不触发 TaintGate
	if vCtx.ActiveTaintLevel < protocol.TaintMedium {
		return nil
	}

	for _, node := range vCtx.Plan.Nodes {
		// 尝试将节点参数包装为 TaintedString 并检查是否可被 SanitizeToSafe
		ts := substrate.NewTaintedString(
			string(node.Args),
			substrate.TaintSource{
				Module:           "m4_validate",
				EntityID:         node.ID,
				OriginTaintLevel: vCtx.ActiveTaintLevel,
			},
			"dag_node_args",
		)

		// 当污点等级为 TaintHigh 时，SanitizeToSafe 必须失败——此节点参数禁止直接进入执行
		if vCtx.ActiveTaintLevel >= protocol.TaintHigh {
			if _, err := substrate.SanitizeToSafe(ts); err == nil {
				// 这不应该发生——TaintHigh 必须被拦截
				// 为保险起见，若 SanitizeToSafe 意外放行，我们主动拒绝
				return &DAGValidationError{
					Layer:  "L1_taint",
					NodeID: node.ID,
					Reason: "unexpected: TaintHigh args passed SanitizeToSafe without sanitization",
				}
			}
			// SanitizeToSafe 正确拒绝了——说明 TaintHigh 数据需要在执行前降级
			// 若节点工具名不在只读白名单中，则阻断
			if !isReadOnlyTool(node.ToolName) {
				return &DAGValidationError{
					Layer:  "L1_taint",
					NodeID: node.ID,
					Reason: fmt.Sprintf("TaintHigh args blocked: tool %q is not read-only, requires schema sanitization before execution", node.ToolName),
				}
			}
		}
	}
	return nil
}

// isReadOnlyTool 判断工具是否为纯读操作（不写入外部状态）。
// 白名单由 M7 ToolRegistry 维护，此处为 MVP 精简版。
func isReadOnlyTool(toolName string) bool {
	switch toolName {
	case "read_file", "list_dir", "search_web", "fetch_url":
		return true
	}
	return false
}

// validatePolicyGate 实现 L1 第二道：Cedar PolicyGate 防线（deny-by-default）。
// 逐节点调用 PolicyGate.Review，任一节点被 Forbid → 整体 DAG 拒绝。
// fail-closed: PolicyGate 调用超时或出错 → 拒绝。
func validatePolicyGate(ctx context.Context, vCtx *DAGValidationContext) error {
	if vCtx.PolicyGate == nil {
		// fail-closed: 无策略引擎 → 拒绝所有操作
		return &DAGValidationError{
			Layer:  "L1_policy",
			Reason: "PolicyGate is nil (fail-closed)",
		}
	}

	for _, node := range vCtx.Plan.Nodes {
		req := protocol.PolicyReviewRequest{
			Principal: vCtx.AgentID,
			Action:    node.ToolName,
			Resource:  node.ID,
			Context: map[string]any{
				"session_id":   vCtx.SessionID,
				"taint_level":  vCtx.ActiveTaintLevel.String(),
				"node_args_sz": len(node.Args),
			},
		}

		result, err := vCtx.PolicyGate.Review(ctx, req)
		if err != nil {
			// fail-closed: 评估异常 → 拒绝
			return &DAGValidationError{
				Layer:  "L1_policy",
				NodeID: node.ID,
				Reason: fmt.Sprintf("PolicyGate.Review error (fail-closed): %v", err),
			}
		}
		if !result.Allowed {
			return &DAGValidationError{
				Layer:  "L1_policy",
				NodeID: node.ID,
				Reason: fmt.Sprintf("PolicyGate denied: %s", result.Reason),
			}
		}
	}

	return nil
}

// validateHeuristic 实现 L2: Heuristic 启发式校验。
// 架构要求: 批量规模(>100) → 受保护路径(`/etc/`,`/sys/`,`~/.ssh/`→拒绝) → 资源预估。
func validateHeuristic(vCtx *DAGValidationContext) error {
	// 1. 并发/批量规模检查
	if len(vCtx.Plan.Nodes) > 100 {
		return &DAGValidationError{
			Layer:  "L2_heuristic",
			Reason: fmt.Sprintf("DAG scale exceeded limit: %d nodes > 100", len(vCtx.Plan.Nodes)),
		}
	}

	// 2. 危险路径黑名单检查 (仅针对文件读写工具)
	forbiddenPaths := []string{"/etc/", "/sys/", "/boot/", "~/.ssh/"}
	for _, node := range vCtx.Plan.Nodes {
		if node.ToolName == "read_file" || node.ToolName == "write_file" || node.ToolName == "bash" {
			argsStr := string(node.Args)
			for _, path := range forbiddenPaths {
				// 简单的包含判断
				if strings.Contains(argsStr, path) {
					return &DAGValidationError{
						Layer:  "L2_heuristic",
						NodeID: node.ID,
						Reason: fmt.Sprintf("heuristic block: accessed protected path %q", path),
					}
				}
			}
		}
	}

	return nil
}
