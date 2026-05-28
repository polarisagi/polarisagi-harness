package config

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/polarisagi/polarisagi-harness/configs"
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
	Tier         int    `yaml:"tier"`
	MaxAgents    int    `yaml:"max_agents"`
	GoMemLimitMB int    `yaml:"go_memlimit_mb"`
	DataDir      string `yaml:"data_dir"`
}

type InferenceConfig struct {
	DefaultProvider   string      `yaml:"default_provider"`
	ReasoningProvider string      `yaml:"reasoning_provider"`
	StructuredOutput  string      `yaml:"structured_output"`
	EmbedderDim       int         `yaml:"embedder_dim"` // vector dimension; changes on local_only toggle
	Cache             CacheConfig `yaml:"cache"`
	STT               STTConfig   `yaml:"stt"`
}

type STTConfig struct {
	SherpaVersion      string `yaml:"sherpa_version" toml:"sherpa_version"`
	SenseVoiceModelURL string `yaml:"sense_voice_model_url" toml:"sense_voice_model_url"`
	PunctModelURL      string `yaml:"punct_model_url" toml:"punct_model_url"`
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
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	CLI       bool   `yaml:"cli"`
	HTTP      bool   `yaml:"http"`
	GRPC      bool   `yaml:"grpc"`
	WebSocket bool   `yaml:"websocket"`
}

func loadModuleTOML(modulePath string, target interface{}) error {
	if _, err := os.Stat(modulePath); os.IsNotExist(err) {
		return nil
	}
	data, err := os.ReadFile(modulePath)
	if err != nil {
		slog.Error("polaris: failed to read threshold override", "file", modulePath, "err", err)
		return err
	}
	if err := toml.Unmarshal(data, target); err != nil {
		slog.Error("polaris: failed to parse threshold override", "file", modulePath, "err", err)
		return err
	}
	slog.Info("polaris: threshold override loaded", "file", modulePath)
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Fallback to embedded configs
		data, err = configs.FS.ReadFile("defaults.yaml")
		if err != nil {
			return nil, err
		}

		// Attempt to export the defaults to the path for future edits
		if errMkdir := os.MkdirAll(filepath.Dir(path), 0755); errMkdir == nil {
			os.WriteFile(path, data, 0600) //nolint:errcheck
		}
	}
	cfg := &Config{Thresholds: DefaultThresholds()}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func LoadThresholds(dataDir string) (*Thresholds, error) {
	t := DefaultThresholds()
	configDir := os.Getenv("POLARIS_THRESHOLDS_DIR")
	if configDir == "" {
		configDir = filepath.Join(dataDir, "config")
	}

	modules := map[string]interface{}{
		"m1_router.toml":        &t.M1Router,
		"m2_storage.toml":       &t.M2Storage,
		"m3_observability.toml": &t.M3Observability,
		"m4_kernel.toml":        &t.M4Kernel,
		"m5_memory.toml":        &t.M5Memory,
		"m6_skill.toml":         &t.M6Skill,
		"m7_tool.toml":          &t.M7Tool,
		"m8_orchestrator.toml":  &t.M8Orchestrator,
		"m9_self_improve.toml":  &t.M9SelfImprove,
		"m10_knowledge.toml":    &t.M10Knowledge,
		"m11_policy.toml":       &t.M11Policy,
		"m12_eval.toml":         &t.M12Eval,
		"m13_interface.toml":    &t.M13Interface,
	}

	for file, target := range modules {
		if err := loadModuleTOML(filepath.Join(configDir, file), target); err != nil {
			return nil, err
		}
	}

	return &t, nil
}
