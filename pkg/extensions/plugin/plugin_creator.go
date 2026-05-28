package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
)

// LLMClient is a minimal interface for the PluginCreator to generate responses.
type LLMClient interface {
	// Generate uses the system prompt and user intent to generate a structured response.
	Generate(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// PluginCreator defines the auto-generation workflow for MCP plugins based on user intent.
type PluginCreator struct {
	llm     LLMClient
	baseDir string // e.g. ~/.polaris-harness/extensions/local/
}

// NewPluginCreator initializes a new creator for auto-generating plugins.
func NewPluginCreator(llm LLMClient, baseDir string) *PluginCreator {
	return &PluginCreator{
		llm:     llm,
		baseDir: baseDir,
	}
}

// GeneratedPlugin represents the structured output expected from the LLM.
type GeneratedPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PythonCode  string `json:"python_code"`
}

const pluginCreatorSystemPrompt = `
You are the internal plugin-creator agent. Your job is to translate a user's intent into a fully functional Anthropic MCP (Model Context Protocol) plugin using Python.
A plugin MUST have a concise name (kebab-case) and a clear description.
You must use the 'mcp' Python package, specifically 'from mcp.server.fastmcp import FastMCP' to define the server.

Output ONLY valid JSON matching this schema:
{
  "name": "plugin-name",
  "description": "What this plugin does...",
  "python_code": "import asyncio\nfrom mcp.server.fastmcp import FastMCP\n\nmcp = FastMCP(\"plugin-name\")\n\n@mcp.tool()\ndef my_tool() -> str:\n    return \"Done\"\n\nif __name__ == \"__main__\":\n    mcp.run(transport='stdio')\n"
}
Do not include any Markdown wrappers like ` + "```json" + ` in the output. Ensure the python code is properly escaped in the JSON string.
`

// GeneratePlugin takes a user's intent, calls the LLM, and creates the physical plugin directory, .mcp.json, and server.py.
func (c *PluginCreator) GeneratePlugin(ctx context.Context, intent string) (string, error) {
	if c.llm == nil {
		return "", perrors.New(perrors.CodeInternal, "plugin_creator: LLM client is nil")
	}

	response, err := c.llm.Generate(ctx, pluginCreatorSystemPrompt, intent)
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to generate plugin", err)
	}

	// Simple JSON extraction to handle model quirks
	jsonStr := extractJSON(response)

	var result GeneratedPlugin
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to parse generated plugin JSON", err)
	}

	if result.Name == "" || result.Description == "" || result.PythonCode == "" {
		return "", perrors.New(perrors.CodeInternal, "plugin_creator: invalid generation, missing required fields")
	}

	// Create physical directory structure
	pluginDir := filepath.Join(c.baseDir, result.Name)

	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to create plugin directory", err)
	}

	// Write server.py
	serverPath := filepath.Join(pluginDir, "server.py")
	if err := os.WriteFile(serverPath, []byte(result.PythonCode), 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to write server.py", err)
	}

	// Create a default plugin.json
	pluginMetaDir := filepath.Join(pluginDir, ".codex-plugin")
	if err := os.MkdirAll(pluginMetaDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to create .codex-plugin directory", err)
	}

	pluginJSON := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "description": "%s",
  "mcpServers": "./.mcp.json"
}`, result.Name, result.Description)

	pluginJSONPath := filepath.Join(pluginMetaDir, "plugin.json")
	if err := os.WriteFile(pluginJSONPath, []byte(pluginJSON), 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to write plugin.json", err)
	}

	// Create .mcp.json using uvx to run the server
	mcpJSON := fmt.Sprintf(`{
  "mcpServers": {
    "%s": {
      "command": "uvx",
      "args": ["run", "server.py"]
    }
  }
}`, result.Name)

	mcpJSONPath := filepath.Join(pluginDir, ".mcp.json")
	if err := os.WriteFile(mcpJSONPath, []byte(mcpJSON), 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "plugin_creator: failed to write .mcp.json", err)
	}

	return pluginDir, nil
}

func extractJSON(input string) string {
	re := regexp.MustCompile(`(?s)\{.*\}`)
	match := re.FindString(input)
	if match != "" {
		return match
	}
	return input
}
