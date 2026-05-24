package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
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

// SearchResult represents a package found in the marketplace.
type SearchResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Publisher   string   `json:"publisher"`
	InstallCmd  string   `json:"install_cmd"`
	Args        []string `json:"args"`
}

// Search queries the marketplace for MCP servers or plugins.
func (c *MCPMarketplaceClient) Search(ctx context.Context, query string) ([]SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s", c.registryURL, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
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

	var results []SearchResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "marketplace: failed to parse response", err)
	}

	return results, nil
}

// Install auto-configures the downloaded MCP server into a local plugin layout.
func (c *MCPMarketplaceClient) Install(ctx context.Context, pkg SearchResult) (string, error) {
	if pkg.InstallCmd == "" {
		return "", perrors.New(perrors.CodeInternal, "marketplace: package missing install command")
	}

	pluginDir := filepath.Join(c.baseInstallDir, pkg.ID)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to create directory", err)
	}

	// Generate .mcp.json
	mcpConfig := MCPConfig{
		MCPServers: map[string]MCPServerDef{
			pkg.ID: {
				Command: pkg.InstallCmd,
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

	manifest := PluginJSON{
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
		return "", perrors.Wrap(perrors.CodeInternal, "marketplace: failed to write plugin.json", err)
	}

	return pluginDir, nil
}
