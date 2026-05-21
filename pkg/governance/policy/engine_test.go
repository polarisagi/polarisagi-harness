package policy

import (
	"context"
	"testing"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// ============================================================================
// DefaultPolicyGate Tests
// ============================================================================

// TestIsAuthorized_EmptyPrincipal — principal="" → false + error
func TestIsAuthorized_EmptyPrincipal(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "", "read_local", "resource", map[string]any{})

	if allowed {
		t.Errorf("expected allowed=false, got true")
	}
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

// TestIsAuthorized_EmptyAction — action="" → false + error
func TestIsAuthorized_EmptyAction(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "", "resource", map[string]any{})

	if allowed {
		t.Errorf("expected allowed=false, got true")
	}
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

// TestIsAuthorized_HardForbidden_DeleteData_NoApproval — delete_data 无 approval_status → false, nil
func TestIsAuthorized_HardForbidden_DeleteData_NoApproval(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "delete_data", "table:users", map[string]any{})

	if allowed {
		t.Errorf("expected allowed=false, got true")
	}
	if err != nil {
		t.Errorf("expected err=nil for hard forbidden, got %v", err)
	}
}

// TestIsAuthorized_HardForbidden_DeleteData_WithApproval — delete_data + approval_status="approved"，通过硬禁止但无 permit 规则 → false, nil
func TestIsAuthorized_HardForbidden_DeleteData_WithApproval(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "delete_data", "table:users", map[string]any{
		"approval_status": "approved",
	})

	if allowed {
		t.Errorf("expected allowed=false (no permit rule), got true")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
}

// TestIsAuthorized_HardForbidden_BudgetExceeded — monthly_spend>budget → false, nil
func TestIsAuthorized_HardForbidden_BudgetExceeded(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "read_local", "resource", map[string]any{
		"monthly_spend_usd":  150.0,
		"monthly_budget_usd": 100.0,
	})

	if allowed {
		t.Errorf("expected allowed=false (budget exceeded), got true")
	}
	if err != nil {
		t.Errorf("expected err=nil for hard forbidden, got %v", err)
	}
}

// TestIsAuthorized_Permitted_ReadLocal — action=read_local, trust_level=2 → true, nil
func TestIsAuthorized_Permitted_ReadLocal(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "read_local", "resource", map[string]any{
		"trust_level": float64(2),
	})

	if !allowed {
		t.Errorf("expected allowed=true, got false")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
}

// TestIsAuthorized_Permitted_ReadLocal_IntTrustLevel — trust_level as int → true, nil
func TestIsAuthorized_Permitted_ReadLocal_IntTrustLevel(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "read_local", "resource", map[string]any{
		"trust_level": 2,
	})

	if !allowed {
		t.Errorf("expected allowed=true, got false")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
}

// TestIsAuthorized_Denied_ReadLocal_LowTrust — trust_level=0 低信任度反向测试 → false, nil
func TestIsAuthorized_Denied_ReadLocal_LowTrust(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "read_local", "file.txt", map[string]any{
		"trust_level": float64(0),
	})

	if allowed {
		t.Errorf("trust_level=0 应被拒绝: ok=%v err=%v", allowed, err)
	}
	if err != nil {
		t.Errorf("expected err=nil for deny-by-default, got %v", err)
	}
}

// TestIsAuthorized_Permitted_NetworkDial — action=network_dial, trust_level=3, capability_token_valid=true → true, nil
func TestIsAuthorized_Permitted_NetworkDial(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "network_dial", "resource", map[string]any{
		"trust_level":            float64(3),
		"capability_token_valid": true,
	})

	if !allowed {
		t.Errorf("expected allowed=true, got false")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
}

// TestIsAuthorized_DenyByDefault — 未知 action → false, nil（无 error）
func TestIsAuthorized_DenyByDefault(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	allowed, err := gate.IsAuthorized(ctx, "user:alice", "unknown_action", "resource", map[string]any{})

	if allowed {
		t.Errorf("expected allowed=false (deny by default), got true")
	}
	if err != nil {
		t.Errorf("expected err=nil (no validation error), got %v", err)
	}
}

// TestReview_Allowed — read_local + trust_level=2 → Allowed=true
func TestReview_Allowed(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	req := protocol.PolicyReviewRequest{
		Principal: "user:alice",
		Action:    "read_local",
		Resource:  "resource",
		Context: map[string]any{
			"trust_level": float64(2),
		},
	}

	result, err := gate.Review(ctx, req)

	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected Allowed=true, got false")
	}
	if result.Reason != "permitted by rule" {
		t.Errorf("expected Reason='permitted by rule', got %q", result.Reason)
	}
	if result.Etag == "" {
		t.Errorf("expected Etag to be non-empty, got empty")
	}
}

// TestReview_Denied — delete_data + 无 approval → Allowed=false
func TestReview_Denied(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	ctx := context.Background()

	req := protocol.PolicyReviewRequest{
		Principal: "user:alice",
		Action:    "delete_data",
		Resource:  "table:users",
		Context:   map[string]any{},
	}

	result, err := gate.Review(ctx, req)

	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
	if result.Allowed {
		t.Errorf("expected Allowed=false, got true")
	}
	if result.Reason != "forbidden by hard constraint" {
		t.Errorf("expected Reason='forbidden by hard constraint', got %q", result.Reason)
	}
}

// TestKillSwitch_TriggeredAfter10Failures — 连续10次 IsAuthorized（空 principal）→ KillSwitch callback 被调用
func TestKillSwitch_TriggeredAfter10Failures(t *testing.T) {
	killSwitchCalled := false
	killSwitchCallback := func() {
		killSwitchCalled = true
	}

	gate := NewDefaultPolicyGate(killSwitchCallback)
	ctx := context.Background()

	// 调用 10 次 IsAuthorized，每次 principal=""，触发 recordFailure
	for i := 0; i < 10; i++ {
		gate.IsAuthorized(ctx, "", "read_local", "resource", map[string]any{})
	}

	if !killSwitchCalled {
		t.Errorf("expected killSwitch callback to be called after 10 failures, but was not")
	}
}

// TestKillSwitch_NotTriggeredBefore10Failures — 连续9次失败不触发 KillSwitch
func TestKillSwitch_NotTriggeredBefore10Failures(t *testing.T) {
	killSwitchCalled := false
	killSwitchCallback := func() {
		killSwitchCalled = true
	}

	gate := NewDefaultPolicyGate(killSwitchCallback)
	ctx := context.Background()

	// 调用 9 次 IsAuthorized，每次 principal=""，触发 recordFailure
	for i := 0; i < 9; i++ {
		gate.IsAuthorized(ctx, "", "read_local", "resource", map[string]any{})
	}

	if killSwitchCalled {
		t.Errorf("expected killSwitch callback NOT to be called after 9 failures, but was called")
	}
}

// TestKillSwitch_ResetOnSuccess — 连续5次失败+1次成功 → 计数器重置
func TestKillSwitch_ResetOnSuccess(t *testing.T) {
	killSwitchCalled := false
	killSwitchCallback := func() {
		killSwitchCalled = true
	}

	gate := NewDefaultPolicyGate(killSwitchCallback)
	ctx := context.Background()

	// 5 次失败
	for i := 0; i < 5; i++ {
		gate.IsAuthorized(ctx, "", "read_local", "resource", map[string]any{})
	}

	// 1 次成功（即使被硬禁止）
	gate.IsAuthorized(ctx, "user:alice", "delete_data", "resource", map[string]any{})

	// 再加4次失败（总共 5+4=9，不应触发）
	for i := 0; i < 4; i++ {
		gate.IsAuthorized(ctx, "", "read_local", "resource", map[string]any{})
	}

	if killSwitchCalled {
		t.Errorf("expected killSwitch not triggered (counter reset after success), but was called")
	}
}

// ============================================================================
// TaintTracker Tests
// ============================================================================

// TestTaintTracker_Mark_Internal — source="internal" → TaintNone
func TestTaintTracker_Mark_Internal(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	level := tracker.Mark("internal")

	if level != protocol.TaintNone {
		t.Errorf("expected TaintNone, got %v", level)
	}
}

// TestTaintTracker_Mark_WebContent — source="web_content" → TaintHigh
func TestTaintTracker_Mark_WebContent(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	level := tracker.Mark("web_content")

	if level != protocol.TaintHigh {
		t.Errorf("expected TaintHigh, got %v", level)
	}
}

// TestTaintTracker_Mark_UserInputRaw — source="user_input_raw" → TaintMedium
func TestTaintTracker_Mark_UserInputRaw(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	level := tracker.Mark("user_input_raw")

	if level != protocol.TaintMedium {
		t.Errorf("expected TaintMedium, got %v", level)
	}
}

// TestTaintTracker_Mark_UserInputSanitised — source="user_input_sanitised" → TaintLow
func TestTaintTracker_Mark_UserInputSanitised(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	level := tracker.Mark("user_input_sanitised")

	if level != protocol.TaintLow {
		t.Errorf("expected TaintLow, got %v", level)
	}
}

// TestTaintTracker_Mark_UnknownSource — source="unknown" → TaintHigh (default)
func TestTaintTracker_Mark_UnknownSource(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	level := tracker.Mark("unknown_source")

	if level != protocol.TaintHigh {
		t.Errorf("expected TaintHigh (default), got %v", level)
	}
}

// TestTaintTracker_IsClean — threshold=TaintMedium: TaintNone/TaintLow 是 clean，TaintMedium 不是
func TestTaintTracker_IsClean(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	tests := []struct {
		level  protocol.TaintLevel
		wantOK bool
	}{
		{protocol.TaintNone, true},
		{protocol.TaintLow, true},
		{protocol.TaintMedium, false},
		{protocol.TaintHigh, false},
		{protocol.TaintUserReviewed, false},
	}

	for _, tt := range tests {
		result := tracker.IsClean(tt.level)
		if result != tt.wantOK {
			t.Errorf("IsClean(%v) = %v, want %v", tt.level, result, tt.wantOK)
		}
	}
}

// TestTaintTracker_Gate_Pass — TaintLow < TaintMedium threshold → nil
func TestTaintTracker_Gate_Pass(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	err := tracker.Gate(protocol.TaintLow)

	if err != nil {
		t.Errorf("expected err=nil (TaintLow < TaintMedium), got %v", err)
	}
}

// TestTaintTracker_Gate_Block — TaintHigh >= TaintMedium threshold → error
func TestTaintTracker_Gate_Block(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	err := tracker.Gate(protocol.TaintHigh)

	if err == nil {
		t.Errorf("expected err != nil (TaintHigh >= TaintMedium), got nil")
	}
}

// TestTaintTracker_Gate_ExactThreshold — TaintMedium == TaintMedium threshold → error
func TestTaintTracker_Gate_ExactThreshold(t *testing.T) {
	tracker := NewTaintTracker(protocol.TaintMedium)

	err := tracker.Gate(protocol.TaintMedium)

	if err == nil {
		t.Errorf("expected err != nil (TaintMedium >= TaintMedium), got nil")
	}
}

// TestTaintTracker_Gate_DifferentThresholds — 多种 threshold 组合
func TestTaintTracker_Gate_DifferentThresholds(t *testing.T) {
	tests := []struct {
		threshold protocol.TaintLevel
		level     protocol.TaintLevel
		wantErr   bool
	}{
		{protocol.TaintLow, protocol.TaintNone, false},
		{protocol.TaintLow, protocol.TaintLow, true},
		{protocol.TaintLow, protocol.TaintMedium, true},
		{protocol.TaintHigh, protocol.TaintLow, false},
		{protocol.TaintHigh, protocol.TaintMedium, false},
		{protocol.TaintHigh, protocol.TaintHigh, true},
	}

	for _, tt := range tests {
		tracker := NewTaintTracker(tt.threshold)
		err := tracker.Gate(tt.level)
		hasErr := err != nil
		if hasErr != tt.wantErr {
			t.Errorf("Gate(threshold=%v, level=%v): wantErr=%v, got %v",
				tt.threshold, tt.level, tt.wantErr, hasErr)
		}
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

// TestIntegration_PolicyGateWithTaintTracking — 组合 gate + taint
func TestIntegration_PolicyGateWithTaintTracking(t *testing.T) {
	gate := NewDefaultPolicyGate(nil)
	taintTracker := NewTaintTracker(protocol.TaintMedium)
	ctx := context.Background()

	// 评估请求（通过）
	allowed, err := gate.IsAuthorized(ctx, "user:alice", "read_local", "resource", map[string]any{
		"trust_level": float64(2),
	})
	if !allowed || err != nil {
		t.Fatalf("policy gate check failed")
	}

	// 检查外部输入污点等级
	inputTaint := taintTracker.Mark("web_content")
	if inputTaint < protocol.TaintMedium {
		t.Fatalf("web_content should be tainted >= TaintMedium")
	}

	// 污点门禁
	err = taintTracker.Gate(inputTaint)
	if err == nil {
		t.Errorf("expected taint gate to block TaintHigh input")
	}

	// 清洁输入
	cleanTaint := taintTracker.Mark("internal")
	err = taintTracker.Gate(cleanTaint)
	if err != nil {
		t.Errorf("expected taint gate to pass for internal source, got %v", err)
	}
}
