package substrate

import (
	"context"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// Provider Router — 三层递进路由。
// 架构文档: docs/arch/01-Inference-Runtime-深度选型.md §4

func Route(ctx context.Context, req *protocol.InferRequest, providers []protocol.Provider) (protocol.Provider, error) {
	for _, p := range providers {
		if p.Capabilities().MaxContextTokens < req.MaxTokens {
			continue
		}
		if !p.Capabilities().SupportsTools && len(req.Tools) > 0 {
			continue
		}
		return p, nil
	}

	tier := determineComplexity(req)
	for _, p := range providers {
		if matchProviderTier(p, tier) {
			return p, nil
		}
	}

	return nil, perrors.New(perrors.CodeInternal, "all providers exhausted")
}

func determineComplexity(req *protocol.InferRequest) int {
	outputEstimate := estimateOutputTokens(req)
	if len(req.Tools) > 5 || outputEstimate > 4096 {
		return 3
	}
	if len(req.Tools) > 1 || outputEstimate > 1024 {
		return 2
	}
	return 1
}

func estimateOutputTokens(req *protocol.InferRequest) int  { return 1024 }
func matchProviderTier(p protocol.Provider, tier int) bool { return false }
