package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// NewVideoAnalysis 返回视频分析工具的 Schema。
func NewVideoAnalysis() protocol.Tool {
	return protocol.Tool{
		Name:        "video_analysis",
		Description: "Analyze video by extracting keyframes to fit within Tier-0 memory constraints",
		Version:     "1.0.0",
		Capability:  protocol.CapWriteNetwork,
		SideEffects: []protocol.SideEffect{protocol.SideNone},
		RiskLevel:   protocol.RiskLow,
		SandboxTier: protocol.SandboxInProcess,
		Source:      protocol.ToolBuiltin,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"video_uri": map[string]any{
					"type":        "string",
					"description": "URI or local path of the video",
				},
				"interval_sec": map[string]any{
					"type":        "integer",
					"description": "Interval in seconds to extract keyframes",
				},
			},
			"required": []string{"video_uri"},
		},
	}
}

// ExecuteVideoAnalysis 执行视频分析。
func ExecuteVideoAnalysis(ctx context.Context, args []byte) ([]byte, error) {
	var req struct {
		VideoURI    string `json:"video_uri"`
		IntervalSec int    `json:"interval_sec"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	if req.IntervalSec <= 0 {
		req.IntervalSec = 5 // 默认 5 秒一帧
	}

	// 实际环境应使用 FFmpeg / OpenCV 提取关键帧并作为 ImagePart 返回，
	// 避免将整个大视频塞入内存。

	result := map[string]any{
		"status": "extracted",
		"frames": []string{
			"data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEAS...", // 模拟数据
			"data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEAS...",
		},
		"message": fmt.Sprintf("Extracted keyframes from %s at %ds interval", req.VideoURI, req.IntervalSec),
	}
	return json.Marshal(result)
}
