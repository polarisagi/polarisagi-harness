// Package plugin 实现 Plugin Registry（P1 特性：技能+MCP 打包分发单元）。
// Plugin = SKILL.md 列表 + MCP Server 配置的聚合 bundle。
// 加载后：Skills → M6 SkillRegistry，MCP → M7 MCPManager。
// 参见 ADR-0015 §2.1。
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// MCPServerDef 映射 .mcp.json 中的单个 server 定义。
type MCPServerDef struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"` // 用于 SSE transport
}

// MCPConfig 映射 .mcp.json 整个文件
type MCPConfig struct {
	MCPServers map[string]MCPServerDef `json:"mcpServers"`
}

// PluginJSON 映射 .codex-plugin/plugin.json 清单文件。
type PluginJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Skills      string `json:"skills,omitempty"`     // 相对路径，如 "./skills/"
	MCPServers  string `json:"mcpServers,omitempty"` // 相对路径，如 "./.mcp.json"
	Apps        string `json:"apps,omitempty"`       // 相对路径，如 "./.app.json"
	Hooks       string `json:"hooks,omitempty"`      // 相对路径，如 "./hooks/hooks.json"
}

// Plugin 运行时 Plugin 实例。
type Plugin struct {
	Manifest PluginJSON
	MCPs     map[string]MCPServerDef // 已解析的 MCP Servers 列表
	Dir      string                  // 插件的根目录路径
	Enabled  bool
}

// ParseManifest 解析 plugin.json 文件。
func ParseManifest(path string) (*PluginJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("plugin: read manifest %s: %v", path, err), err)
	}
	var m PluginJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("plugin: parse manifest %s: %v", path, err), err)
	}
	if err := validateManifest(&m, path); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateManifest(m *PluginJSON, path string) error {
	if strings.TrimSpace(m.Name) == "" {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("plugin: manifest %s missing name", path))
	}
	if strings.TrimSpace(m.Version) == "" {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("plugin: manifest %s missing version", path))
	}
	return nil
}
