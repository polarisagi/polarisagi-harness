// Package plugin 实现 Plugin Registry（P1 特性：技能+MCP 打包分发单元）。
// Plugin = SKILL.md 列表 + MCP Server 配置的聚合 bundle。
// 加载后：Skills → M6 SkillRegistry，MCP → M7 MCPManager。
// 参见 ADR-0015 §2.1。
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"

	"gopkg.in/yaml.v3"
)

// MCPServerDef Plugin 内嵌的 MCP Server 配置。
// 字段子集映射到 MCPClientConfig（M7 mcp_client.go）。
type MCPServerDef struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"` // stdio | streamable_http
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Trusted   bool              `yaml:"trusted,omitempty"`
}

// Manifest Plugin 清单文件（plugin.yaml）。
type Manifest struct {
	Name        string         `yaml:"name"`
	Version     string         `yaml:"version"`
	Description string         `yaml:"description"`
	Skills      []string       `yaml:"skills"` // SKILL.md 相对/绝对路径
	MCPServers  []MCPServerDef `yaml:"mcp_servers"`
}

// Plugin 运行时 Plugin 实例（已加载的清单 + 来源路径）。
type Plugin struct {
	Manifest Manifest
	Dir      string // plugin.yaml 所在目录（技能路径相对此解析）
	Enabled  bool
}

// AbsSkillPaths 返回所有技能文件的绝对路径。
func (p *Plugin) AbsSkillPaths() []string {
	paths := make([]string, 0, len(p.Manifest.Skills))
	for _, rel := range p.Manifest.Skills {
		if filepath.IsAbs(rel) {
			paths = append(paths, rel)
		} else {
			paths = append(paths, filepath.Join(p.Dir, rel))
		}
	}
	return paths
}

// ParseManifest 解析 plugin.yaml 文件。
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("plugin: read manifest %s: %v", path, err), err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("plugin: parse manifest %s: %v", path, err), err)
	}
	if err := validateManifest(&m, path); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateManifest(m *Manifest, path string) error {
	if strings.TrimSpace(m.Name) == "" {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("plugin: manifest %s missing name", path))
	}
	if strings.TrimSpace(m.Version) == "" {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("plugin: manifest %s missing version", path))
	}
	return nil
}
