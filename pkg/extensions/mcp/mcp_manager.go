package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/pkg/action"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// MCPServerInfo MCP Server 运行时状态快照。
type MCPServerInfo struct {
	ID        string
	Name      string
	Transport string
	Connected bool
	Tools     []MCPTool
	Error     string
}

type mcpEntry struct {
	client *MCPClient
	name   string
	cfg    MCPClientConfig
	tools  []MCPTool
	errMsg string
}

// MCPManager 管理所有 MCP Server 连接，动态注册工具到 InProcessSandbox。
type MCPManager struct {
	mu         sync.RWMutex
	entries    map[string]*mcpEntry
	sandbox    *action.InProcessSandbox
	httpClient *http.Client
	policy     protocol.PolicyGate // 对 CallTool 直接路径执行安全检查
}

func NewMCPManager(sandbox *action.InProcessSandbox, httpClient *http.Client, policy protocol.PolicyGate) *MCPManager {
	return &MCPManager{
		entries:    make(map[string]*mcpEntry),
		sandbox:    sandbox,
		httpClient: httpClient,
		policy:     policy,
	}
}

// Add 连接一个 MCP Server，发现工具并注册到 sandbox。
// 连接失败时仍写入 tombstone entry（errMsg 非空），使 ListServers 能向 UI 暴露失败原因。
// name 必须满足 ^[a-zA-Z0-9_-]+$，否则拒绝安装。
func (m *MCPManager) Add(ctx context.Context, serverID, name string, cfg MCPClientConfig) error {
	if err := validateLLMNamePart(name); err != nil {
		return perrors.Wrap(perrors.CodeInvalidInput, fmt.Sprintf("mcp: server name %q invalid (must match ^[a-zA-Z0-9_-]+$)", name), err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.entries[serverID]; ok {
		if old.client != nil {
			old.client.Close()
		}
		m.unregisterTools(old.name, old.tools)
	}

	storeFailed := func(err error) error {
		m.entries[serverID] = &mcpEntry{name: name, cfg: cfg, errMsg: err.Error()}
		return err
	}

	client := NewMCPClient(cfg, m.httpClient)
	if err := client.Connect(ctx); err != nil {
		wrapped := perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: connect %q", serverID), err)
		return storeFailed(wrapped)
	}
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		wrapped := perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: initialize %q", serverID), err)
		return storeFailed(wrapped)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		wrapped := perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: list tools %q", serverID), err)
		return storeFailed(wrapped)
	}

	validTools := m.registerTools(name, client, tools)
	m.entries[serverID] = &mcpEntry{
		client: client,
		name:   name,
		cfg:    cfg,
		tools:  validTools,
	}
	slog.Info("mcp_manager: server connected", "id", serverID, "tools", len(tools))
	return nil
}

// Remove 断开并移除 MCP Server，取消注册其所有工具。
func (m *MCPManager) Remove(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[serverID]; ok {
		if e.client != nil {
			e.client.Close()
		}
		m.unregisterTools(e.name, e.tools)
		delete(m.entries, serverID)
	}
}

// CallTool 直接路由调用指定的 MCP 工具。
// 执行 PolicyGate 校验（与 InMemoryToolRegistry.ExecuteTool 语义一致）。
func (m *MCPManager) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	e, ok := m.entries[serverID]
	m.mu.RUnlock()
	if !ok {
		return "", perrors.New(perrors.CodeInternal, "mcp_manager: server not found: "+serverID)
	}

	// PolicyGate: deny-by-default，与 ToolRegistry 路径保持一致
	if m.policy != nil {
		tl := 2 // MCP Server 信任等级（社区级别）
		if e.cfg.Trusted {
			tl = 3 // 白名单 MCP Server 提升信任等级
		}
		allowed, pErr := m.policy.IsAuthorized(ctx, "agent", "tool_execute",
			serverID+":"+toolName,
			map[string]any{
				"tool_source": "mcp",
				"trust_level": tl,
			})
		if pErr != nil || !allowed {
			reason := "policy denied"
			if pErr != nil {
				reason = pErr.Error()
			}
			return "", perrors.New(perrors.CodeForbidden, fmt.Sprintf("mcp_manager: policy blocked %s: %s", toolName, reason))
		}
	}

	text, _, _, err := e.client.CallToolTainted(ctx, toolName, args)
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "mcp_manager: call tool", err)
	}
	return text, err
}

// ListServers 返回所有 MCP Server 的运行时状态快照。
func (m *MCPManager) ListServers() []MCPServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]MCPServerInfo, 0, len(m.entries))
	for id, e := range m.entries {
		result = append(result, MCPServerInfo{
			ID:        id,
			Name:      e.name,
			Transport: string(e.cfg.Transport),
			Connected: e.errMsg == "",
			Tools:     e.tools,
			Error:     e.errMsg,
		})
	}
	return result
}

// ListToolSchemas 返回所有已连接 MCP 工具的 ToolSchema，用于注入 InferRequest。
func (m *MCPManager) ListToolSchemas() []protocol.ToolSchema {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []protocol.ToolSchema
	for _, e := range m.entries {
		for _, t := range e.tools {
			var schema any
			json.Unmarshal(t.InputSchema, &schema) //nolint:errcheck
			result = append(result, protocol.ToolSchema{
				Name:        mcpToolName(e.name, t.Name),
				Description: t.Description,
				Parameters:  schema,
			})
		}
	}
	return result
}

// LoadFromDB 启动时从数据库加载并异步连接所有已启用的 MCP Server。
// dataDir 用于展开 args/url 中的 {DATA_DIR} 占位符。
func (m *MCPManager) LoadFromDB(ctx context.Context, db *sql.DB, dataDir string) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, transport, command, args, env, url, timeout, trust_tier FROM mcp_servers WHERE enabled=1`)
	if err != nil {
		slog.Error("mcp_manager: load from db", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, transport, command, argsJSON, envJSON, urlStr string
		var timeout, trustTier int
		if err := rows.Scan(&id, &name, &transport, &command, &argsJSON, &envJSON, &urlStr, &timeout, &trustTier); err != nil {
			continue
		}
		var args []string
		json.Unmarshal([]byte(argsJSON), &args) //nolint:errcheck
		var env map[string]string
		json.Unmarshal([]byte(envJSON), &env) //nolint:errcheck

		for i, a := range args {
			args[i] = strings.ReplaceAll(a, "{DATA_DIR}", dataDir)
		}

		resolvedURL := strings.ReplaceAll(urlStr, "{DATA_DIR}", dataDir)
		// "streamable_http" 是数据库存储值，兼容 Claude Code 的 "streamable-http" 别名
		if transport == "streamable-http" {
			transport = string(MCPStreamableHTTP)
		}
		cfg := MCPClientConfig{
			Transport:  MCPTransport(transport),
			Command:    command,
			Args:       args,
			Env:        env,
			URL:        resolvedURL,
			Timeout:    time.Duration(timeout) * time.Second,
			ServerName: name,
			// trust_tier >= 3 (Official/System) → TaintMedium；其余保持 TaintHigh
			Trusted: trustTier >= 3,
		}
		// 每个 server 独立 goroutine，避免一个慢连接阻塞其他
		go func(id, name string, cfg MCPClientConfig) {
			connCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := m.Add(connCtx, id, name, cfg); err != nil {
				slog.Error("mcp_manager: load server failed", "id", id, "err", err)
			}
		}(id, name, cfg)
	}
}

// LoadFromPlugins 从已安装插件加载子 MCP Server。
// 插件内的 MCP 不写 mcp_servers 全局表，通过此方法在启动时动态读取 install_path/.mcp.json。
// 与 LoadFromDB 配合：LoadFromDB 加载独立安装的 MCP，LoadFromPlugins 加载插件内嵌的 MCP。
func (m *MCPManager) LoadFromPlugins(ctx context.Context, db *sql.DB, dataDir string) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, install_path, mcp_policy, trust_tier FROM plugins WHERE enabled=1 AND install_path != ''`)
	if err != nil {
		slog.Error("mcp_manager: load_from_plugins query failed", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var pluginID, pluginName, installPath, mcpPolicyJSON string
		var trustTier int
		if err := rows.Scan(&pluginID, &pluginName, &installPath, &mcpPolicyJSON, &trustTier); err != nil {
			continue
		}
		m.LoadOnePlugin(ctx, pluginID, pluginName, installPath, mcpPolicyJSON, trustTier, dataDir)
	}
}

// LoadOnePlugin 加载单个插件的子 MCP Server，供插件启用/MCP 策略变更时按需调用。
func (m *MCPManager) LoadOnePlugin(ctx context.Context, pluginID, pluginName, installPath, mcpPolicyJSON string, trustTier int, dataDir string) {
	var mcpPolicy map[string]map[string]any
	_ = json.Unmarshal([]byte(mcpPolicyJSON), &mcpPolicy)

	type mcpServerEntry struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
	}
	type mcpFile struct {
		MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	}

	mcpData, err := readFileBytes(installPath + "/.mcp.json")
	if err != nil {
		slog.Debug("mcp_manager: plugin has no .mcp.json", "plugin", pluginName)
		return
	}
	var fileCfg mcpFile
	if err := json.Unmarshal(mcpData, &fileCfg); err != nil {
		slog.Warn("mcp_manager: failed to parse plugin .mcp.json", "plugin", pluginName, "err", err)
		return
	}

	for serverName, def := range fileCfg.MCPServers {
		if policy, ok := mcpPolicy[serverName]; ok {
			if enabled, ok := policy["enabled"].(bool); ok && !enabled {
				slog.Info("mcp_manager: plugin mcp disabled by policy", "plugin", pluginName, "server", serverName)
				continue
			}
		}

		args := make([]string, len(def.Args))
		for i, a := range def.Args {
			args[i] = strings.ReplaceAll(a, "{DATA_DIR}", dataDir)
		}

		transport := MCPStdio
		resolvedURL := strings.ReplaceAll(def.URL, "{DATA_DIR}", dataDir)
		if resolvedURL != "" {
			transport = MCPStreamableHTTP
		}

		serverID := fmt.Sprintf("plugin_%s_%s", pluginID, serverName)
		scopedName := pluginName + "-" + serverName

		cfg := MCPClientConfig{
			Transport:  transport,
			Command:    def.Command,
			Args:       args,
			Env:        def.Env,
			URL:        resolvedURL,
			Timeout:    30 * time.Second,
			ServerName: scopedName,
			Trusted:    trustTier >= 3,
		}

		go func(id, name string, cfg MCPClientConfig) {
			connCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := m.Add(connCtx, id, name, cfg); err != nil {
				slog.Error("mcp_manager: load plugin mcp failed", "plugin", pluginName, "server", serverName, "err", err)
			}
		}(serverID, scopedName, cfg)
	}
}

// readFileBytes 读取文件内容，供包内使用。
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// maxLLMToolNameLen 是 OpenAI 兼容接口对 function.name 的最大长度限制。
const maxLLMToolNameLen = 64

// registerTools 注册合法的 MCP 工具到 sandbox，返回实际注册成功的工具子集。
// 服务器名（serverName）在 Add() 中已经过 validateLLMNamePart 校验，此处信任。
// 工具名来自外部 MCP 服务器，不可控：非法字符静默替换并记录警告；超长则跳过。
func (m *MCPManager) registerTools(serverName string, client *MCPClient, tools []MCPTool) []MCPTool {
	// 确定此 server 的污点等级：白名单 → TaintMedium；其余 → TaintHigh
	taint := protocol.TaintHigh
	if client.cfg.Trusted {
		taint = protocol.TaintMedium
	}
	valid := make([]MCPTool, 0, len(tools))
	for _, t := range tools {
		llmName := mcpToolName(serverName, t.Name)
		if llmName != mcpToolName(serverName, sanitizeToolNamePart(t.Name)) || t.Name != sanitizeToolNamePart(t.Name) {
			slog.Warn("mcp: tool name sanitized", "server", serverName, "original", t.Name, "llm_name", llmName)
		}
		if len(llmName) > maxLLMToolNameLen {
			slog.Warn("mcp: tool LLM name too long, skipped", "server", serverName, "tool", t.Name, "llm_name", llmName, "max", maxLLMToolNameLen)
			continue
		}
		fn := makeMCPToolFn(client, t.Name)
		// RegisterRich 将 MCP 工具注册到富工具路径（支持 ImageParts 回传）
		m.sandbox.RegisterRich(llmName, fn, taint)
		valid = append(valid, t)
	}
	return valid
}

// makeMCPToolFn 创建调用 MCP 工具的富执行函数。
// 返回完整 ToolResult（含 ImageParts），使用 CallToolTainted 进行污点保护反序列化（M07 §1 安全要求）。
func makeMCPToolFn(client *MCPClient, mcpName string) action.InProcessRichFn {
	return func(ctx context.Context, input []byte) (*protocol.ToolResult, error) {
		var args map[string]any
		if len(input) > 0 {
			json.Unmarshal(input, &args) //nolint:errcheck
		}
		// CallToolTainted 内部执行 TaintPreservingDecoder，taint 通过 RegisterRich 传递
		text, imgs, _, err := client.CallToolTainted(ctx, mcpName, args)
		if err != nil {
			return nil, err
		}
		return &protocol.ToolResult{
			Success:    true,
			Output:     []byte(text),
			ImageParts: imgs, // MCP type="image" content block 解析结果
		}, nil
	}
}

func (m *MCPManager) unregisterTools(serverName string, tools []MCPTool) {
	for _, t := range tools {
		m.sandbox.Unregister(mcpToolName(serverName, t.Name))
	}
}

// mcpToolName 生成 LLM 工具名，格式：mcp__<serverName>__<toolName>。
// serverName 由调用方（Add）保证合法；toolName 来自外部，经 sanitizeToolNamePart 处理。
func mcpToolName(serverName, toolName string) string {
	return "mcp__" + serverName + "__" + sanitizeToolNamePart(toolName)
}

// sanitizeToolNamePart 将外部工具名中不符合 ^[a-zA-Z0-9_-]+$ 的字符替换为下划线。
// 仅用于来自外部 MCP 服务器的工具名；用户配置的服务器名走 validateLLMNamePart 硬校验。
func sanitizeToolNamePart(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// validateLLMNamePart 校验字符串是否满足 OpenAI 工具名规范 ^[a-zA-Z0-9_-]+$。
// 用于用户可控的名称（MCP server name、skill name），非法则快速失败。
func validateLLMNamePart(s string) error {
	if s == "" {
		return perrors.New(perrors.CodeInvalidInput, "name must not be empty")
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return perrors.New(perrors.CodeInvalidInput, fmt.Sprintf("char %q not in ^[a-zA-Z0-9_-]+$", r))
		}
	}
	return nil
}
