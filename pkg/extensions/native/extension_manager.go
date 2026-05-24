package native

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/extensions/marketplace"
)

// makeExtensionManagerFn creates a builtin tool function for managing extensions.
// It allows the LLM to search for and install official extensions from the cloud registry.
func makeExtensionManagerFn(marketplaceClient *marketplace.MCPMarketplaceClient) protocol.ToolExecutorFn {
	return func(ctx context.Context, argsBytes []byte) (protocol.ToolResult, error) {
		// Due to the simplicity of this example, we mock the parsing.
		// In a real implementation, this would parse a struct matching the tool's input schema.
		// Currently returning a placeholder error to signify it needs to be fully wired up
		// to the protocol definitions.
		slog.Info("native: extension_manager invoked", "args", string(argsBytes))
		return protocol.ToolResult{}, fmt.Errorf("native: extension_manager argument parser not yet fully implemented")
	}
}

// TODO: Export a registration function that registers 'search_extension' and 'install_extension'
// into the ToolRegistry using makeExtensionManagerFn.
