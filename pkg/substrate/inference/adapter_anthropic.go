package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/observability"
)

// AnthropicAdapter 实现 protocol.Provider，对接 Anthropic Messages API。
type AnthropicAdapter struct {
	model               string
	credentialFn        func() string
	client              *http.Client
	caps                protocol.ProviderCapabilities
	enablePromptCaching bool   // 注入 cache_control 标记以激活 prompt caching
	baseURL             string // 空值 → "https://api.anthropic.com"（测试可覆盖）
}

var _ protocol.Provider = (*AnthropicAdapter)(nil)

// AnthropicOption 适配器选项函数。
type AnthropicOption func(*AnthropicAdapter)

// WithAnthropicPromptCaching 开启 Anthropic prompt caching。
// 向 system prompt 和最后一个 tool 注入 cache_control:{type:"ephemeral"}，
// 命中缓存时 cache_read_input_tokens 费率约为正常输入的 1/10。
func WithAnthropicPromptCaching() AnthropicOption {
	return func(a *AnthropicAdapter) {
		a.enablePromptCaching = true
		a.caps.CostPer1KCacheHit = 0.30 // Anthropic cache read: $0.30/1M tokens
	}
}

// NewAnthropicAdapter 构造 Anthropic 适配器。
func NewAnthropicAdapter(model string, credFn func() string, client *http.Client, opts ...AnthropicOption) *AnthropicAdapter {
	if client == nil {
		client = defaultHTTPClient
	}
	a := &AnthropicAdapter{
		model:        model,
		credentialFn: credFn,
		client:       client,
		caps: protocol.ProviderCapabilities{
			SupportsStreaming: true,
			SupportsTools:     true,
			SupportsThinking:  true,
			MaxContextTokens:  200000,
			CostPer1KInput:    3.0,
			CostPer1KOutput:   15.0,
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// messagesURL 返回 Messages API 端点（测试可通过 baseURL 覆盖）。
func (a *AnthropicAdapter) messagesURL() string {
	base := a.baseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return base + "/v1/messages"
}

func (a *AnthropicAdapter) ModelID() string {
	return a.model
}

func (a *AnthropicAdapter) Capabilities() protocol.ProviderCapabilities {
	return a.caps
}

func (a *AnthropicAdapter) Tokenizer() protocol.TokenizerAdapter {
	return &simpleTokenizer{}
}

func (a *AnthropicAdapter) Infer(ctx context.Context, req *protocol.InferRequest) (*protocol.InferResponse, error) {
	body, err := a.buildAnthropicRequest(req, false)
	if err != nil {
		return nil, err
	}
	apiKey := a.credentialFn()
	defer clearString(&apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		raw, _ := io.ReadAll(httpResp.Body)
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("anthropic: HTTP %d: %s", httpResp.StatusCode, raw))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Model      string `json:"model"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "anthropic: decode", err)
	}

	textBuilder := new(strings.Builder)
	for _, c := range out.Content {
		if c.Type == "text" {
			textBuilder.WriteString(c.Text)
		}
	}
	resp := &protocol.InferResponse{
		Content:      textBuilder.String(),
		FinishReason: out.StopReason,
		Model:        out.Model,
		Usage: protocol.Usage{
			InputTokens:         out.Usage.InputTokens,
			OutputTokens:        out.Usage.OutputTokens,
			CacheHitTokens:      out.Usage.CacheReadInputTokens,
			CacheCreationTokens: out.Usage.CacheCreationInputTokens,
		},
	}

	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		observability.GlobalTokenBurnRate.Add(int64(resp.Usage.InputTokens + resp.Usage.OutputTokens))
	}

	return resp, nil
}

func (a *AnthropicAdapter) StreamInfer(ctx context.Context, req *protocol.InferRequest) (<-chan protocol.StreamEvent, error) {
	body, err := a.buildAnthropicRequest(req, true)
	if err != nil {
		return nil, err
	}
	apiKey := a.credentialFn()
	defer clearString(&apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != 200 {
		raw, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("anthropic: HTTP %d: %s", httpResp.StatusCode, raw))
	}

	ch := make(chan protocol.StreamEvent, 64)
	go func() {
		defer close(ch)
		defer httpResp.Body.Close()
		a.parseAnthropicStream(ctx, httpResp.Body, ch)
	}()
	return ch, nil
}

func (a *AnthropicAdapter) buildAnthropicRequest(req *protocol.InferRequest, stream bool) ([]byte, error) { //nolint:gocyclo
	model := resolveAnthropicModel(a.model)
	if req.Model != "" {
		model = resolveAnthropicModel(req.Model)
	}

	// 转换 messages
	var msgs []map[string]any
	var system string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		if len(m.Parts) > 0 {
			var contentBlocks []any
			for _, p := range m.Parts {
				switch v := p.(type) {
				case protocol.ImagePart:
					contentBlocks = append(contentBlocks, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": v.MediaType,
							"data":       string(v.Data), // Assuming base64 string
						},
					})
				default:
					contentBlocks = append(contentBlocks, v)
				}
			}
			msgs = append(msgs, map[string]any{"role": m.Role, "content": contentBlocks})
		} else {
			msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
		}
	}

	payload := map[string]any{
		"model":      model,
		"messages":   msgs,
		"max_tokens": req.MaxTokens,
	}
	if system != "" {
		payload["system"] = strings.TrimSpace(system)
	}
	if req.MaxTokens <= 0 {
		payload["max_tokens"] = 4096
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	if stream {
		payload["stream"] = true
	}
	// 传入工具 schema（Anthropic tools 格式）
	if len(req.Tools) > 0 {
		anthropicTools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			anthropicTools = append(anthropicTools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		payload["tools"] = anthropicTools
	}

	// Anthropic Prompt Caching: 在 system / last tool / last user message 注入 cache_control
	if a.enablePromptCaching { //nolint:nestif
		// 1. system → array + cache_control
		if system != "" {
			payload["system"] = []map[string]any{
				{"type": "text", "text": strings.TrimSpace(system), "cache_control": map[string]string{"type": "ephemeral"}},
			}
		}
		// 2. last tool → cache_control
		if tools, ok := payload["tools"].([]map[string]any); ok && len(tools) > 0 {
			tools[len(tools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		// 3. last user message → 最后一个 content block 加 cache_control
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i]["role"] == "user" {
				if parts, ok := msgs[i]["content"].([]any); ok && len(parts) > 0 {
					if lastPart, ok := parts[len(parts)-1].(map[string]any); ok {
						lastPart["cache_control"] = map[string]string{"type": "ephemeral"}
					}
				}
				break
			}
		}
	}

	return json.Marshal(payload)
}

// parseAnthropicStream 解析 Anthropic SSE 事件并转换为统一的 StreamEvent。
// tool_use 事件打包为 StreamToolCall，Content 为 JSON: {"id","name","input"}。
func (a *AnthropicAdapter) parseAnthropicStream(ctx context.Context, body io.Reader, ch chan<- protocol.StreamEvent) { //nolint:gocyclo
	scanner := bufio.NewScanner(body)
	// 跟踪当前 tool_use block 的状态
	var toolID, toolName string
	var toolInputBuf strings.Builder
	inToolBlock := false

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var frame struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			continue
		}

		switch frame.Type {
		case "message_start":
			if frame.Message.Usage.InputTokens > 0 {
				ch <- protocol.StreamEvent{
					Type: protocol.StreamTextDelta,
					Usage: protocol.Usage{
						InputTokens:         frame.Message.Usage.InputTokens,
						CacheHitTokens:      frame.Message.Usage.CacheReadInputTokens,
						CacheCreationTokens: frame.Message.Usage.CacheCreationInputTokens,
					},
				}
				observability.GlobalTokenBurnRate.Add(int64(frame.Message.Usage.InputTokens))
			}
		case "content_block_start":
			if frame.ContentBlock.Type == "tool_use" {
				toolID = frame.ContentBlock.ID
				toolName = frame.ContentBlock.Name
				toolInputBuf.Reset()
				inToolBlock = true
			}
		case "content_block_delta":
			if inToolBlock && frame.Delta.Type == "input_json_delta" {
				toolInputBuf.WriteString(frame.Delta.PartialJSON)
			} else if !inToolBlock && frame.Delta.Type == "text_delta" && frame.Delta.Text != "" {
				ch <- protocol.StreamEvent{Type: protocol.StreamTextDelta, Content: frame.Delta.Text}
			}
		case "content_block_stop":
			if inToolBlock {
				inputJSON := toolInputBuf.String()
				if inputJSON == "" {
					inputJSON = "{}"
				}
				payload, _ := json.Marshal(map[string]any{
					"id":    toolID,
					"name":  toolName,
					"input": json.RawMessage(inputJSON),
				})
				ch <- protocol.StreamEvent{Type: protocol.StreamToolCall, Content: string(payload)}
				inToolBlock = false
			}
		case "message_delta":
			if frame.Usage.OutputTokens > 0 {
				ch <- protocol.StreamEvent{
					Type:  protocol.StreamTextDelta,
					Usage: protocol.Usage{OutputTokens: frame.Usage.OutputTokens},
				}
				observability.GlobalTokenBurnRate.Add(int64(frame.Usage.OutputTokens))
			}
		case "message_stop":
			return
		}
	}
}

func resolveAnthropicModel(requested string) string {
	switch requested {
	case "claude-instant-1.2", "claude-2.0", "claude-2.1":
		return "claude-3-5-haiku-latest"
	case "claude-3-opus-20240229":
		return "claude-3-5-sonnet-latest"
	default:
		if requested == "" {
			return "claude-3-5-sonnet-latest"
		}
		return requested
	}
}
