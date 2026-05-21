package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cfg*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not: valid: yaml: :")
	f.Close()
	_, err = Load(f.Name())
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_ValidMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
system:
  tier: 0
  max_agents: 4
inference:
  default_provider: deepseek
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.System.Tier != 0 {
		t.Errorf("expected tier=0, got %d", cfg.System.Tier)
	}
	if cfg.System.MaxAgents != 4 {
		t.Errorf("expected max_agents=4, got %d", cfg.System.MaxAgents)
	}
	if cfg.Inference.DefaultProvider != "deepseek" {
		t.Errorf("expected default_provider=deepseek, got %s", cfg.Inference.DefaultProvider)
	}
}

func TestLoad_DefaultThresholdsApplied(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("system:\n  tier: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := DefaultThresholds()
	if cfg.Thresholds.M1Router.CircuitBreakerFailureCount != def.M1Router.CircuitBreakerFailureCount {
		t.Errorf("expected default circuit breaker count=%d, got %d",
			def.M1Router.CircuitBreakerFailureCount, cfg.Thresholds.M1Router.CircuitBreakerFailureCount)
	}
}

func TestGetAndUpdate(t *testing.T) {
	original := Get()

	cfg := &Config{}
	cfg.System.Tier = 99
	Update(cfg)
	defer func() { Update(original) }()

	got := Get()
	if got == nil {
		t.Fatal("Get() returned nil after Update")
	}
	if got.System.Tier != 99 {
		t.Errorf("expected tier=99, got %d", got.System.Tier)
	}
}

func TestDefaultThresholds_NonZero(t *testing.T) {
	def := DefaultThresholds()
	if def.M1Router.CircuitBreakerFailureCount == 0 {
		t.Error("expected non-zero M1Router.CircuitBreakerFailureCount")
	}
	if def.M3Observability.MemCautionMB == 0 {
		t.Error("expected non-zero M3Observability.MemCautionMB")
	}
}

func TestApplyThresholdDefaults_ZeroFallback(t *testing.T) {
	// 零值应该回退到默认值
	empty := Thresholds{}
	result := applyThresholdDefaults(empty)
	def := DefaultThresholds()
	if result.M1Router.CircuitBreakerFailureCount != def.M1Router.CircuitBreakerFailureCount {
		t.Errorf("expected fallback to default CircuitBreakerFailureCount")
	}
}
