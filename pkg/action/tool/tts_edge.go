package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// NewEdgeTTS 返回 TTS 工具的 Schema。
func NewEdgeTTS() protocol.Tool {
	return protocol.Tool{
		Name:        "tts_edge",
		Description: "Convert text to speech using Edge TTS",
		Version:     "1.0.0",
		Capability:  protocol.CapWriteNetwork,
		SideEffects: []protocol.SideEffect{protocol.SideNetworkCall},
		RiskLevel:   protocol.RiskLow,
		SandboxTier: protocol.SandboxInProcess,
		Source:      protocol.ToolBuiltin,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Text to convert to speech",
				},
			},
			"required": []string{"text"},
		},
	}
}

// ExecuteEdgeTTS 执行文本转语音。
func ExecuteEdgeTTS(ctx context.Context, args []byte) ([]byte, error) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	// MVP 阶段模拟返回
	audioURI := "data:audio/mp3;base64,SUQz..."

	result := map[string]string{
		"audio_uri": audioURI,
		"status":    "success",
		"message":   "Text converted to speech successfully",
	}
	return json.Marshal(result)
}
