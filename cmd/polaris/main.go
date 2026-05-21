// polaris-harness main entry point.
// 启动序列：配置加载 → 存储初始化 → 策略引擎 → 推理路由器 → 认知核心 → 协同层 → 调度器/HITL → 信号监听优雅退出。
// 架构文档: docs/arch/ARCHITECTURE.md §3 启动顺序
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/config"
	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/action"
	polartool "github.com/mrlaoliai/polaris-harness/pkg/action/tool"
	"github.com/mrlaoliai/polaris-harness/pkg/cognition/kernel"
	"github.com/mrlaoliai/polaris-harness/pkg/cognition/memory"
	"github.com/mrlaoliai/polaris-harness/pkg/cognition/skill"
	"github.com/mrlaoliai/polaris-harness/pkg/edge/hitl"
	"github.com/mrlaoliai/polaris-harness/pkg/edge/scheduler"
	"github.com/mrlaoliai/polaris-harness/pkg/governance/eval"
	"github.com/mrlaoliai/polaris-harness/pkg/interface/server"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/inference"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/observability"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/policy"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/storage"
	"github.com/mrlaoliai/polaris-harness/pkg/swarm"
	knowledgepkg "github.com/mrlaoliai/polaris-harness/pkg/swarm/knowledge"
	si "github.com/mrlaoliai/polaris-harness/pkg/swarm/self_improve"
	"github.com/mrlaoliai/polaris-harness/pkg/swarm/supervisor"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "polaris: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error { //nolint:gocyclo
	// ─── 0. 子命令分发 ──────────────────────────────────────────────────────
	if len(os.Args) > 1 {
		switch os.Args[1] {
		// CLI 客户端命令（不启动服务，直接与运行中的 server 通信）
		case "init", "setup":
			return runInit()
		case "chat":
			return runChatCmd(os.Args[2:])
		case "status":
			return runCLIStatus()
		case "export":
			return runExport(os.Args[2:])
		case "import":
			return runImport(os.Args[2:])
		case "config":
			return runConfigCmd(os.Args[2:])
		case "version", "--version", "-v":
			fmt.Printf("polaris v%s\n", cliVersion())
			return nil
		case "help", "--help", "-h":
			printCLIHelp()
			return nil
		// 内部子命令（原有）
		case "benchmark-routing":
			return runBenchmarkRouting(os.Args[2:])
		case "migrate":
			if len(os.Args) > 2 && os.Args[2] == "openclaw" {
				return runMigrateOpenClaw(os.Args[3:])
			}
		case "memory":
			if len(os.Args) > 2 && os.Args[2] == "process-staging" {
				return runProcessStaging()
			}
		}
	}

	// ─── 0. 信号监听 ────────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ─── 0.3 日志初始化（stdout + ~/.polaris-harness/polaris.log）─────────────
	dataDir := resolveDataDirBase()
	if logFile := observability.SetupLogger(dataDir); logFile != nil {
		defer logFile.Close()
	}
	// 用 LogStore 包裹全局 handler，获取日志供前端 SSE 流
	logStore := server.NewLogStore(slog.Default().Handler(), 500)
	slog.SetDefault(slog.New(logStore))
	slog.Info("polaris: logger initialized", "data_dir", dataDir)

	// ─── 0.4 硬件探针 → Tier 判定 → FeatureGate ───────────────────────────
	autoConf, err := observability.NewAutoConfig()
	if err != nil {
		slog.Warn("polaris: AutoConfig failed, using Tier0 defaults", "err", err)
	} else {
		slog.Info("polaris: hardware probed",
			"tier", autoConf.Config.Tier,
			"ram_mb", autoConf.Config.TotalRAMMB,
			"cpu_cores", autoConf.Config.CPUCores,
		)
	}

	// ─── 0.5 全局指标生命周期 ──────────────────────────────────────────────────
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				observability.GlobalTokenBurnRate.Tick()
			}
		}
	}()

	// ─── 0.6 内存压力监控（每 5s 轮询，驱动 FeatureGate 运行时降级）──────────
	go autoConf.RunMemoryWatcher(ctx)
	slog.Info("polaris: memory pressure monitor started", "poll_interval_s", 5)

	// ─── 1. 配置加载 ────────────────────────────────────────────────────────
	cfgPath := os.Getenv("POLARIS_CONFIG")
	if cfgPath == "" {
		cfgPath = "configs/defaults.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "config.Load", err)
	}
	slog.Info("polaris: config loaded", "tier", cfg.System.Tier, "max_agents", cfg.System.MaxAgents)

	// ─── 2. 存储初始化 (L0 基础设施) ─────────────────────────────────────────
	dbPath := filepath.Join(dataDir, "polaris.db")
	schemaDir := resolveSchemaDir()
	store, err := storage.OpenSQLiteFromDir(dbPath, schemaDir)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "storage.OpenSQLiteFromDir", err)
	}
	defer store.Close()
	slog.Info("polaris: storage initialized", "db", dbPath)

	// ─── 2.5 SurrealDB Core 认知存储（FeatureSurrealDBCore 门控，Tier0 内存，Tier1+ HNSW）──
	// 门控必须在此检查：无 FFI 隔离时 OpenSurrealDBCore 在内存压力下可触发 OOM。
	var surrealStore *storage.SurrealDBCoreStore
	if autoConf == nil || autoConf.Gate.State(observability.FeatureSurrealDBCore) != observability.FeatureDisabled {
		useHNSW := autoConf != nil && autoConf.Config.SurrealVecMode == observability.SurrealVecHNSW
		if surrealCore, sErr := storage.OpenSurrealDBCore(useHNSW); sErr != nil {
			slog.Warn("polaris: SurrealDB Core init failed, cognitive axis falls back to SQLite", "err", sErr)
		} else {
			surrealStore = surrealCore
			slog.Info("polaris: SurrealDB Core initialized", "hnsw", useHNSW)
		}
	} else {
		slog.Info("polaris: SurrealDB Core disabled by FeatureGate (memory pressure), cognitive axis → SQLite")
	}

	// ─── 2.6 StorageRouter（三轴统一路由）────────────────────────────────────
	var surrealProto protocol.Store
	if surrealStore != nil {
		surrealProto = surrealStore
	}
	storageRouter := substrate.NewStorageRouter(store, surrealProto)
	slog.Info("polaris: storage router initialized")

	// ─── 2.7 OutboxWorker（跨引擎投影）──────────────────────────────────────
	outboxWorker := substrate.NewOutboxWorker(store.DB(), 5, 3)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var cursor int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				batch, bErr := outboxWorker.FetchBatch(ctx, cursor, 20)
				if bErr != nil {
					slog.Warn("polaris: outbox fetch", "err", bErr)
					continue
				}
				for _, rec := range batch {
					if pErr := outboxWorker.Process(ctx, rec); pErr != nil {
						slog.Warn("polaris: outbox process", "err", pErr, "id", rec.ID)
					}
					if rec.ID > cursor {
						cursor = rec.ID
					}
				}
			}
		}
	}()
	slog.Info("polaris: outbox worker started", "poll_interval_s", 5)

	// ─── 月度成本报告 (M13 §1.1) ──────────────────────────────────────────────────
	scheduler.StartMonthlyCostReport(ctx, filepath.Join(dataDir, "reports"))

	// ─── 3. 策略引擎 (L0 PolicyGate) ─────────────────────────────────────────
	gate := policy.NewGate(func() {
		slog.Error("polaris: KILLSWITCH TRIGGERED — escalating to HITL")
		stop()
	})
	slog.Info("polaris: policy gate initialized (deny-by-default)")

	// ─── 4. 推理路由器 (L0 M1) ───────────────────────────────────────────────
	dialer := substrate.NewSafeDialer()
	safeHTTPClient := substrate.NewSafeHTTPClient(dialer)
	inference.SetDefaultHTTPClient(safeHTTPClient)

	reg := inference.NewProviderRegistry()
	if os.Getenv("OPENAI_API_KEY") != "" {
		reg.Register("openai-gpt4o", inference.NewOpenAIAdapter(
			"https://api.openai.com/v1", "gpt-4o",
			func() string { return os.Getenv("OPENAI_API_KEY") }, safeHTTPClient,
		))
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		reg.Register("claude-3-5-sonnet", inference.NewAnthropicAdapter(
			"claude-3-5-sonnet-20241022",
			func() string { return os.Getenv("ANTHROPIC_API_KEY") }, safeHTTPClient,
		))
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		reg.Register("deepseek-v4-flash", inference.NewDeepSeekAdapter(
			func() string { return os.Getenv("DEEPSEEK_API_KEY") }, safeHTTPClient,
		))
	}

	// ─── 4.5 本地推理（Ollama，FeatureLocalInference 门控）─────────────────────
	var embedder substrate.Embedder
	var qloraAdapter *inference.QLoRAAdapter
	var prmAdapter *inference.PRMAdapter
	var steeringAdapter *inference.SteeringAdapter
	if autoConf != nil { //nolint:nestif
		if autoConf.Gate.State(observability.FeatureLocalInference) != observability.FeatureDisabled {
			localModel := autoConf.Config.LocalModelID
			if localModel == "" {
				localModel = "llama3.2"
			}
			reg.Register("ollama-local", inference.NewOllamaAdapter(localModel, safeHTTPClient))
			slog.Info("polaris: Ollama local inference registered", "model", localModel)
		}
		// ─── 4.6 本地嵌入（Ollama，FeatureLocalEmbedding 门控）──────────────────
		if autoConf.Gate.State(observability.FeatureLocalEmbedding) != observability.FeatureDisabled {
			embedModel := autoConf.Config.LocalEmbeddingModel
			if embedModel == "" {
				embedModel = "nomic-embed-text"
			}
			embedder = inference.NewOllamaEmbeddingAdapter(embedModel, safeHTTPClient)
			slog.Info("polaris: Ollama embedding registered", "model", embedModel)
		}
		// ─── 4.7 训练适配器（FeatureQLoRA / FeaturePRMTraining 门控）────────────
		if autoConf.Gate.State(observability.FeatureQLoRA) != observability.FeatureDisabled {
			qloraAdapter = inference.NewQLoRAAdapter("", safeHTTPClient)
			slog.Info("polaris: QLoRA training adapter initialized")
		}
		if autoConf.Gate.State(observability.FeaturePRMTraining) != observability.FeatureDisabled {
			prmAdapter = inference.NewPRMAdapter("", safeHTTPClient)
			slog.Info("polaris: PRM training adapter initialized")
		}
		// ─── 4.8 激活引导（FeatureActivationSteer 门控）─────────────────────────
		if autoConf.Gate.State(observability.FeatureActivationSteer) != observability.FeatureDisabled {
			steeringAdapter = inference.NewSteeringAdapter("", safeHTTPClient)
			slog.Info("polaris: activation steering adapter initialized")
		}
		// ─── 4.9 大型本地模型（FeatureLargeLocalLLM 门控，Tier2+，7B+ 量化模型）──
		if autoConf.Gate.State(observability.FeatureLargeLocalLLM) != observability.FeatureDisabled {
			if largeModel, ok := observability.TierLocalModel(autoConf.Config.Tier); ok {
				reg.Register("ollama-large", inference.NewOllamaAdapter(largeModel, safeHTTPClient))
				slog.Info("polaris: large local LLM registered", "model", largeModel)
			}
		}
	}

	router := inference.NewInferenceRouter(reg, dialer)
	mem := memory.NewMemImpl(store)
	slog.Info("polaris: inference router and memory initialized")

	// ─── 5. MEMF + 启发式记忆（M9 内环基础）────────────────────────────────────
	fallacyPool := swarm.NewFallacyMemoryPool(store.DB())
	heuristics := swarm.NewHeuristicsMemory(store.DB())
	slog.Info("polaris: MEMF and heuristics memory initialized")

	// ─── 6. Sandbox 路由器 (L1 M7) ───────────────────────────────────────────
	var containerSandbox *action.ContainerSandbox
	if autoConf != nil && autoConf.Gate.State(observability.FeatureL3Sandbox) != observability.FeatureDisabled {
		containerSandbox = action.NewContainerSandbox(autoConf.Config.L3SandboxBackend)
		slog.Info("polaris: L3 container sandbox initialized", "backend", autoConf.Config.L3SandboxBackend)
	}
	inProcSandbox := action.NewInProcessSandbox()
	var wasmSandbox *action.WasmSandbox
	if autoConf == nil || autoConf.Gate.State(observability.FeatureL2Sandbox) != observability.FeatureDisabled {
		wasmSandbox = action.NewWasmSandbox(ctx)
		slog.Info("polaris: L2 Wasm sandbox initialized")
	} else {
		slog.Info("polaris: L2 Wasm sandbox disabled by FeatureGate")
	}
	sandboxRouter := action.NewSandboxRouter(inProcSandbox, wasmSandbox, containerSandbox, runtime.GOOS, cfg.System.Tier)
	slog.Info("polaris: sandbox router initialized", "os", runtime.GOOS, "tier", cfg.System.Tier)

	// ─── 6.3 内置工具注册 & MCP Manager ─────────────────────────────────────
	allowedPaths := []string{dataDir}
	toolReg := polartool.NewInMemoryToolRegistry(nil)
	if err := action.RegisterBuiltinTools(inProcSandbox, toolReg, allowedPaths, dialer); err != nil {
		slog.Warn("polaris: builtin tool registration partial failure", "err", err)
	}
	mcpMgr := action.NewMCPManager(inProcSandbox, safeHTTPClient)
	slog.Info("polaris: builtin tools registered, MCP manager initialized")

	// ─── 6.5 Skill Library (L1 M6) ───────────────────────────────────────────
	skillRegistry := skill.NewSQLiteRegistry(store.DB())
	skillSelector := skill.NewSelector(skillRegistry)
	_ = skillSelector

	// WasmSkillExecutor：注入 WazeroRuntime 适配器 + 文件系统加载器
	wasmRT := action.NewWazeroRuntime(ctx)
	wasmRunner := action.NewWasmRunnerAdapter(wasmRT)
	wasmLoader := skill.NewFilesystemWasmLoader("skills/builtin")
	skillExecutor := skill.NewWasmSkillExecutor(skillRegistry, wasmRunner, wasmLoader)
	_ = skillExecutor
	slog.Info("polaris: skill library initialized (wazero-backed)")

	// ─── 7. Knowledge RAG (L2 M10) ───────────────────────────────────────────
	ingester := knowledgepkg.NewDefaultIngestionPipeline(storageRouter)
	retriever := knowledgepkg.NewDefaultHybridRetriever(storageRouter, embedder)
	slog.Info("polaris: knowledge RAG initialized (storage_router_backed)")

	// ─── 7.5 知识图谱构建管线（GraphBuildPipeline，M10 §2.7）───────────────────
	// InferenceRouter 实现 protocol.Provider，作为图构建的 LLM 客户端。
	var graphLLMClient swarm.LLMClient
	if router != nil {
		graphLLMClient = swarm.NewProviderLLMClient(router, "")
	}
	graphTier := 0
	if autoConf != nil {
		graphTier = int(autoConf.Config.Tier)
	}
	var graphPipeline *swarm.GraphBuildPipeline
	if autoConf == nil || autoConf.Gate.State(observability.FeatureGraphRAGFull) != observability.FeatureDisabled {
		graphPipeline = swarm.NewGraphBuildPipeline(graphLLMClient, graphTier)
		slog.Info("polaris: knowledge graph pipeline initialized", "tier", graphTier)
	} else {
		slog.Info("polaris: GraphRAG pipeline disabled by FeatureGate (Tier1+, 1024MB min)")
	}
	_ = graphPipeline

	// ─── 7.6 PII 检测器（M11 §5.1）──────────────────────────────────────────
	var piiDetector *policy.PIIDetector
	if autoConf != nil && autoConf.Gate.State(observability.FeaturePresidioPII) != observability.FeatureDisabled {
		piiDetector = policy.NewPIIDetectorWithPresidio("http://localhost:3000/analyze", safeHTTPClient)
		slog.Info("polaris: PII detector initialized (Presidio sidecar)")
	} else {
		piiDetector = policy.NewPIIDetector()
		slog.Info("polaris: PII detector initialized (Go regex Tier 0)")
	}
	_ = piiDetector

	// ─── 8. Eval Harness (L3 M12) ────────────────────────────────────────────
	evalStore := eval.NewSQLiteEvalStore(store)
	evalRunner := eval.NewRunner(store, evalStore)
	slog.Info("polaris: eval harness initialized")

	// ─── 9. Blackboard & Scheduler (L2 M8 + L3 M13) ─────────────────────────
	blackboard := swarm.NewSQLiteBlackboard(store.DB())
	reaperCtx, reaperStop := context.WithCancel(ctx)
	defer reaperStop()

	reaper := swarm.NewReaper(blackboard)
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-reaperCtx.Done():
				return
			case <-ticker.C:
				reaper.Phase1(reaperCtx)
			}
		}
	}()

	sched := scheduler.NewSQLiteScheduler(store)
	hitlGateway := hitl.NewGateway(store)
	slog.Info("polaris: blackboard, scheduler, HITL gateway initialized")

	// ─── 9.5 M8 Multi-Agent Orchestrator ─────────────────────────────────────
	agentRegistry := swarm.NewAgentRegistry()
	orchestrator := swarm.NewOrchestrator(blackboard, agentRegistry, cfg.System.MaxAgents)

	// ─── 10. Agent Kernel (L1 M4) ────────────────────────────────────────────
	agent := kernel.NewAgent("agent-0", nil, router)
	agent.Config.MaxReplan = cfg.Thresholds.M4Kernel.MaxReplanAttempts
	agent.Config.DefaultBudget = cfg.Thresholds.M4Kernel.DefaultBudget
	agent.Config.MaxSteps = cfg.Thresholds.M4Kernel.MaxSteps
	agent.InjectHITL(hitlGateway)

	// Load preferences from DB and inject into agent
	if prefs, err := server.LoadAllPreferences(ctx, store.DB()); err == nil {
		agent.SetPreferences(prefs)
	} else {
		slog.Warn("polaris: failed to load preferences on startup", "err", err)
	}

	agentRegistry.Register("agent-0", swarm.AgentCard{ //nolint:errcheck
		Name:   "agent-0",
		Skills: []string{"general"},
	}, agent)

	dagExec := kernel.NewDAGExecutor(func(ctx context.Context, toolName string, args []byte, taintLevel protocol.TaintLevel) (*protocol.ToolResult, error) {
		tool := protocol.Tool{
			Name:        toolName,
			SandboxTier: 1,
		}
		return sandboxRouter.Execute(ctx, tool, args)
	}, nil)
	slog.Info("polaris: agent kernel & DAG executor initialized")
	evalRunner.InjectAgent(&evalAgentAdapter{agent: agent})

	// ─── 10.3 M9 Self-Improvement Engine ─────────────────────────────────────
	taskEventCh := make(chan si.TaskCompleteEvent, 64)
	versionEventCh := make(chan si.VersionChangeEvent, 8)

	// 桥接 Blackboard 事件 → M9 TaskCompleteEvent
	go func() {
		bbEvents, subErr := blackboard.Subscribe(ctx)
		if subErr != nil {
			slog.Warn("polaris: m9 blackboard subscribe failed", "err", subErr)
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-bbEvents:
				if !ok {
					return
				}
				switch ev.Type {
				case "task_completed":
					select {
					case taskEventCh <- si.TaskCompleteEvent{
						TaskID:  ev.TaskID,
						Success: true,
					}:
					default:
					}
				case "task_failed":
					select {
					case taskEventCh <- si.TaskCompleteEvent{
						TaskID:  ev.TaskID,
						Success: false,
						Failure: si.FailureLogic,
					}:
					default:
					}
				}
			}
		}
	}()

	reflexionEngine := swarm.NewReflexionEngine(fallacyPool, heuristics, nil)
	reflexionBridge := swarm.NewReflexionBridge(reflexionEngine)
	idleDetector := swarm.NewIdleDetector()
	curriculumGen := swarm.NewAutoCurriculumGenerator(idleDetector, fallacyPool, heuristics)
	curriculumBridge := swarm.NewCurriculumBridge(curriculumGen, blackboard)
	rollout := swarm.NewProgressiveRollout()
	rolloutBridge := swarm.NewRolloutBridge(rollout)

	m9Engine := si.NewEngine(nil, reflexionBridge, curriculumBridge, rolloutBridge, taskEventCh, versionEventCh)

	// ─── PromptOptimizer（M9 三融合：GEPA + MemAPO + ContraPrompt）────────────
	promptOptimizer := swarm.NewPromptOptimizerMVP()
	_ = promptOptimizer
	slog.Info("polaris: M9 self-improvement engine + PromptOptimizer initialized")

	// 训练/引导适配器使用记录（避免 unused 编译错误，后续 M9 流水线消费）
	_ = qloraAdapter
	_ = prmAdapter
	_ = steeringAdapter

	// ─── 10.5 Supervisor Tree 守护进程 ─────────────────────────────────────────
	sv := supervisor.NewSupervisor(5, 5*time.Minute)

	sv.AddWorker("agent-0", func(ctx context.Context) error {
		return agent.Run(ctx)
	})
	sv.AddWorker("orchestrator", func(ctx context.Context) error {
		return orchestrator.ListenLoop(ctx)
	})
	sv.AddWorker("m9-engine", func(ctx context.Context) error {
		return m9Engine.Run(ctx)
	})

	sv.Start()
	defer sv.Stop()
	slog.Info("polaris: supervisor tree started", "workers", 3)

	// ─── 10.7 从 DB 加载厂商配置（覆盖 env var 注册，实现运行时热更新基础）
	if err := server.LoadProvidersFromDB(ctx, store.DB(), reg, safeHTTPClient); err != nil {
		slog.Warn("polaris: LoadProvidersFromDB", "err", err)
	}

	// ─── 11. M13 Interface Server ──────────────────────────────────────────────
	// FeatureWebUI 仅控制 dashboard 是否渲染；REST API 始终启动（Agent 通信依赖）。
	if autoConf != nil && autoConf.Gate.State(observability.FeatureWebUI) == observability.FeatureDisabled {
		slog.Warn("polaris: FeatureWebUI disabled by FeatureGate — serving API-only mode, dashboard unavailable")
	}
	httpServer := server.NewServer(fmt.Sprintf(":%d", cfg.Thresholds.M13Interface.HTTPPort), agent, blackboard, hitlGateway, store.DB(), reg, safeHTTPClient)
	httpServer.SetMCPManager(mcpMgr)
	httpServer.SetToolRegistry(toolReg)
	httpServer.SetSkillRegistry(skillRegistry)
	httpServer.SetToolExecutor(func(ctx context.Context, name string, args []byte) (*protocol.ToolResult, error) {
		return sandboxRouter.Execute(ctx, protocol.Tool{Name: name}, args)
	})
	httpServer.SetLogStore(logStore)
	httpServer.SetEvalRunner(evalRunner)
	if err := httpServer.Start(); err != nil {
		slog.Error("polaris: failed to start HTTP server", "err", err)
		return err
	}
	go mcpMgr.LoadFromDB(ctx, store.DB()) // 异步连接已启用的 MCP Server

	// ─── 12. 启动摘要 ─────────────────────────────────────────────────────────
	printStartupSummary(cfg, gate, router, mem, ingester, retriever, evalRunner, blackboard, sched, hitlGateway, agent, dagExec, httpServer)

	// ─── 13. 等待终止信号 (优雅退出) ─────────────────────────────────────────
	slog.Info("polaris: system ready — waiting for signals (SIGINT/SIGTERM to exit)")
	<-ctx.Done()

	slog.Info("polaris: shutdown initiated, draining...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	reaperStop()
	slog.Info("polaris: shutdown complete")
	return nil
}

func resolveDataDirBase() string {
	dir := os.Getenv("POLARIS_DATA_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".polaris-harness")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "."
	}
	return dir
}

func resolveDataDir(filename string) string {
	return filepath.Join(resolveDataDirBase(), filename)
}

func resolveSchemaDir() string {
	if dir := os.Getenv("POLARIS_SCHEMA_DIR"); dir != "" {
		return dir
	}
	return "internal/protocol/schema"
}

func printStartupSummary(cfg *config.Config, components ...any) {
	slog.Info("polaris: system initialized",
		"tier", cfg.System.Tier,
		"max_agents", cfg.System.MaxAgents,
		"os", runtime.GOOS,
		"components", len(components),
	)
}

// evalAgentAdapter 适配 kernel.Agent → eval.EvalAgent 接口。
// kernel.Agent.Run(ctx) error 与 eval.EvalAgent.Run(ctx, []byte) ([]byte, []string, error) 签名不匹配。
type evalAgentAdapter struct {
	agent *kernel.Agent
}

func (a *evalAgentAdapter) Run(ctx context.Context, input []byte) ([]byte, []string, error) {
	a.agent.SetTaskIntent(input)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.agent.Run(ctx)
	}()

	a.agent.SendIntent(protocol.TriggerIntentReceived)

	select {
	case err := <-errCh:
		if err != nil {
			return nil, nil, err
		}
		return a.agent.GetExecuteResult(), nil, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}
