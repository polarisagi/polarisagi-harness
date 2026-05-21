package policy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// DefaultPolicyGate implements protocol.PolicyGate using an in-memory
// mock/Cedar-like evaluation strategy (deny-by-default).
type DefaultPolicyGate struct {
	mu                  sync.Mutex
	consecutiveFailures int
	killSwitchTrigger   func()
}

func NewDefaultPolicyGate(killSwitchTrigger func()) *DefaultPolicyGate {
	return &DefaultPolicyGate{
		killSwitchTrigger: killSwitchTrigger,
	}
}

// IsAuthorized evaluates access control (deny-by-default).
func (pg *DefaultPolicyGate) IsAuthorized(ctx context.Context, principal, action, resource string, context map[string]any) (bool, error) {
	// Evaluate with timeout (e.g. >10ms -> deny + count)
	// Simulated logic for MVP:
	if principal == "" || action == "" {
		pg.recordFailure()
		return false, perrors.New(perrors.CodeInternal, "invalid request")
	}

	// forbid-overrides-permit simulation
	if isHardForbidden(principal, action, resource, context) {
		pg.mu.Lock()
		pg.consecutiveFailures = 0 // Reset on successful eval (even if denied)
		pg.mu.Unlock()
		return false, nil
	}

	allowed := isPermitted(principal, action, resource, context)
	pg.mu.Lock()
	pg.consecutiveFailures = 0
	pg.mu.Unlock()
	return allowed, nil
}

// Review provides a detailed policy review response.
func (pg *DefaultPolicyGate) Review(ctx context.Context, req protocol.PolicyReviewRequest) (protocol.PolicyReviewResult, error) {
	allowed, err := pg.IsAuthorized(ctx, req.Principal, req.Action, req.Resource, req.Context)
	if err != nil {
		return protocol.PolicyReviewResult{Allowed: false, Reason: err.Error()}, err
	}

	reason := "denied by default"
	if allowed {
		reason = "permitted by rule"
	} else if isHardForbidden(req.Principal, req.Action, req.Resource, req.Context) {
		reason = "forbidden by hard constraint"
	}

	return protocol.PolicyReviewResult{
		Allowed: allowed,
		Reason:  reason,
		Etag:    fmt.Sprintf("%d", time.Now().UnixNano()),
	}, nil
}

func (pg *DefaultPolicyGate) recordFailure() {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	pg.consecutiveFailures++
	if pg.consecutiveFailures >= 10 && pg.killSwitchTrigger != nil {
		pg.killSwitchTrigger()
	}
}

// Hard constraints (Layer 2)
func isHardForbidden(principal, action, resource string, context map[string]any) bool {
	// Example rule: forbid irreversible actions without approval
	if action == "delete_data" || action == "deploy_to_production" {
		if status, ok := context["approval_status"].(string); !ok || status != "approved" {
			return true
		}
	}
	// Example rule: budget cap
	if spend, ok1 := context["monthly_spend_usd"].(float64); ok1 {
		if budget, ok2 := context["monthly_budget_usd"].(float64); ok2 && spend > budget {
			return true
		}
	}
	return false
}

// Soft constraints (Layer 3) - Cedar-like evaluation
func isPermitted(principal, action, resource string, context map[string]any) bool {
	trustLevel := 0.0
	if t, ok := context["trust_level"].(float64); ok {
		trustLevel = t
	} else if t, ok := context["trust_level"].(int); ok {
		trustLevel = float64(t)
	}

	role, _ := context["role"].(string)
	tokenValid, _ := context["capability_token_valid"].(bool)

	// Policy 3: Admin overrides
	if role == "admin" && strings.HasPrefix(action, "manage_") {
		return true
	}

	// Policy 1: Read operations
	if strings.HasPrefix(action, "read_") {
		if trustLevel >= 1.0 {
			return true
		}
	}

	// Policy 2: Network operations
	if strings.HasPrefix(action, "network_") || action == "http_request" {
		if trustLevel >= 3.0 && tokenValid {
			return true
		}
	}

	// Policy 4: Local Write operations
	if strings.HasPrefix(action, "write_") && !strings.HasPrefix(action, "write_network") {
		if trustLevel >= 2.0 {
			return true
		}
	}

	return false
}

// TaintTracker enforces that external data never enters instruction context unmarked.
// Moved TaintLevel definitions to protocol package to avoid circular deps.
type TaintTracker struct {
	threshold protocol.TaintLevel
}

func NewTaintTracker(threshold protocol.TaintLevel) *TaintTracker {
	return &TaintTracker{threshold: threshold}
}

func (t *TaintTracker) Mark(source string) protocol.TaintLevel {
	switch source {
	case "internal":
		return protocol.TaintNone
	case "user_input_sanitised":
		return protocol.TaintLow
	case "user_input_raw":
		return protocol.TaintMedium
	case "web_content", "api_response_untrusted":
		return protocol.TaintHigh
	case "known_attack":
		return protocol.TaintHigh // Map to highest protocol taint, wait we don't have Critical in protocol.
		// Actually protocol has TaintHigh and TaintUserReviewed
	default:
		return protocol.TaintHigh
	}
}

func (t *TaintTracker) IsClean(level protocol.TaintLevel) bool {
	return level < t.threshold
}

func (t *TaintTracker) Gate(level protocol.TaintLevel) error {
	if level >= t.threshold {
		return perrors.New(perrors.CodeInternal, fmt.Sprintf("taint gate: level %s >= threshold %s", level.String(), t.threshold.String()))
	}
	return nil
}

// AuditTrail is an immutable, append-only record of all policy decisions.
type AuditTrail interface {
	Record(ctx context.Context, entry *AuditEntry) error
	Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)
}

type AuditEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Principal string `json:"principal"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}

type AuditFilter struct {
	Principal string
	Action    string
	Since     string
	Limit     int
}
