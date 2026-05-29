package protocol

// ============================================================================
// M7 Extensions — Plugin, Skill, MCP, Marketplace 模型
// ============================================================================

// RegistryEntry 插件目录条目（ADR-0016：Publisher/TrustTier/Type 字段）。
type RegistryEntry struct {
	// ID 全局唯一 slug，格式："{publisher}/{name}" 或 "mcp/{name}"
	ID        string `json:"id" yaml:"id"`
	Publisher string `json:"publisher" yaml:"publisher"`
	// Type "mcp" | "skill" | "plugin" | "app"
	Type      string `json:"type" yaml:"type"`
	TrustTier int    `json:"trust_tier" yaml:"trust_tier"`

	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Transport   string            `json:"transport,omitempty" yaml:"transport,omitempty"`
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args        []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	URL         string            `json:"url,omitempty" yaml:"url,omitempty"`
	Tags        []string          `json:"tags" yaml:"tags"`
	Homepage    string            `json:"homepage,omitempty" yaml:"homepage,omitempty"`
	Timeout     int               `json:"timeout" yaml:"timeout"`
	// 运行时叠加：是否已安装（extension_instances 表中存在同 catalog_id）
	Installed bool `json:"installed" yaml:"installed"`
}

// Marketplace 市场配置。
type Marketplace struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Publisher   string `json:"publisher" yaml:"publisher"`
	RepoURL     string `json:"repo_url" yaml:"repo_url"`
	Description string `json:"description" yaml:"description"`
	IsBuiltin   int    `json:"is_builtin" yaml:"is_builtin"`
	TrustTier   int    `json:"trust_tier" yaml:"trust_tier"`
	Enabled     int    `json:"enabled" yaml:"enabled"`
	CreatedAt   string `json:"created_at" yaml:"created_at"`
}

// PluginJSON 表示 .codex-plugin/plugin.json 中的信息
type PluginJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	MCPServers  string `json:"mcpServers,omitempty"`
}

// MCPServerDef 定义单个 MCP Server 配置。
// Type 对应 Claude Code .mcp.json 的 "type" 字段：
//   - "stdio"（默认）: 本地进程，使用 Command/Args/Env
//   - "http" / "streamable-http": 远端 HTTP，使用 URL/Headers
//   - "sse": 已废弃（Anthropic 2026-04-01 停止接受），仍保留兼容
type MCPServerDef struct {
	Type    string            `json:"type,omitempty"`    // "stdio"|"http"|"streamable-http"|"sse"
	Command string            `json:"command,omitempty"` // stdio 专用
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`     // http/sse 专用
	Headers map[string]string `json:"headers,omitempty"` // http/sse 专用，Bearer 等
}

// MCPConfig 表示 .mcp.json 的结构
type MCPConfig struct {
	MCPServers map[string]MCPServerDef `json:"mcpServers"`
}

// PluginBundleManifest 是多组件 Bundle 的扩展 plugin.json（M13-bis §2.1）。
// MCPFile 字段与 PluginJSON.MCPServers 同名，向下兼容已有单 MCP 插件格式。
type PluginBundleManifest struct {
	Name        string                  `json:"name"`
	Version     string                  `json:"version"`
	Description string                  `json:"description"`
	Entrypoint  string                  `json:"entrypoint,omitempty"`
	MCPFile     string                  `json:"mcpServers,omitempty"` // 指向 .mcp.json 的相对路径
	MCPInline   map[string]MCPServerDef `json:"mcp_inline,omitempty"`  // 内联 MCP 服务器映射
	Skills      []BundleSkillRef        `json:"skills,omitempty"`
	Hooks       map[string]string       `json:"hooks,omitempty"` // 事件 → 脚本路径
}

// BundleSkillRef 引用 Bundle 内的单个技能。
type BundleSkillRef struct {
	Path string `json:"path"` // 相对于 Bundle 根目录的 SKILL.md 路径
	Name string `json:"name,omitempty"`
}

// AIPluginJSON 是 OpenAI ai-plugin.json 清单格式。
// https://platform.openai.com/docs/plugins/getting-started/plugin-manifest
type AIPluginJSON struct {
	SchemaVersion       string `json:"schema_version"`
	NameForModel        string `json:"name_for_model"`
	NameForHuman        string `json:"name_for_human"`
	DescriptionForModel string `json:"description_for_model"`
	DescriptionForHuman string `json:"description_for_human"`
	Auth                struct {
		Type string `json:"type"` // "none" | "service_http" | "user_http" | "oauth"
	} `json:"auth"`
	API struct {
		Type string `json:"type"` // "openapi" | "mcp"
		URL  string `json:"url"`
	} `json:"api"`
	LogoURL      string `json:"logo_url"`
	ContactEmail string `json:"contact_email"`
	LegalInfoURL string `json:"legal_info_url"`
}

// AnthropicPluginTOML 是 Anthropic .claude-plugin/plugin.toml 格式。
// struct tag `toml:` 由 go-toml/v2 反射读取，无需在 protocol 包导入 toml 库。
type AnthropicPluginTOML struct {
	Plugin struct {
		Name        string `toml:"name"`
		Description string `toml:"description"`
		Version     string `toml:"version"`
	} `toml:"plugin"`
	MCP struct {
		Command string            `toml:"command"`
		Args    []string          `toml:"args"`
		Env     map[string]string `toml:"env"`
	} `toml:"mcp"`
}

// GoogleSkillsYAML 是 Google Agent Skills manifest 格式（skills.yaml / agent-manifest.yaml）。
type GoogleSkillsYAML struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Version     string   `yaml:"version"`
	Command     string   `yaml:"command,omitempty"`
	Args        []string `yaml:"args,omitempty"`
	Skills      []struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Command     string   `yaml:"command,omitempty"`
		Args        []string `yaml:"args,omitempty"`
	} `yaml:"skills,omitempty"`
}

// PluginInstallRequest 一键安装请求体。
type PluginInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	// 可选覆盖：不传则使用 catalog 默认值
	Name    string            `json:"name,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}
