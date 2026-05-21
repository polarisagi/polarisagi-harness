package config

// Thresholds 汇总全模块硬编码阈值的参数化配置。
// 架构文档: ROADMAP.md §3 (P2-06 待对齐工程边界)
// SSoT: docs/arch/spec/state.yaml §thresholds
// 加载优先级: 内置默认值 < config/m*.toml < 环境变量 POLARIS_THRESHOLDS_*
type Thresholds struct {
	M1Router        M1RouterThresholds        `toml:"m1_router"`
	M2Storage       M2StorageThresholds       `toml:"m2_storage"`
	M3Observability M3ObservabilityThresholds `toml:"m3_observability"`
	M4Kernel        M4KernelThresholds        `toml:"m4_kernel"`
	M5Memory        M5MemoryThresholds        `toml:"m5_memory"`
	M6Skill         M6SkillThresholds         `toml:"m6_skill"`
	M7Tool          M7ToolThresholds          `toml:"m7_tool"`
	M8Orchestrator  M8OrchestratorThresholds  `toml:"m8_orchestrator"`
	M10Knowledge    M10KnowledgeThresholds    `toml:"m10_knowledge"`
	M11Policy       M11PolicyThresholds       `toml:"m11_policy"`
	M13Interface    M13InterfaceThresholds    `toml:"m13_interface"`
}

type M1RouterThresholds struct {
	CircuitBreakerFailureCount    int `toml:"circuit_breaker.failure_count"`    // 5
	CircuitBreakerCooldownSeconds int `toml:"circuit_breaker.cooldown_seconds"` // 10
	PreFlightCostTolerancePct     int `toml:"pre_flight.cost_tolerance_pct"`    // 15
	TimeoutDialSeconds            int `toml:"timeout.dial_seconds"`             // 3
	TimeoutTLSSeconds             int `toml:"timeout.tls_seconds"`              // 5
	TimeoutResponseHeaderSeconds  int `toml:"timeout.response_header_seconds"`  // 30
	TimeoutTotalSeconds           int `toml:"timeout.total_seconds"`            // 120
	MaxStreamBufferKB             int `toml:"stream.max_buffer_kb"`             // 256
}

type M2StorageThresholds struct {
	SQLiteBusyTimeoutMs int `toml:"sqlite.busy_timeout_ms"`      // 5000
	SurrealBufferPoolMB int `toml:"surreal.buffer_pool_mb"`      // 64 — SurrealDB-Core FFI 内存池上限
	EventlogHotDays     int `toml:"eventlog.hot_days"`           // 7
	EventlogWarmDays    int `toml:"eventlog.warm_days"`          // 30
	MaxBatchSize        int `toml:"transaction.max_batch_size"`  // 64
	MaxRowsPerTx        int `toml:"transaction.max_rows_per_tx"` // 50
	OutboxMaxAttempts   int `toml:"outbox.max_attempts"`         // 5
}

type M3ObservabilityThresholds struct {
	MemCautionMB  int64 `toml:"memory.caution_mb"`  // 1536 (1.5GB)
	MemWarningMB  int64 `toml:"memory.warning_mb"`  // 1024 (1.0GB)
	MemCriticalMB int64 `toml:"memory.critical_mb"` // 512
	BaselineP95   int64 `toml:"baseline.p95"`       // 200
}

type M4KernelThresholds struct {
	MaxReplanAttempts  int `toml:"max_replan_attempts"`  // 3
	DefaultBudget      int `toml:"default_budget"`       // 50000
	MaxSteps           int `toml:"max_steps"`            // 10
	Tier0MaxConcurrent int `toml:"tier0_max_concurrent"` // 4
}

type M5MemoryThresholds struct {
	EpisodicTTLDays       int `toml:"episodic.ttl_days"`         // 30
	ConsolidationInterval int `toml:"consolidation.interval_ms"` // 60000
	ImmutableCoreMax      int `toml:"core.immutable_max"`        // 100
}

type M6SkillThresholds struct {
	GoldCacheSize     int `toml:"cache_size.gold"`      // 5
	SilverCacheSize   int `toml:"cache_size.silver"`    // 20
	BronzeCacheSize   int `toml:"cache_size.bronze"`    // 25
	BronzeCacheTTLMin int `toml:"cache_ttl.bronze_min"` // 30
}

type M7ToolThresholds struct {
	DefaultSandboxLevel int  `toml:"sandbox.default_level"` // 2
	DryRunEnabled       bool `toml:"sandbox.dry_run_enabled"`
	MaxWasmMemoryMB     int  `toml:"wasm.max_memory_mb"`   // 256
	MaxWasmWallclockS   int  `toml:"wasm.max_wallclock_s"` // 60
}

type M8OrchestratorThresholds struct {
	LeaseTTLSeconds    int `toml:"lease.ttl_seconds"`       // 60
	HeartbeatSeconds   int `toml:"heartbeat.seconds"`       // 15
	HeartbeatJitter    int `toml:"heartbeat.jitter"`        // 5
	ReaperScanInterval int `toml:"reaper.scan_interval_ms"` // 1000
	MaxAgentsDesktop   int `toml:"agents.max_desktop"`      // 2
	MaxAgentsServer    int `toml:"agents.max_server"`       // 3
}

type M10KnowledgeThresholds struct {
	RAGFinalTopK        int `toml:"rag.final_top_k"`       // 5
	RAGRerankTopM       int `toml:"rag.rerank_top_m"`      // 50
	GraphRAGDailyBudget int `toml:"graphrag.daily_budget"` // 200
	ChunkSize           int `toml:"chunk.size"`            // 256
}

type M11PolicyThresholds struct {
	CapDefaultTTLSeconds int `toml:"capability.default_ttl_seconds"` // 300
	AuditRetentionDays   int `toml:"audit.retention_days"`           // 730
}

type M13InterfaceThresholds struct {
	HTTPPort               int `toml:"http.port"`                 // 29999
	ReadTimeoutSeconds     int `toml:"timeout.read_seconds"`      // 10
	WriteTimeoutSeconds    int `toml:"timeout.write_seconds"`     // 60
	IdleTimeoutSeconds     int `toml:"timeout.idle_seconds"`      // 120
	HITLDefaultDeadlineMin int `toml:"hitl.default_deadline_min"` // 5
	WorkerIntentHandler    int `toml:"worker.intent_handler"`     // 4
	WorkerIngest           int `toml:"worker.ingest"`             // 2
	WorkerBackground       int `toml:"worker.background"`         // 2
	WorkerEval             int `toml:"worker.eval"`               // 1
	WorkerCron             int `toml:"worker.cron"`               // 1
}

// DefaultThresholds 提供内置默认值（当 config/m*.toml 缺失时使用）。
func DefaultThresholds() Thresholds {
	return Thresholds{
		M1Router: M1RouterThresholds{
			CircuitBreakerFailureCount:    5,
			CircuitBreakerCooldownSeconds: 10,
			PreFlightCostTolerancePct:     15,
			TimeoutDialSeconds:            3,
			TimeoutTLSSeconds:             5,
			TimeoutResponseHeaderSeconds:  30,
			TimeoutTotalSeconds:           120,
			MaxStreamBufferKB:             256,
		},
		M2Storage: M2StorageThresholds{
			SQLiteBusyTimeoutMs: 5000,
			SurrealBufferPoolMB: 64,
			EventlogHotDays:     7,
			EventlogWarmDays:    30,
			MaxBatchSize:        64,
			MaxRowsPerTx:        50,
			OutboxMaxAttempts:   5,
		},
		M3Observability: M3ObservabilityThresholds{
			MemCautionMB:  1536,
			MemWarningMB:  1024,
			MemCriticalMB: 512,
			BaselineP95:   200,
		},
		M4Kernel: M4KernelThresholds{
			MaxReplanAttempts:  3,
			DefaultBudget:      50000,
			MaxSteps:           10,
			Tier0MaxConcurrent: 4,
		},
		M5Memory: M5MemoryThresholds{
			EpisodicTTLDays:       30,
			ConsolidationInterval: 60000,
			ImmutableCoreMax:      100,
		},
		M6Skill: M6SkillThresholds{
			GoldCacheSize:     5,
			SilverCacheSize:   20,
			BronzeCacheSize:   25,
			BronzeCacheTTLMin: 30,
		},
		M7Tool: M7ToolThresholds{
			DefaultSandboxLevel: 2,
			DryRunEnabled:       true,
			MaxWasmMemoryMB:     256,
			MaxWasmWallclockS:   60,
		},
		M8Orchestrator: M8OrchestratorThresholds{
			LeaseTTLSeconds:    60,
			HeartbeatSeconds:   15,
			HeartbeatJitter:    5,
			ReaperScanInterval: 1000,
			MaxAgentsDesktop:   2,
			MaxAgentsServer:    3,
		},
		M10Knowledge: M10KnowledgeThresholds{
			RAGFinalTopK:        5,
			RAGRerankTopM:       50,
			GraphRAGDailyBudget: 200,
			ChunkSize:           256,
		},
		M11Policy: M11PolicyThresholds{
			CapDefaultTTLSeconds: 300,
			AuditRetentionDays:   730,
		},
		M13Interface: M13InterfaceThresholds{
			HTTPPort:               29999,
			ReadTimeoutSeconds:     10,
			WriteTimeoutSeconds:    60,
			IdleTimeoutSeconds:     120,
			HITLDefaultDeadlineMin: 5,
			WorkerIntentHandler:    4,
			WorkerIngest:           2,
			WorkerBackground:       2,
			WorkerEval:             1,
			WorkerCron:             1,
		},
	}
}
