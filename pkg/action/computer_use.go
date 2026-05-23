package action

import (
	"context"
	"encoding/json"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// ComputerUseTool 提供基于底层 Rust MCP Sidecar 的 GUI 自动化能力。
// 架构文档: docs/arch/07-Tool-Action-Layer-深度选型.md §7.1
type ComputerUseTool struct {
	mcpManager *MCPManager
	serverID   string
	toolName   string
}

// NewComputerUseTool 创建基于 MCP 的 ComputerUseTool 代理实例。
func NewComputerUseTool(mcpManager *MCPManager) *ComputerUseTool {
	return &ComputerUseTool{
		mcpManager: mcpManager,
		serverID:   "polaris-computer",
		toolName:   "computer_use_action",
	}
}

// Execute 接收 JSON 序列化的动作参数，并透明转发给 MCP Sidecar 执行。
func (c *ComputerUseTool) Execute(ctx context.Context, input []byte) ([]byte, error) {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: invalid args", err)
	}

	// 转发给 MCP Manager 进行执行
	resultText, err := c.mcpManager.CallTool(ctx, c.serverID, c.toolName, args)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "computer_use: mcp call failed", err)
	}

	// MCP Sidecar 返回的是文本或图片 Base64 的包装，这里直接返回其序列化后的内容
	return []byte(resultText), nil
}
