package action

import "time"

// CapabilityToken — JIT 签发的短寿命能力令牌。
// 沙箱门口签发，Ed25519 签名，5min TTL，最小权限。
// 架构文档: docs/arch/07-Tool-Action-Layer-深度选型.md §6

type CapabilityToken struct {
	ID              string
	AgentID         string
	SessionID       string
	Operations      []TokenOperation
	IssuedAt        int64
	ExpiresAt       int64 // +5min
	ParentID        string
	DerivationDepth int    // 最大深度 3
	Signature       []byte // Ed25519
}

// TokenOperation 单次授权操作。
type TokenOperation struct {
	ToolName string
	MaxCalls int
	Params   map[string]any
}

// Mint JIT 签发 Token。
// 签发后置到 Sandbox 门口: Planner(S_PLAN)→LLM决定调用→不签发Token(仅ToolIntent)
// → Blackboard Post→CAS Claim+HITL(可能10+分钟)→Worker(S_EXECUTE)进入Sandbox Gate
// → Gate1-5通过→JIT Mint Token(MaxCalls=1, TTL=5min)→立即拉起Sandbox
func Mint(agentID, sessionID string, ops []TokenOperation) *CapabilityToken {
	now := time.Now().Unix()
	return &CapabilityToken{
		ID:         generateID(),
		AgentID:    agentID,
		SessionID:  sessionID,
		Operations: ops,
		IssuedAt:   now,
		ExpiresAt:  now + 300, // 5min TTL
	}
}

// IsExpired 判断 Token 是否过期。
func (t *CapabilityToken) IsExpired() bool {
	return time.Now().Unix() > t.ExpiresAt
}

// Renew 续期 Token（宿主侧 goroutine，Wasm 单线程无法自续）。
// 两层校验: (a) Agent Lease ExpiresAt>now (b) [Cedar-Gate] etag 一致.
// etag 变更 → 重调 PolicyGate.Review → FORBID → ErrPolicyRevoked + cancel Sandbox.
func (t *CapabilityToken) Renew() error {
	if time.Now().Unix() > t.ExpiresAt {
		return ErrTokenExpired
	}
	t.ExpiresAt = time.Now().Unix() + 300
	return nil
}

// ValidateDelegation 校验委托链。
// 规则1 权限收缩: effectiveCapability = min(caller, target)
// 规则2 沙箱单调: target.SandboxTier >= caller.SandboxTier
// 规则3 溯源: DerivationDepth >= 3 → 拒绝
func ValidateDelegation(parent *CapabilityToken, targetTool string) (*CapabilityToken, error) {
	if parent.DerivationDepth >= 3 {
		return nil, ErrMaxDelegationDepth
	}
	return &CapabilityToken{
		ParentID:        parent.ID,
		DerivationDepth: parent.DerivationDepth + 1,
		AgentID:         parent.AgentID,
		SessionID:       parent.SessionID,
	}, nil
}

func generateID() string { return "tok_" + time.Now().Format("20060102150405") }

var (
	ErrTokenExpired       = &TokenError{"token expired"}
	ErrMaxDelegationDepth = &TokenError{"max delegation depth exceeded"}
	ErrPolicyRevoked      = &TokenError{"policy revoked during execution"}
)

type TokenError struct{ msg string }

func (e *TokenError) Error() string { return e.msg }
