package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/pkg/action"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// ─── JSON-RPC 2.0 ─────────────────────────────────────────────────────────────

type mcpRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPTool MCP Server 暴露的工具描述。
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPClientConfig MCP Server 连接配置。
type MCPClientConfig struct {
	Transport  MCPTransport      // "stdio" | "sse" | "streamable_http"
	Command    string            // stdio: 可执行命令
	Args       []string          // stdio: 命令参数
	Env        map[string]string // stdio: 附加环境变量
	URL        string            // sse / streamable_http: 端点 URL
	Timeout    time.Duration     // 单次请求超时，0 → 30s
	ServerName string            // 用于 TaintPreservingDecoder 溯源
	Trusted    bool              // true → TaintMedium（白名单）；false → TaintHigh
}

// MCPClient 实现 MCP JSON-RPC 2.0 协议客户端（stdio + SSE + Streamable HTTP）。
type MCPClient struct {
	cfg        MCPClientConfig
	httpClient *http.Client

	// stdio 专用
	cmd   *exec.Cmd
	stdin io.WriteCloser

	// SSE 专用（从 endpoint 事件获取 POST URL）
	postURL string

	// 请求等待表
	mu      sync.Mutex
	pending map[int64]chan *mcpRPCResponse
	nextID  atomic.Int64

	done chan struct{}
	once sync.Once
}

func NewMCPClient(cfg MCPClientConfig, httpClient *http.Client) *MCPClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &MCPClient{
		cfg:        cfg,
		httpClient: httpClient,
		pending:    make(map[int64]chan *mcpRPCResponse),
		done:       make(chan struct{}),
	}
}

// Connect 建立传输层连接并启动响应读取循环。
func (c *MCPClient) Connect(ctx context.Context) error {
	switch c.cfg.Transport {
	case MCPStdio:
		return c.connectStdio(ctx)
	case MCPSSE:
		return c.connectSSE(ctx)
	case MCPStreamableHTTP:
		return nil // HTTP 无持久连接，每次请求独立建立
	default:
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: unsupported transport %q", c.cfg.Transport))
	}
}

// ─── stdio transport ──────────────────────────────────────────────────────────

func (c *MCPClient) connectStdio(ctx context.Context) error {
	if c.cfg.Command == "" {
		return perrors.New(perrors.CodeInternal, "mcp: stdio transport requires command")
	}
	cmd := exec.CommandContext(ctx, c.cfg.Command, c.cfg.Args...)
	for k, v := range c.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: stdin pipe: %v", err), err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: stdout pipe: %v", err), err)
	}
	if err := cmd.Start(); err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: start process: %v", err), err)
	}
	c.cmd = cmd
	c.stdin = stdin
	go c.readLoop(stdout)
	return nil
}

// readLoop 持续读取 stdout，dispatch JSON-RPC 响应。
func (c *MCPClient) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var resp mcpRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Debug("mcp: stdio parse error", "err", err)
			continue
		}
		c.dispatch(&resp)
	}
	c.Close()
}

// ─── SSE transport ────────────────────────────────────────────────────────────

func (c *MCPClient) connectSSE(ctx context.Context) error {
	sseURL := strings.TrimRight(c.cfg.URL, "/") + "/sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: SSE connect: %v", err), err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: SSE status %d", resp.StatusCode))
	}

	endpointCh := make(chan string, 1)
	go c.readSSE(resp.Body, endpointCh)

	select {
	case postURL := <-endpointCh:
		c.postURL = postURL
		return nil
	case <-time.After(10 * time.Second):
		resp.Body.Close()
		return perrors.New(perrors.CodeInternal, "mcp: SSE endpoint event timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *MCPClient) readSSE(body io.ReadCloser, endpointCh chan<- string) {
	defer body.Close()
	scanner := bufio.NewScanner(body)
	var event, data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			switch event {
			case "endpoint":
				select {
				case endpointCh <- data:
				default:
				}
			case "message", "":
				var resp mcpRPCResponse
				if err := json.Unmarshal([]byte(data), &resp); err == nil {
					c.dispatch(&resp)
				}
			}
			event, data = "", ""
			continue
		}
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		} else if v, ok := strings.CutPrefix(line, "data: "); ok {
			data = v
		}
	}
	c.Close()
}

// ─── 发送 / 等待 ──────────────────────────────────────────────────────────────

// call 发送 JSON-RPC 请求并等待响应。
func (c *MCPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := mcpRPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}

	ch := make(chan *mcpRPCResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(ctx, req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp rpc error %d: %s", resp.Error.Code, resp.Error.Message))
		}
		return resp.Result, nil
	case <-time.After(c.cfg.Timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: request timeout (%s)", c.cfg.Timeout))
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *MCPClient) notify(ctx context.Context, method string, params any) error {
	req := mcpRPCRequest{JSONRPC: "2.0", Method: method, Params: params}
	return c.send(ctx, req)
}

func (c *MCPClient) send(ctx context.Context, req mcpRPCRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	switch c.cfg.Transport {
	case MCPStdio:
		_, err = c.stdin.Write(append(b, '\n'))
		return err
	case MCPSSE:
		return c.httpPostOnly(ctx, c.postURL, b)
	case MCPStreamableHTTP:
		resp, err := c.httpPostReceive(ctx, c.cfg.URL, b)
		if err != nil {
			return err
		}
		if resp != nil {
			c.dispatch(resp)
		}
		return nil
	}
	return perrors.New(perrors.CodeInternal, "mcp: unknown transport")
}

func (c *MCPClient) httpPostOnly(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: POST status %d: %s", resp.StatusCode, b))
	}
	return nil
}

// httpPostReceive 向 Streamable HTTP endpoint POST，同步读取 JSON 或 SSE 响应。
func (c *MCPClient) httpPostReceive(ctx context.Context, url string, body []byte) (*mcpRPCResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		var data string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" && data != "" {
				var r mcpRPCResponse
				if json.Unmarshal([]byte(data), &r) == nil {
					return &r, nil
				}
				data = ""
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		return nil, perrors.New(perrors.CodeInternal, "mcp: streamable http: no response in SSE stream")
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	var r mcpRPCResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: response parse: %v", err), err)
	}
	return &r, nil
}

func (c *MCPClient) dispatch(resp *mcpRPCResponse) {
	if resp.ID == nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[*resp.ID]
	if ok {
		delete(c.pending, *resp.ID)
	}
	c.mu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

// ─── MCP 协议方法 ─────────────────────────────────────────────────────────────

// Initialize 执行 MCP 初始化握手。
func (c *MCPClient) Initialize(ctx context.Context) error {
	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "polaris", "version": "1.0"},
	}); err != nil {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: initialize: %v", err), err)
	}
	return c.notify(ctx, "notifications/initialized", nil)
}

// ListTools 查询服务端工具列表。
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	result, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/list: %v", err), err)
	}
	var resp struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/list parse: %v", err), err)
	}
	return resp.Tools, nil
}

// CallTool 调用指定工具并返回文本结果。
func (c *MCPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/call %q: %v", name, err), err)
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/call parse: %v", err), err)
	}
	var sb strings.Builder
	for _, item := range resp.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
		}
	}
	text := sb.String()
	if resp.IsError {
		return "", perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: tool error: %s", text))
	}
	return text, nil
}

// CallToolTainted 调用工具，对响应 JSON 进行污点保护反序列化，返回内容与最高污点等级。
//
// 依赖 TaintPreservingDecoder 对所有 string 叶子打标（M07 §1 安全要求）。
// trusted 由 MCPClientConfig.Trusted 决定：白名单 → TaintMedium；其余 → TaintHigh。
func (c *MCPClient) CallToolTainted(ctx context.Context, name string, arguments map[string]any) (string, protocol.TaintLevel, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", protocol.TaintHigh, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/call %q: %v", name, err), err)
	}

	// 污点保护反序列化：遍历 JSON 树，对所有 string 叶子打标
	dec := action.NewTaintPreservingDecoder(c.cfg.ServerName, c.cfg.Trusted)
	node := dec.Decode(result, "")
	maxTaint := node.MaxTaint()
	if maxTaint < dec.Taint() {
		// 若 JSON 全为非 string 节点（无叶子字符串），仍保守取 server 级别
		maxTaint = dec.Taint()
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", maxTaint, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("mcp: tools/call parse: %v", err), err)
	}
	var sb strings.Builder
	for _, item := range resp.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
		}
	}
	text := sb.String()
	if resp.IsError {
		return "", maxTaint, perrors.New(perrors.CodeInternal, fmt.Sprintf("mcp: tool error: %s", text))
	}
	return text, maxTaint, nil
}

// Close 关闭连接并释放资源。
func (c *MCPClient) Close() {
	c.once.Do(func() {
		close(c.done)
		if c.stdin != nil {
			c.stdin.Close()
		}
		if c.cmd != nil {
			c.cmd.Wait() //nolint:errcheck
		}
	})
}
