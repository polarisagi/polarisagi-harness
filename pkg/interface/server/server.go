package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/action"
	"github.com/mrlaoliai/polaris-harness/pkg/cognition/kernel"
	"github.com/mrlaoliai/polaris-harness/pkg/interface/channels"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/inference"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate/observability"
	webui "github.com/mrlaoliai/polaris-harness/web"

	"gopkg.in/yaml.v3"
)

// Server 包装 HTTP 与 WebSocket 服务，作为 M13 的对外网关。
type Server struct {
	addr          string
	srv           *http.Server
	agent         *kernel.Agent
	blackboard    protocol.Blackboard
	hitlGateway   protocol.HITL
	db            *sql.DB
	registry      *inference.ProviderRegistry                                                       // 热重载 Provider 注册表
	httpClient    *http.Client                                                                      // 复用 SafeHTTPClient
	transcriptDir string                                                                            // per-session JSONL transcript 目录
	hooks         *HookRunner                                                                       // Shell Script Hooks（End-User 扩展点）
	compressor    *Compressor                                                                       // 上下文超长自动压缩
	channelMgr    *channels.Manager                                                                 // 所有聊天平台 poller 管理
	mcpMgr        *action.MCPManager                                                                // MCP Server 连接管理
	toolReg       protocol.ToolRegistry                                                             // builtin tool 元数据
	skillReg      protocol.SkillRegistry                                                            // skill 元数据
	toolExec      func(ctx context.Context, name string, args []byte) (*protocol.ToolResult, error) // tool_use 执行器
	logStore      *LogStore                                                                         // 日志环形缓冲 + SSE 广播
	evalRunner    protocol.EvalRunner                                                               // M12 评测套件
}

// SetMCPManager 注入 MCPManager（NewServer 之后、Start 之前调用）。
func (s *Server) SetMCPManager(m *action.MCPManager) { s.mcpMgr = m }

// SetToolRegistry 注入 ToolRegistry（NewServer 之后、Start 之前调用）。
func (s *Server) SetToolRegistry(r protocol.ToolRegistry) { s.toolReg = r }

// SetSkillRegistry 注入 SkillRegistry（NewServer 之后、Start 之前调用）。
func (s *Server) SetSkillRegistry(r protocol.SkillRegistry) { s.skillReg = r }

// SetToolExecutor 注入工具执行函数，用于 tool_use 循环（NewServer 之后、Start 之前调用）。
func (s *Server) SetToolExecutor(fn func(ctx context.Context, name string, args []byte) (*protocol.ToolResult, error)) {
	s.toolExec = fn
}

// SetLogStore 注入日志存储（NewServer 之后、Start 之前调用）。
func (s *Server) SetLogStore(ls *LogStore) { s.logStore = ls }

// SetEvalRunner 注入 M12 评测套件（NewServer 之后、Start 之前调用）。
func (s *Server) SetEvalRunner(r protocol.EvalRunner) { s.evalRunner = r }

// buildToolSchemas 收集全部可用工具 schema，用于注入 InferRequest.Tools。
func (s *Server) buildToolSchemas() []protocol.ToolSchema {
	var schemas []protocol.ToolSchema
	if s.toolReg != nil {
		for _, t := range s.toolReg.List() {
			schemas = append(schemas, protocol.ToolSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	if s.mcpMgr != nil {
		schemas = append(schemas, s.mcpMgr.ListToolSchemas()...)
	}
	return schemas
}

// NewServer 创建新的 HTTP Server。
// DEV_MODE=1 时将静态资源请求反向代理到 Vite dev server (:5173)。
func NewServer(addr string, agent *kernel.Agent, bb protocol.Blackboard, hitlGateway protocol.HITL, db *sql.DB, registry *inference.ProviderRegistry, httpClient *http.Client, safeDialer protocol.SafeDialer) *Server {
	tDir := defaultTranscriptDir()
	go PruneTranscripts(tDir, 30) // 启动时异步清理 30 天前的 transcript

	s := &Server{
		addr:          addr,
		agent:         agent,
		blackboard:    bb,
		hitlGateway:   hitlGateway,
		db:            db,
		registry:      registry,
		httpClient:    httpClient,
		transcriptDir: tDir,
		hooks:         NewHookRunner(),
	}

	// 注入内置的 yaml 配置作为种子数据到数据库（SSoT 架构）
	if b, err := os.ReadFile("configs/marketplaces.yaml"); err == nil {
		var mps []Marketplace
		if err := yaml.Unmarshal(b, &mps); err == nil {
			now := time.Now().UTC().Format(time.RFC3339)
			for _, mp := range mps {
				_, _ = db.Exec(`INSERT OR IGNORE INTO plugin_marketplaces(id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at) 
				                VALUES(?,?,?,?,?,?,1,?,1,?)`,
					mp.ID, mp.Name, mp.Type, mp.Publisher, mp.RepoURL, mp.Description, mp.TrustTier, now)
			}
		}
	} else {
		slog.Warn("polaris-server: configs/marketplaces.yaml load failed", "err", err)
	}

	if b, err := os.ReadFile("configs/registry.yaml"); err == nil {
		var entries []RegistryEntry
		if err := yaml.Unmarshal(b, &entries); err == nil {
			for _, e := range entries {
				payload, _ := json.Marshal(e)
				_, _ = db.Exec(`INSERT OR IGNORE INTO registry_cache(id, marketplace_id, type, name, description, publisher, trust_tier, url, payload) 
				                VALUES(?,?,?,?,?,?,?,?,?)`,
					e.ID, "builtin", e.Type, e.Name, e.Description, e.Publisher, e.TrustTier, e.URL, string(payload))
			}
		}
	} else {
		slog.Warn("polaris-server: configs/registry.yaml load failed", "err", err)
	}

	s.compressor = newCompressor(db, s.hooks)
	s.channelMgr = channels.NewManager(httpClient, func(channelType, channelID string, cfg map[string]any, msg channels.Message) {
		s.dispatchChannelMessage(channelType, channelID, cfg, msg)
	}, channels.WithSafeDialer(safeDialer))

	mux := http.NewServeMux()

	// API 端点
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleHealthz)
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("GET /v1/doctor", s.handleDoctor)
	mux.Handle("GET /metrics", observability.MetricsHandler())
	mux.HandleFunc("GET /v1/logs/stream", s.handleLogStream)
	mux.HandleFunc("POST /v1/agent/query", s.handleAgentQuery)
	mux.HandleFunc("POST /v1/agent/stream", s.handleAgentStream)
	mux.HandleFunc("POST /v1/agent/{taskID}/interrupt", s.handleAgentInterrupt) // inv_global_08 <200ms
	mux.HandleFunc("GET /v1/approvals/pending", s.handleGetPendingApprovals)
	mux.HandleFunc("POST /v1/approvals/", s.handleResolveApproval) // /v1/approvals/{id}/resolve

	// LLM 厂商配置 API
	mux.HandleFunc("GET /v1/providers", s.handleListProviders)
	mux.HandleFunc("POST /v1/providers", s.handleCreateProvider)
	mux.HandleFunc("PUT /v1/providers/{providerID}", s.handleUpdateProvider)
	mux.HandleFunc("DELETE /v1/providers/{providerID}", s.handleDeleteProvider)
	mux.HandleFunc("POST /v1/providers/{providerID}/test", s.handleTestProvider)

	// 厂商模型管理 API（两层架构：provider → models）
	mux.HandleFunc("GET /v1/providers/{providerID}/models", s.handleListModels)
	mux.HandleFunc("POST /v1/providers/{providerID}/models", s.handleCreateModel)
	mux.HandleFunc("PUT /v1/providers/{providerID}/models/{modelID}", s.handleUpdateModel)
	mux.HandleFunc("DELETE /v1/providers/{providerID}/models/{modelID}", s.handleDeleteModel)

	// 模型角色配置 API（对话模型 / 推理模型）
	mux.HandleFunc("GET /v1/config/model-roles", s.handleGetModelRoles)
	mux.HandleFunc("PUT /v1/config/model-roles", s.handleSetModelRoles)
	mux.HandleFunc("GET /v1/config", s.handleGetConfig)

	// Preferences API
	mux.HandleFunc("GET /v1/preferences", s.handleGetPreferences)
	mux.HandleFunc("PUT /v1/preferences/{key}", s.handleSetPreference)

	// M12 评测 API
	mux.HandleFunc("POST /v1/eval/run", s.handleEvalRun)

	// 会话历史 API
	mux.HandleFunc("GET /v1/sessions", s.handleListSessions)
	mux.HandleFunc("GET /v1/sessions/{sessionID}", s.handleGetSession)
	mux.HandleFunc("DELETE /v1/sessions/{sessionID}", s.handleDeleteSession)

	// 全文搜索 API（FTS5）
	mux.HandleFunc("GET /v1/search", s.handleSearch)

	// 用量洞察 & 会话回顾
	mux.HandleFunc("GET /v1/insights", s.handleInsights)
	mux.HandleFunc("GET /v1/sessions/{sessionID}/recap", s.handleSessionRecap)

	// Trajectory 导出（自演化训练数据）
	mux.HandleFunc("GET /v1/export/trajectories", s.handleExportTrajectories)

	// 自动化 (Automations)
	mux.HandleFunc("GET /v1/automations", s.handleListAutomations)
	mux.HandleFunc("POST /v1/automations", s.handleCreateAutomation)
	mux.HandleFunc("PUT /v1/automations/{id}", s.handleUpdateAutomation)
	mux.HandleFunc("DELETE /v1/automations/{id}", s.handleDeleteAutomation)
	mux.HandleFunc("GET /v1/automations/{id}/runs", s.handleListAutomationRuns)
	mux.HandleFunc("POST /v1/automations/{id}/trigger", s.handleTriggerAutomation)
	mux.HandleFunc("GET /v1/automation-templates", s.handleListAutomationTemplates)

	// 聊天平台集成 API
	mux.HandleFunc("GET /v1/channels", s.handleListChannels)
	mux.HandleFunc("POST /v1/channels", s.handleCreateChannel)
	mux.HandleFunc("PUT /v1/channels/{channelID}", s.handleUpdateChannel)
	mux.HandleFunc("DELETE /v1/channels/{channelID}", s.handleDeleteChannel)
	mux.HandleFunc("POST /v1/webhooks/{channelType}/{channelID}", s.handleWebhookReceive)

	// 工具 & Skill 管理 API
	mux.HandleFunc("GET /v1/tools", s.handleListTools)
	mux.HandleFunc("GET /v1/tools/schemas", s.handleListToolSchemas)
	mux.HandleFunc("POST /v1/tools/{name}/execute", s.handleExecuteTool)
	mux.HandleFunc("GET /v1/skills", s.handleListSkills)
	mux.HandleFunc("POST /v1/skills/install", s.handleInstallSkill)

	// MCP Server 管理 API
	mux.HandleFunc("GET /v1/mcp-servers", s.handleListMCPServers)
	mux.HandleFunc("POST /v1/mcp-servers", s.handleCreateMCPServer)
	mux.HandleFunc("PUT /v1/mcp-servers/{serverID}", s.handleUpdateMCPServer)
	mux.HandleFunc("DELETE /v1/mcp-servers/{serverID}", s.handleDeleteMCPServer)
	mux.HandleFunc("POST /v1/mcp-servers/{serverID}/test", s.handleTestMCPServer)

	// 插件目录 API
	mux.HandleFunc("GET /v1/plugins/catalog", s.handleListPluginCatalog)
	mux.HandleFunc("POST /v1/plugins/install", s.handleInstallPlugin)
	mux.HandleFunc("DELETE /v1/plugins/{catalogID}", s.handleUninstallPlugin)

	// Custom Entity Creation
	mux.HandleFunc("POST /v1/mcp/create", s.handleCreateMCP)
	mux.HandleFunc("POST /v1/skills/create", s.handleCreateSkill)
	mux.HandleFunc("POST /v1/plugins/create", s.handleCreatePlugin)
	mux.HandleFunc("POST /v1/apps/create", s.handleCreateApp)

	// 插件市场 API
	mux.HandleFunc("GET /v1/plugins/marketplaces", s.handleListMarketplaces)
	mux.HandleFunc("POST /v1/plugins/marketplaces", s.handleAddMarketplace)
	mux.HandleFunc("DELETE /v1/plugins/marketplaces/{id}", s.handleDeleteMarketplace)
	mux.HandleFunc("POST /v1/plugins/sync", s.handleSyncMarketplaces)

	// OpenAI 兼容端点（允许第三方 OpenAI SDK 客户端直接对接）
	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChat)

	// 预算管理
	mux.HandleFunc("GET /v1/config/budget", s.handleGetBudget)
	mux.HandleFunc("PUT /v1/config/budget", s.handleSetBudget)

	// 系统备份 / 恢复
	mux.HandleFunc("GET /v1/export/backup", s.handleExportBackup)
	mux.HandleFunc("POST /v1/import/backup", s.handleImportBackup)

	s.setupWebUI(mux)

	// 挂载中间件
	handler := s.withMiddleware(mux)

	s.srv = &http.Server{
		Addr:        addr,
		Handler:     handler,
		ReadTimeout: 30 * time.Second,
		// WriteTimeout 设为 0 禁用全局超时：SSE 流式连接由 ResponseController 管理每请求超时。
		// 短超时（如 60s）会在长对话中途断流。
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *Server) setupWebUI(mux *http.ServeMux) {
	// 挂载 Web UI 静态资源：DEV_MODE=1 反代 Vite，否则用 go:embed dist
	if os.Getenv("DEV_MODE") == "1" {
		target, _ := url.Parse("http://localhost:5173")
		proxy := httputil.NewSingleHostReverseProxy(target)
		mux.Handle("/", proxy)
		return
	}

	subFS, err := fs.Sub(webui.WebUIFS, "dist")
	if err != nil {
		return
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Don't fallback for API routes
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Clean the path to check if it exists in the embed FS
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "."
		}

		// Check if the requested file exists
		f, err := subFS.Open(p)
		if err != nil {
			// Fallback to index.html for SPA routing
			r.URL.Path = "/"
		} else {
			f.Close()
		}

		// 缓存策略：
		// - index.html 及所有 HTML：no-cache（每次重新验证，防止浏览器用旧 HTML）
		// - /assets/*.js /assets/*.css（Vite 内容 hash 命名）：immutable 永久缓存
		// - 其他静态资源：1h 缓存
		switch {
		case strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" || r.URL.Path == "":
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}

		http.FileServer(http.FS(subFS)).ServeHTTP(w, r)
	})
}

// Start 非阻塞启动服务器。
func (s *Server) Start() error {
	slog.Info("polaris-server: starting", "addr", s.addr)

	// 提前监听端口，如果失败直接返回，避免其他后台协程（如 pollers）启动导致多个实例抢占
	// 注意：在 net/http 中我们可以使用 net.Listen
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		slog.Error("polaris-server: listener error", "err", err)
		return err
	}

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("polaris-server: serve error", "err", err)
		}
	}()
	go s.channelMgr.LoadFromDB(s.db)        // 启动所有已配置平台的 poller
	s.startCronRunner(context.Background()) // 启动 Cron 定时任务后台 runner

	// gateway.startup hook：服务完全启动后触发，fire-and-forget
	workspace := os.Getenv("POLARIS_DATA_DIR")
	if workspace == "" {
		if home, err := os.UserHomeDir(); err == nil {
			workspace = filepath.Join(home, ".polaris-harness")
		}
	}
	s.hooks.Fire("gateway.startup", map[string]string{
		"POLARIS_WORKSPACE": workspace,
		"POLARIS_ADDR":      s.addr,
	})

	return nil
}

// Shutdown 优雅关闭服务器。
func (s *Server) Shutdown(ctx context.Context) error {
	s.channelMgr.StopAll()
	return s.srv.Shutdown(ctx)
}

// handleHealthz 提供基础的健康检查。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}

// handleGetConfig 返回当前运行时配置的 YAML 原始内容（只读视图）。
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := os.Getenv("POLARIS_CONFIG")
	if cfgPath == "" {
		cfgPath = "configs/defaults.yaml"
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		http.Error(w, "config file not readable: "+err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":   cfgPath,
		"format": "yaml",
		"raw":    string(raw),
	})
}

// handleEvalRun 触发 M12 评测套件执行并返回报告。
// POST /v1/eval/run  body: {"suite":"training"|"validation"}
func (s *Server) handleEvalRun(w http.ResponseWriter, r *http.Request) {
	if s.evalRunner == nil {
		http.Error(w, "eval runner not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Suite       string `json:"suite"`
		CandidateID string `json:"candidate_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Suite == "" {
		req.Suite = "training"
	}
	report, err := s.evalRunner.RunSuite(r.Context(), req.Suite, req.CandidateID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// handleAgentQuery 处理同步的单次对话/查询请求。
func (s *Server) handleAgentQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input     string `json:"input"`
		SessionID string `json:"session_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 阻塞等待内核执行（MVP 直接向 Agent 注入 Intent，但目前内核仅支持独占执行）
	// M8 Blackboard 集成后应该发布 Task，然后等待
	s.agent.SetTaskIntent([]byte(req.Input))
	// s.agent.SendIntent(...) 启动等

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"response": "Agent query acknowledged (async). Use SSE for streaming or Blackboard for results.",
	})
}

// handleGetPendingApprovals 获取待审批任务。
func (s *Server) handleGetPendingApprovals(w http.ResponseWriter, r *http.Request) {
	if s.hitlGateway == nil {
		http.Error(w, "HITL not enabled", http.StatusNotImplemented)
		return
	}

	pending, err := s.hitlGateway.Pending(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pending": pending,
	})
}

// handleAgentInterrupt 处理用户中断请求（M13 §1.2.5，inv_global_08 <200ms SLO）。
// POST /v1/agent/{taskID}/interrupt
// body: {"action":"resume"|"redirect"|"abort","redirect":"新意图文本","reason":"..."}
func (s *Server) handleAgentInterrupt(w http.ResponseWriter, r *http.Request) {
	if s.agent == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}

	taskID := r.PathValue("taskID")
	var req struct {
		Action   string `json:"action"`   // "resume" | "redirect" | "abort"
		Redirect string `json:"redirect"` // action=redirect 时的新意图
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action := kernel.InterruptResume
	switch req.Action {
	case "redirect":
		action = kernel.InterruptRedirect
	case "abort":
		action = kernel.InterruptAbort
	}

	s.agent.Interrupt(kernel.InterruptRequest{
		Reason:   req.Reason,
		Action:   action,
		Redirect: req.Redirect,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "accepted",
		"taskID": taskID,
	})
}

// handleResolveApproval 提交审批结果。
func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	if s.hitlGateway == nil {
		http.Error(w, "HITL not enabled", http.StatusNotImplemented)
		return
	}

	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 || pathParts[4] != "resolve" {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	approvalID := pathParts[3]

	var req struct {
		Action  string `json:"action"` // "approve" or "deny"
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authCtx := FromContext(r.Context())

	resp := protocol.HITLResponse{
		OptionKey: req.Action,
		UserID:    authCtx.UserID, // M13: 接入鉴权上下文
	}

	err := s.hitlGateway.Respond(r.Context(), approvalID, resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleStatus 返回 WebUI statusBar 所需的运行时指标快照。
func agentStateString(s protocol.AgentState) string {
	switch s {
	case protocol.AgentStateIdle:
		return "idle"
	case protocol.AgentStatePerceive:
		return "perceive"
	case protocol.AgentStatePlan:
		return "plan"
	case protocol.AgentStateValidate:
		return "validate"
	case protocol.AgentStateExecute:
		return "execute"
	case protocol.AgentStateReflect:
		return "reflect"
	case protocol.AgentStateReplan:
		return "replan"
	case protocol.AgentStateRollback:
		return "rollback"
	case protocol.AgentStateComplete:
		return "complete"
	case protocol.AgentStateFailed:
		return "failed"
	case protocol.AgentStateInterrupt:
		return "interrupt"
	default:
		return "unknown"
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	memMB := memStats.Sys / (1024 * 1024)

	// 从 registry 取当前对话模型名称
	modelID := s.registry.PickProviderName("default")
	if modelID == "" {
		modelID = s.registry.PickProviderName("general")
	}

	// Agent state
	agentState := ""
	agentID := ""
	agentConfig := map[string]any{}
	if s.agent != nil {
		agentID = s.agent.ID
		agentState = agentStateString(s.agent.StateMachine().Current())
		agentConfig = map[string]any{
			"max_replan":     s.agent.Config.MaxReplan,
			"default_budget": s.agent.Config.DefaultBudget,
			"max_steps":      s.agent.Config.MaxSteps,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sealed":          false,
		"model_id":        modelID,
		"token_used":      0,
		"token_limit":     0,
		"cost_cny":        0.0,
		"memory_mb":       memMB,
		"memory_limit_mb": 8192,
		"agent_id":        agentID,
		"agent_state":     agentState,
		"agent_config":    agentConfig,
	})
}
