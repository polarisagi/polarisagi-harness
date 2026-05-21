package inference

import (
	"context"
	"net/http"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/observability"
)

// DeepSeekAdapter 实现 protocol.Provider，对接 DeepSeek 官方 API (兼容 OpenAI 格式)。
type DeepSeekAdapter struct {
	credentialFn func() string
	client       *OpenAICompatibleClient
	capabilities protocol.ProviderCapabilities
}

// NewDeepSeekAdapter 构造一个 DeepSeek 适配器。
func NewDeepSeekAdapter(credFn func() string, httpClient *http.Client) *DeepSeekAdapter {
	if httpClient == nil {
		httpClient = defaultHTTPClient
	}

	c := &OpenAICompatibleClient{
		BaseURL:    "https://api.deepseek.com/v1", // DeepSeek 兼容入口
		HTTPClient: httpClient,
	}

	return &DeepSeekAdapter{
		credentialFn: credFn,
		client:       c,
		capabilities: protocol.ProviderCapabilities{
			SupportsStreaming: true,
			SupportsTools:     true,
			SupportsThinking:  true,
			MaxContextTokens:  64000,
			CostPer1KInput:    0.14, // 预估费率
			CostPer1KOutput:   0.28,
		},
	}
}

func (d *DeepSeekAdapter) ID() string {
	return "deepseek-v4-flash" // 或者通过参数动态传入
}

func (d *DeepSeekAdapter) Capabilities() protocol.ProviderCapabilities {
	return d.capabilities
}

func (d *DeepSeekAdapter) Tokenizer() protocol.TokenizerAdapter {
	// MVP: 我们假装这里有一个 Tokenizer
	return nil
}

// Infer 阻塞执行单次全量推理。
func (d *DeepSeekAdapter) Infer(ctx context.Context, req *protocol.InferRequest) (*protocol.InferResponse, error) {
	apiReq := translateRequest(req)
	apiKey := d.credentialFn()
	defer clearString(&apiKey)

	apiReq.Model = resolveDeepSeekModel(apiReq.Model)

	resp, err := d.client.SendRequest(ctx, apiKey, apiReq)
	if err != nil {
		return nil, err
	}

	out := &protocol.InferResponse{
		Model: resp.ID,
		Usage: protocol.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	if out.Usage.InputTokens > 0 || out.Usage.OutputTokens > 0 {
		observability.GlobalTokenBurnRate.Add(int64(out.Usage.InputTokens + out.Usage.OutputTokens))
	}

	if len(resp.Choices) > 0 {
		out.Content = resp.Choices[0].Message.Content
		out.FinishReason = resp.Choices[0].FinishReason
	}

	return out, nil
}

// StreamInfer 执行流式推理并返回事件通道。
func (d *DeepSeekAdapter) StreamInfer(ctx context.Context, req *protocol.InferRequest) (<-chan protocol.StreamEvent, error) {
	apiReq := translateRequest(req)
	apiKey := d.credentialFn()
	defer clearString(&apiKey)

	apiReq.Model = resolveDeepSeekModel(apiReq.Model)

	return d.client.SendStreamRequest(ctx, apiKey, apiReq)
}

// resolveDeepSeekModel 负责将旧模型名称迁移到新模型名称（90天过渡期 fallback）
func resolveDeepSeekModel(model string) string {
	switch model {
	case "", "deepseek-chat":
		return "deepseek-v4-flash"
	case "deepseek-reasoner":
		return "deepseek-v4-pro"
	default:
		return model
	}
}
