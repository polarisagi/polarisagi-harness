package marketplace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"log/slog"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// MCPMarketplaceClient handles interactions with external MCP registries.
type MCPMarketplaceClient struct {
	httpClient     *http.Client
	registryURL    string
	baseInstallDir string
}

// NewMCPMarketplaceClient creates a new client. Default registry is registry.modelcontextprotocol.io
func NewMCPMarketplaceClient(registryURL, baseInstallDir string) *MCPMarketplaceClient {
	if registryURL == "" {
		registryURL = "https://registry.modelcontextprotocol.io/api" // mock standard API path
	}
	return &MCPMarketplaceClient{
		httpClient:     &http.Client{},
		registryURL:    registryURL,
		baseInstallDir: baseInstallDir,
	}
}

// Search queries the marketplace for MCP servers or plugins.
func (c *MCPMarketplaceClient) Search(ctx context.Context, query string) ([]protocol.RegistryEntry, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s", c.registryURL, url.QueryEscape(query))
	slog.Info("marketplace: searching for packages", "query", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		slog.Error("marketplace: invalid search request", "err", err)
		return nil, perrors.Wrap(perrors.CodeInternal, "marketplace: invalid search request", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "marketplace: search failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("marketplace: search returned %d", resp.StatusCode))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "marketplace: failed to read response", err)
	}

	var results []protocol.RegistryEntry
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "marketplace: failed to parse response", err)
	}

	return results, nil
}

// Install auto-configures the downloaded MCP server into a local plugin layout.
func (c *MCPMarketplaceClient) Install(ctx context.Context, pkg protocol.RegistryEntry) (string, error) {
	if pkg.Command == "" {
		return "", perrors.New(perrors.CodeInternal, "marketplace: package missing install command")
	}

	pluginDir := filepath.Join(c.baseInstallDir, pkg.ID)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to create directory", err)
	}

	// Generate .mcp.json
	mcpConfig := protocol.MCPConfig{
		MCPServers: map[string]protocol.MCPServerDef{
			pkg.ID: {
				Command: pkg.Command,
				Args:    pkg.Args,
			},
		},
	}

	mcpData, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: marshal mcp.json failed", err)
	}

	mcpPath := filepath.Join(pluginDir, ".mcp.json")
	if err := os.WriteFile(mcpPath, mcpData, 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to write .mcp.json", err)
	}

	// Generate .codex-plugin/plugin.json
	pluginMetaDir := filepath.Join(pluginDir, ".codex-plugin")
	if err := os.MkdirAll(pluginMetaDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to create .codex-plugin directory", err)
	}

	manifest := protocol.PluginJSON{
		Name:        pkg.Name,
		Version:     "1.0.0", // from market
		Description: pkg.Description,
		MCPServers:  "./.mcp.json",
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: marshal plugin.json failed", err)
	}

	manifestPath := filepath.Join(pluginMetaDir, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		slog.Error("marketplace: failed to write plugin.json", "err", err)
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to write plugin.json", err)
	}

	slog.Info("marketplace: install success", "pkg_id", pkg.ID, "dir", pluginDir)
	return pluginDir, nil
}
