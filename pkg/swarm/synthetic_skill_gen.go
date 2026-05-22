package swarm

import (
	"context"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// SyntheticSkillGen 提供 M6 级别的逻辑坍缩，将任务轨迹生成 Wasm Skill。
type SyntheticSkillGen struct {
	provider protocol.Provider
}

func NewSyntheticSkillGen(provider protocol.Provider) *SyntheticSkillGen {
	return &SyntheticSkillGen{provider: provider}
}

// Generate 生成合成技能 (Logic Collapse)。
func (g *SyntheticSkillGen) Generate(ctx context.Context, name, description string) (protocol.Tool, error) {
	// MVP 阶段：仅生成对应的 Tool 壳子
	return protocol.Tool{
		Name:        name,
		Description: description,
		Version:     "1.0.0",
		Capability:  protocol.CapReadOnly,
		SideEffects: []protocol.SideEffect{protocol.SideNone},
		RiskLevel:   protocol.RiskLow,
		SandboxTier: protocol.SandboxInProcess,
		Source:      protocol.ToolBuiltin,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{"type": "string"},
			},
		},
	}, nil
}
