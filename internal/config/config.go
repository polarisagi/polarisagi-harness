package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type Config struct {
	System        SystemConfig        `yaml:"system"`
	Inference     InferenceConfig     `yaml:"inference"`
	Storage       StorageConfig       `yaml:"storage"`
	Observability ObservabilityConfig `yaml:"observability"`
	Agent         AgentConfig         `yaml:"agent"`
	Orchestrator  OrchestratorConfig  `yaml:"orchestrator"`
	SelfImprove   SelfImproveConfig   `yaml:"self_improve"`
	Knowledge     KnowledgeConfig     `yaml:"knowledge"`
	Policy        PolicyConfig        `yaml:"policy"`
	Eval          EvalConfig          `yaml:"eval"`
	Interface     InterfaceConfig     `yaml:"interface"`
	Thresholds    Thresholds          `yaml:"-"`
}

type SystemConfig struct {
	Tier         int `yaml:"tier"`
	MaxAgents    int `yaml:"max_agents"`
	GoMemLimitMB int `yaml:"go_memlimit_mb"`
}

type InferenceConfig struct {
	DefaultProvider   string      `yaml:"default_provider"`
	ReasoningProvider string      `yaml:"reasoning_provider"`
	StructuredOutput  string      `yaml:"structured_output"`
	EmbedderDim       int         `yaml:"embedder_dim"` // vector dimension; changes on local_only toggle
	Cache             CacheConfig `yaml:"cache"`
}

type CacheConfig struct {
	Enabled bool   `yaml:"enabled"`
	Backend string `yaml:"backend"`
}

type StorageConfig struct {
	Engines map[string]string `yaml:"engines"`
}

type ObservabilityConfig struct {
	Traces  TraceConfig  `yaml:"traces"`
	Metrics MetricConfig `yaml:"metrics"`
	Logs    LogConfig    `yaml:"logs"`
}

type TraceConfig struct {
	Enabled bool    `yaml:"enabled"`
	Sampler float64 `yaml:"sampler"`
}

type MetricConfig struct {
	Enabled bool `yaml:"enabled"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type AgentConfig struct {
	Kernel KernelConfig `yaml:"kernel"`
	Memory MemoryConfig `yaml:"memory"`
	Skill  SkillConfig  `yaml:"skill"`
}

type KernelConfig struct {
	StateMachine             string  `yaml:"state_machine"`
	DefaultSurpriseThreshold float64 `yaml:"default_surprise_threshold"`
}

type MemoryConfig struct {
	Layers        []string `yaml:"layers"`
	Consolidation string   `yaml:"consolidation"`
}

type SkillConfig struct {
	BuiltinPath                string `yaml:"builtin_path"`
	MaxLogicCollapseConcurrent int    `yaml:"max_logic_collapse_concurrent"`
}

type OrchestratorConfig struct {
	Mode     string `yaml:"mode"`
	Protocol string `yaml:"protocol"`
}

type SelfImproveConfig struct {
	Gradient       bool                `yaml:"gradient"`
	AutoCurriculum bool                `yaml:"auto_curriculum"`
	LogicCollapse  LogicCollapseConfig `yaml:"logic_collapse"`
}

type LogicCollapseConfig struct {
	Enabled              bool `yaml:"enabled"`
	MinSuccessForTrigger int  `yaml:"min_success_for_trigger"`
}

type KnowledgeConfig struct {
	RAG RAGConfig `yaml:"rag"`
}

type RAGConfig struct {
	Mode     string `yaml:"mode"`
	GraphRAG string `yaml:"graphrag"`
}

type PolicyConfig struct {
	Engine       string `yaml:"engine"`
	DefaultBlock bool   `yaml:"default_block"`
}

type EvalConfig struct {
	CIGate       bool `yaml:"ci_gate"`
	ShadowDeploy bool `yaml:"shadow_deploy"`
}

type InterfaceConfig struct {
	CLI       bool `yaml:"cli"`
	HTTP      bool `yaml:"http"`
	GRPC      bool `yaml:"grpc"`
	WebSocket bool `yaml:"websocket"`
}

func loadModuleTOML(modulePath string, target interface{}) {
	if _, err := os.Stat(modulePath); err == nil {
		data, err := os.ReadFile(modulePath)
		if err == nil {
			toml.Unmarshal(data, target) //nolint:errcheck
		}
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Thresholds: DefaultThresholds()}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	configDir := os.Getenv("POLARIS_THRESHOLDS_DIR")
	if configDir == "" {
		configDir = "config"
	}

	loadModuleTOML(filepath.Join(configDir, "m1_router.toml"), &cfg.Thresholds.M1Router)
	loadModuleTOML(filepath.Join(configDir, "m2_storage.toml"), &cfg.Thresholds.M2Storage)
	loadModuleTOML(filepath.Join(configDir, "m3_observability.toml"), &cfg.Thresholds.M3Observability)
	loadModuleTOML(filepath.Join(configDir, "m4_kernel.toml"), &cfg.Thresholds.M4Kernel)
	loadModuleTOML(filepath.Join(configDir, "m5_memory.toml"), &cfg.Thresholds.M5Memory)
	loadModuleTOML(filepath.Join(configDir, "m6_skill.toml"), &cfg.Thresholds.M6Skill)
	loadModuleTOML(filepath.Join(configDir, "m7_tool.toml"), &cfg.Thresholds.M7Tool)
	loadModuleTOML(filepath.Join(configDir, "m8_orchestrator.toml"), &cfg.Thresholds.M8Orchestrator)
	loadModuleTOML(filepath.Join(configDir, "m10_knowledge.toml"), &cfg.Thresholds.M10Knowledge)
	loadModuleTOML(filepath.Join(configDir, "m11_policy.toml"), &cfg.Thresholds.M11Policy)
	loadModuleTOML(filepath.Join(configDir, "m13_interface.toml"), &cfg.Thresholds.M13Interface)

	cfg.Thresholds = applyThresholdDefaults(cfg.Thresholds)

	return cfg, nil
}

// applyThresholdDefaults 合并外部配置到默认值：外部值为 0 时回退默认。
func applyThresholdDefaults(t Thresholds) Thresholds {
	def := DefaultThresholds()
	if t.M1Router.CircuitBreakerFailureCount == 0 {
		t.M1Router.CircuitBreakerFailureCount = def.M1Router.CircuitBreakerFailureCount
	}
	if t.M1Router.CircuitBreakerCooldownSeconds == 0 {
		t.M1Router.CircuitBreakerCooldownSeconds = def.M1Router.CircuitBreakerCooldownSeconds
	}
	if t.M2Storage.SurrealBufferPoolMB == 0 {
		t.M2Storage.SurrealBufferPoolMB = def.M2Storage.SurrealBufferPoolMB
	}
	if t.M3Observability.MemCautionMB == 0 {
		t.M3Observability.MemCautionMB = def.M3Observability.MemCautionMB
	}
	if t.M3Observability.MemWarningMB == 0 {
		t.M3Observability.MemWarningMB = def.M3Observability.MemWarningMB
	}
	if t.M3Observability.MemCriticalMB == 0 {
		t.M3Observability.MemCriticalMB = def.M3Observability.MemCriticalMB
	}
	if t.M4Kernel.MaxReplanAttempts == 0 {
		t.M4Kernel.MaxReplanAttempts = def.M4Kernel.MaxReplanAttempts
	}
	if t.M8Orchestrator.LeaseTTLSeconds == 0 {
		t.M8Orchestrator.LeaseTTLSeconds = def.M8Orchestrator.LeaseTTLSeconds
	}
	return t
}
