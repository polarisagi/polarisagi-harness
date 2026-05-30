package native

import (
	"database/sql"
	"fmt"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/internal/protocol"
	"github.com/polarisagi/polarisagi-harness/pkg/action"
	"github.com/polarisagi/polarisagi-harness/pkg/action/tool"
	"github.com/polarisagi/polarisagi-harness/pkg/extensions/marketplace"
	"github.com/polarisagi/polarisagi-harness/pkg/extensions/mcp"
)

// RegisterExtensionTools 注册原生的 L2 扩展工具（GUI/浏览器/市场管理）。
func RegisterExtensionTools(
	sandbox *action.InProcessSandbox,
	toolReg *tool.InMemoryToolRegistry,
	mcpManager *mcp.MCPManager,
	db *sql.DB,
	marketplaceClient *marketplace.MCPMarketplaceClient,
	installMgr *marketplace.Manager,
	hitlGateway protocol.HITL,
) error {
	tools := []struct {
		meta protocol.Tool
		fn   action.InProcessFn
	}{

		{
			meta: protocol.Tool{
				Name:        "search_extension",
				Description: "从官方扩展云端市场搜索插件、技能或 MCP 服务器",
				Version:     "1.0.0",
				Capability:  protocol.CapWriteNetwork,
				SideEffects: []protocol.SideEffect{protocol.SideNetworkCall},
				RiskLevel:   protocol.RiskLow,
				SandboxTier: protocol.SandboxInProcess,
				Source:      protocol.ToolBuiltin,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "搜索关键词（如 git, browser, notion）"},
					},
					"required": []string{"query"},
				},
			},
			fn: MakeExtensionSearchFn(db, marketplaceClient),
		},
		{
			meta: protocol.Tool{
				Name:        "install_extension",
				Description: "从官方扩展市场下载并安装指定的插件/扩展包（ID 需从 search_extension 结果中获取）",
				Version:     "1.0.0",
				Capability:  protocol.CapWriteLocal,
				SideEffects: []protocol.SideEffect{protocol.SideNetworkCall, protocol.SideFileWrite},
				RiskLevel:   protocol.RiskHigh,
				SandboxTier: protocol.SandboxInProcess,
				Source:      protocol.ToolBuiltin,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "description": "插件包的唯一 ID"},
					},
					"required": []string{"id"},
				},
			},
			fn: MakeExtensionInstallFn(marketplaceClient, installMgr, hitlGateway),
		},
	}

	for _, t := range tools {
		sandbox.Register(t.meta.Name, t.fn)
		if err := toolReg.Register(t.meta); err != nil {
			return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("extension_tools: register %q", t.meta.Name), err)
		}
	}

	return nil
}
