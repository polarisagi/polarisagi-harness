package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/pkg/action"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
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
}

func NewMCPManager(sandbox *action.InProcessSandbox, httpClient *http.Client) *MCPManager {
	return &MCPManager{
		entries:    make(map[string]*mcpEntry),
		sandbox:    sandbox,
		httpClient: httpClient,
	}
}

// Add 连接一个 MCP Server，发现工具并注册到 sandbox。
func (m *MCPManager) Add(ctx context.Context, serverID, name string, cfg MCPClientConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.entries[serverID]; ok {
		old.client.Close()
		m.unregisterTools(serverID, old.tools)
	}

	client := NewMCPClient(cfg, m.httpClient)
	if err := client.Connect(ctx); err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: connect %q: %v", serverID, err), err)
	}
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: initialize %q: %v", serverID, err), err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp_manager: list tools %q: %v", serverID, err), err)
	}

	m.registerTools(serverID, client, tools)
	m.entries[serverID] = &mcpEntry{
		client: client,
		name:   name,
		cfg:    cfg,
		tools:  tools,
	}
	slog.Info("mcp_manager: server connected", "id", serverID, "tools", len(tools))
	return nil
}

// Remove 断开并移除 MCP Server，取消注册其所有工具。
func (m *MCPManager) Remove(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[serverID]; ok {
		e.client.Close()
		m.unregisterTools(serverID, e.tools)
		delete(m.entries, serverID)
	}
}

// CallTool 直接路由调用指定的 MCP 工具。
func (m *MCPManager) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	e, ok := m.entries[serverID]
	m.mu.RUnlock()
	if !ok {
		return "", perrors.New(perrors.CodeInternal, "mcp_manager: server not found: "+serverID)
	}
	text, _, err := e.client.CallToolTainted(ctx, toolName, args)
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
	for serverID, e := range m.entries {
		for _, t := range e.tools {
			var schema any
			json.Unmarshal(t.InputSchema, &schema) //nolint:errcheck
			result = append(result, protocol.ToolSchema{
				Name:        mcpToolName(serverID, t.Name),
				Description: t.Description,
				Parameters:  schema,
			})
		}
	}
	return result
}

// LoadFromDB 启动时从数据库加载并异步连接所有已启用的 MCP Server。
func (m *MCPManager) LoadFromDB(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, transport, command, args, env, url, timeout FROM mcp_servers WHERE enabled=1`)
	if err != nil {
		slog.Error("mcp_manager: load from db", "err", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, transport, command, argsJSON, envJSON, urlStr string
		var timeout int
		if err := rows.Scan(&id, &name, &transport, &command, &argsJSON, &envJSON, &urlStr, &timeout); err != nil {
			continue
		}
		var args []string
		json.Unmarshal([]byte(argsJSON), &args) //nolint:errcheck
		var env map[string]string
		json.Unmarshal([]byte(envJSON), &env) //nolint:errcheck

		cfg := MCPClientConfig{
			Transport: MCPTransport(transport),
			Command:   command,
			Args:      args,
			Env:       env,
			URL:       urlStr,
			Timeout:   time.Duration(timeout) * time.Second,
		}
		// 每个 server 独立 goroutine，避免一个慢连接阻塞其他
		go func(id, name string, cfg MCPClientConfig) {
			connCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := m.Add(connCtx, id, name, cfg); err != nil {
				slog.Warn("mcp_manager: load server failed", "id", id, "err", err)
			}
		}(id, name, cfg)
	}
}

func (m *MCPManager) registerTools(serverID string, client *MCPClient, tools []MCPTool) {
	// 确定此 server 的污点等级：白名单 → TaintMedium；其余 → TaintHigh
	taint := protocol.TaintHigh
	if client.cfg.Trusted {
		taint = protocol.TaintMedium
	}
	for _, t := range tools {
		toolName := mcpToolName(serverID, t.Name)
		mcpName := t.Name
		fn := makeMCPToolFn(client, mcpName)
		// RegisterWithTaint 将污点等级附加到工具注册，供 InProcessSandbox.Run 写入 ToolResult
		m.sandbox.RegisterWithTaint(toolName, fn, taint)
	}
}

// makeMCPToolFn 创建调用 MCP 工具的执行函数。
// 使用 CallToolTainted 进行污点保护反序列化（M07 §1 安全要求）。
func makeMCPToolFn(client *MCPClient, mcpName string) action.InProcessFn {
	return func(ctx context.Context, input []byte) ([]byte, error) {
		var args map[string]any
		if len(input) > 0 {
			json.Unmarshal(input, &args) //nolint:errcheck
		}
		// CallToolTainted 内部执行 TaintPreservingDecoder，taint 通过 RegisterWithTaint 传递
		text, _, err := client.CallToolTainted(ctx, mcpName, args)
		if err != nil {
			return nil, err
		}
		return []byte(text), nil
	}
}

func (m *MCPManager) unregisterTools(serverID string, tools []MCPTool) {
	for _, t := range tools {
		m.sandbox.Unregister(mcpToolName(serverID, t.Name))
	}
}

func mcpToolName(serverID, toolName string) string {
	return "mcp:" + serverID + ":" + toolName
}
