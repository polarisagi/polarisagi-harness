# 模块 13: Interface & Scheduler

> 对外: CLI + HTTP/SSE + MCP + Web UI; 对内: 任务队列 + 定时任务 + HITL
> Go; [HE-Rule-1]; [Tier-0-Limit]; [Phase0-Bootstrapping]
> **§跳读**: 0-bis:6 职责 / 0-ter:21 不变量速查 / 1:34 对外接口 / 2:259 对内调度 / 3:367 MCP / 6:378 (SOFT)降级 / 7:395 跨模块契约 / 8:412 Web UI 规约
## 0-bis. 职责边界

| M13 **是** | M13 **不是** |
|-----------|-------------|
| 对外接口层（CLI REPL + HTTP REST + SSE + Web UI） | 业务逻辑执行（由各模块负责） |
| 对内调度（TaskQueue + Cron + ResourceReaper） | 任务分解与编排（那是 M8） |
| HITL 审批网关（HITLGateway + Notifier） | 审批策略制定（那是 M11 [ESCALATE]） |
| TrafficSplitter 流量分发执行（percent 控制） | 流量切换决策（那是 M9 ProgressiveRollout） |
| ResourceGovernor 准入控制（三级降级联合执行） | 内存压力检测（那是 M3 OSMemoryGuard） |
| Sealed/Unsealed 服务器状态管理 | KillSwitch 阶段触发（那是 M11） |
| 对外 API 认证（Session Token + API Key） | 凭证存储（那是 M11 CredentialVault） |
| EgressGateway Provider 域名白名单预检 | 网络连接安全（委托 M11 SafeDialer.DialContext 完整执行） |

---

## 0-ter. 不变量速查表

| 编号 | 不变量 | 验证方式 |
|------|--------|---------|
| inv_M13_01 | 默认绑定 127.0.0.1——远程绑定须显式 + TLS + capability + audit | M13 §1.2.1 AuthMiddleware |
| inv_M13_02 | 单机优先——不引入 Kafka/Redis/RabbitMQ/K8s | 架构硬约束审计 |
| inv_M13_03 | Worker pool 严格隔离——intent_handler/eval/ingest/background/cron 独立 pool | M13 §2.0 ResourceGovernor |
| inv_M13_04 | HITL 审批超时触发 timeout policy——kill_pause（默认）/ auto_deny / auto_approve | M13 §2.4 ApprovalRequest |
| inv_M13_05 | ResourceGovernor 与 M3 OSMemoryGuard 共享三级降级阈值——任一触发即执行 | M13 §2.0 三级降级 |
| inv_M13_06 | 所有出站请求经 EgressGateway → M11 SafeDialer.DialContext 完整五阶段 SSRF 防护 | M13 §1.2.2 EgressGateway |

---

## 1. 对外接口

### 1.1 CLI

```
polaris query "..." / chat / serve / config get|set <k> <v> /
        config history / config revert <version> / config diff <v1> <v2> /
        cron list|add|remove / sessions list|switch|resume <id> / status / doctor /
        export [--output polaris-backup-YYYYMMDD.jsonl] / import <backup.jsonl> /
        tool quarantine <toolID> /
        migrate openclaw [--dry-run] [--with-memory] [--smart] [--stage] /
        memory process-staging
```

AgentREPL:
  组成: agent (*Agent), session (*Session), history (*REPLHistory)
  Run: 1. 欢迎提示 2. 逐行读 stdin 3. "/"→handleCommand; 否则→订阅 agent.StreamInfer 4. EventToken→stdout; EventToolCall→"calling {name}"; EventThinking→思考; EventComplete→结束
  handleCommand: /help /sessions /switch <id> /skills /memory /status /quit

配置版本控制: 用户配置每次变更原子记录到 events 表（source_type='user_config_change'），享受 EventLog 完整审计 + 回滚能力。`polaris config history` 显示变更历史；`polaris config revert <version>` 回退；`polaris config diff <v1> <v2>` 对比差异。

数据导出/迁移: `polaris export [outfile.jsonl]` 流式导出 `chat_sessions`、`chat_messages` 和 `kv_store` (config 前缀)。`polaris import <infile.jsonl>` 幂等 upsert 恢复。

**外部平台迁移**: `polaris migrate openclaw` 一次性迁移工具（`cmd/polaris/migrate_openclaw.go`）,从 OpenClaw 用户数据目录(~/.openclaw)导入配置、API 密钥、记忆和人设文件:
- 配置与密钥 → `configs/defaults.yaml` (key mapping, 低难度)
- SOUL.md/AGENTS.md → 系统 prompt + M5 记忆层 (均 Markdown, 低难度)
- 记忆 SQLite → EventLog (需 `--with-memory` 显式启用, schema 自省映射, 中难度)
  - 默认 `--stage` 模式: 记忆写入隔离命名空间 `topic=memory.openclaw.staging`, 低 `salience=0.3`
  - `--stage=false` : 跳过隔离, 直接写主线 `memory.openclaw`
  - `--smart` : LLM 启发式预压缩(按 session 分组摘要、去重、过滤低价值行), 降噪后写入
  - 迁移后运行 `polaris memory process-staging` 触发三阶段渐进吸收: 去重→Salience重算→topic提升到主线
- 技能 SKILL.md → 仅拷贝源码 + 标注"需人工 Logic Collapse 编译为 Wasm"(脚本→Wasm 无法自动转换, 高难度)
- ClawHub 技能注册表只读拉取: `--clawhub-url` 参数, 同源技能按同一规则处理

月度成本报告: cron 0 0 1 * * → 生成 monthly_cost_report.md，含 by_provider / by_task_type / by_session / by_call_type(llm|embedding) 四维度。`polaris config budget set <amount>` 配置 monthly_budget，写入 `kv_store`（键 `config:budget:monthly_usd`）。

### 1.2 HTTP REST API

完整路由见 `pkg/interface/server/server.go`。以下按业务域分组。

```
─── 系统 ───────────────────────────────────────────────
GET  /healthz                              健康检查
GET  /readyz                               就绪检查
GET  /v1/status                            系统状态（Agent 状态 + Token 统计 + 内存）
GET  /v1/doctor                            诊断报告
GET  /metrics                              Prometheus 指标

─── Agent 对话 ─────────────────────────────────────────
POST /v1/agent/query                       同步查询
POST /v1/agent/stream                      SSE 流式推送（text/event-stream）
POST /v1/agent/{taskID}/interrupt          [UserInterrupt] 中断（详见 §1.2.5，<200ms SLO）
GET  /v1/logs/stream                       实时日志 SSE（EventSource GET）

─── 会话管理 ───────────────────────────────────────────
GET    /v1/sessions                        列出会话
GET    /v1/sessions/{id}                   会话详情
DELETE /v1/sessions/{id}                   删除会话
GET    /v1/sessions/{id}/recap             会话摘要

─── 搜索与洞察 ─────────────────────────────────────────
GET  /v1/search                            全文搜索
GET  /v1/insights                          系统洞察报告

─── Provider 与模型 ────────────────────────────────────
GET    /v1/providers                       列出 Provider
POST   /v1/providers                       创建 Provider
PUT    /v1/providers/{providerID}          更新 Provider
DELETE /v1/providers/{providerID}          删除 Provider
POST   /v1/providers/{providerID}/test     测试 Provider 连通性
GET    /v1/providers/{providerID}/models   列出模型
POST   /v1/providers/{providerID}/models   添加模型
PUT    /v1/providers/{providerID}/models/{modelID}    更新模型
DELETE /v1/providers/{providerID}/models/{modelID}    删除模型

─── 配置 ───────────────────────────────────────────────
GET  /v1/config                            读取运行配置
GET  /v1/config/model-roles               读取模型角色映射
PUT  /v1/config/model-roles               更新模型角色映射

─── 工具与技能 ─────────────────────────────────────────
GET  /v1/tools                             列出已注册工具
GET  /v1/tools/schemas                     工具 JSON Schema
GET  /v1/skills                            列出已安装技能
POST /v1/tools/{name}/execute              直接执行工具
POST /v1/skills/install                    安装技能（接受 Wasm 载荷或源码）

─── MCP Server ─────────────────────────────────────────
GET    /v1/mcp-servers                     列出 MCP Server
POST   /v1/mcp-servers                     注册 MCP Server
PUT    /v1/mcp-servers/{serverID}          更新 MCP Server
DELETE /v1/mcp-servers/{serverID}          删除 MCP Server
POST   /v1/mcp-servers/{serverID}/test     测试 MCP Server 连通性

─── 插件与市场 (Marketplace) ──────────────────────────────
GET    /v1/plugins/catalog                 读取聚合市场目录缓存（MCP/Skill/Plugin/App）
POST   /v1/plugins/sync                    异步克隆并解析远程市场配置
GET    /v1/plugins/marketplaces            获取已订阅的市场列表
POST   /v1/plugins/marketplaces            添加订阅的远程市场
DELETE /v1/plugins/marketplaces/{id}       删除订阅市场
POST   /v1/plugins/install                 安装并使能目录项
DELETE /v1/plugins/{catalogID}             卸载已安装目录项

─── 渠道（Channel）────────────────────────────────────
GET    /v1/channels                        列出渠道
POST   /v1/channels                        创建渠道
PUT    /v1/channels/{channelID}            更新渠道
DELETE /v1/channels/{channelID}            删除渠道
POST   /v1/webhooks/{channelType}/{channelID}   Webhook 入站接收

─── HITL 审批 ──────────────────────────────────────────
GET  /v1/approvals/pending                 [ESCALATE] 待审批列表
POST /v1/approvals/{id}/resolve            [ESCALATE] 审批决定

─── 定时任务 ───────────────────────────────────────────
GET    /v1/cron                            列出 Cron 任务
POST   /v1/cron                            创建 Cron 任务
PUT    /v1/cron/{jobID}                    更新 Cron 任务
DELETE /v1/cron/{jobID}                    删除 Cron 任务

─── 评测 ───────────────────────────────────────────────
POST /v1/eval/run                          触发 Eval 运行

─── 数据导出 ───────────────────────────────────────────
GET  /v1/export/trajectories               导出轨迹数据

─── OpenAI 兼容 ───────────────────────────────
POST /v1/chat/completions                  OpenAI 兼容端点（第三方客户端接入）
```

SSE 请求 (`POST /v1/agent/stream`): query(string,req) / session_id(string,opt) / model(string,opt) / temperature(float32,opt) / max_tokens(int,opt)
SSE 事件 (text/event-stream): "token" | "tool_call" | "tool_result" | "thinking" | "error" | "complete" → data: <JSON>\n\n

#### 1.2.1 认证

AuthMiddleware:
  1. X-Session-Token → 匹配本地 Bearer Token (~/.polaris-harness/.session_token 0600) → 放行; loopback 不免密 → 401
  2. X-API-Key → [CredentialVault] KeychainProvider.Verify SHA-256 常量时间比较 → 失败 401
  公网: JWT Ed25519 + TLS 1.3; 3 次失败 → IP 冷却 5min

#### 1.2.2 Egress Gateway

HTTP 层出站适配器，委托 M11 SafeDialer（M11 §6 统一安全 Dialer）执行完整 SSRF 防护。本层仅维护 Provider 域名白名单作为预检（api.deepseek.com, api.anthropic.com, api.openai.com, api.github.com, localhost）——不在白名单的域名提前拒绝，减少 SafeDialer DNS 查询开销。实际连接（DNS 解析、CIDR 校验、TOCTOU 消除、IP 锁定）全部由 M11 SafeDialer.DialContext 统一执行。扩展: /config network allow example.com:443（追加白名单，仍需经 SafeDialer 完整校验）。

#### 1.2.3 Sealed/Unsealed

ServerState: ServerSealed(0) / ServerUnsealed(1)
Sealed 态: 仅 /v1/admin/unseal + /healthz; 业务 503; 定时器冻结; worker 挂起
Unseal: 1.密码/[CredentialVault]解锁 2.KMS 解密主密钥→凭证注入 M1 3.健康检查→开放端点→启动 worker pool
SealedMiddleware: ServerState==Unsealed→next; 否则非 unseal/healthz→503

#### 1.2.4 优雅关闭

Server.Shutdown:
  1. 禁用 Keep-Alive, 停止新连接
  2. 30s context 等待进行中请求
  3. http.Server.Shutdown(ctx)
  4. SSE 客户端连接随 context 取消自然断开
  5. scheduler.Drain()
  6. reaper.RunNow()
WaitForShutdown: SIGINT/SIGTERM→Shutdown→失败 os.Exit(1)

#### 1.2.5 `[UserInterrupt]` 端点（inv_global_08，<200ms）

```
POST /v1/agent/{taskID}/interrupt
Body: { action: "resume" | "redirect" | "abort", instruction?: string }
```

**协议**: Auth(X-Session-Token) → MutationBus 写 pending → EventLog 推 `agent_interrupt_requested` → M4.ContextCancel()（取消所有子 goroutine）→ 202 立即返回（不等 S_INTERRUPT 确认）。跨 session 中断需 `interrupt_remote` Capability。

**SLO**: 端点接收 → ContextCancel 完成 < 200ms（M3 `polaris_user_interrupt_latency_ms` Histogram 监控）

**action 语义**:
- `resume`: instruction 注入 ZoneImmutable（[TaintLevel]=TaintUserReviewed, source='user_interrupt'），Agent 恢复原状态继续
- `redirect`: 跳转 S_PLAN 重新规划，不消耗 ReplanCount
- `abort`: 进入 S_FAILED + Saga 逆序补偿 + workspace GC

**约束**:
- 与 [KillSwitch] 同等优先级但作用域为单 task；KillSwitch FULLSTOP 覆盖所有 task，UserInterrupt 仅当前 taskID
- 同 task 30s 内重复中断 → HTTP 429（防抖）
- task 不存在 / 已 S_COMPLETE/S_FAILED → HTTP 404

### 1.3 WebSocket [计划：可选升级路径]

> 当前版本推送走 SSE（`GET /v1/logs/stream` + `POST /v1/agent/stream`），client→server 走 REST。
> 以下为 WebSocket 升级方案的设计约束，实现时以此为规范。

HandleWebSocket:
  1. upgrader 升级
  2. 读 goroutine: ClientMessage JSON→Intent→Agent.Input
  3. 写循环: 订阅 event channel→发送队列 cap=256→WriteJSON(5s 超时)
  4. 队列满背压（cap=256）:
     Critical（不可丢弃）: tool_call_started/tool_result/error/approval_required/task_completed/task_failed
     Streaming（可丢弃）: token/thinking
     Critical 满: 扫队尾合并 Streaming 腾 slot；无可合并 → Force Disconnect + [EventLog] 回放
     Streaming 满: 队尾找最近 Streaming 合并（Go struct 层面，严禁 []byte 拼接）；严禁 drop-oldest
     polaris_ws_coalesced_events Counter
  5. defer Unsubscribe

ClientMessage: Type(string:"query"/"interrupt"/"approval_response") / Payload(json.RawMessage) / SessionID(string,opt)
Intent: SessionID(string) / Action(Query/Interrupt/ResolveApproval) / Input(string) / Metadata(map[string]string)
WSEvent: Type(string) / Data(interface{}) / Timestamp(int64)

### 1.4 Web UI

> 实现规约已迁出为独立文档，见 §8 / M13-Interface-WebUI.md。

ServeWebUI:
- `DEV_MODE=1` → 反向代理 Vite dev server (`:5173`)
- 生产 → `http.FileServer(http.FS(subFS))` 挂载 `go:embed all:dist`

FeatureGate: `FeatureWebUI` 控制是否注册 `/` 路由。关闭时仅 REST API 可用（API-only 模式），不影响 CLI 功能。见 M03 §5。

### 1.5 Rate Limiting

RateLimiterMiddleware:
  Token Bucket GCRA, 双层隔离 (进程指纹+client_type 复合键)
  fingerprint: 本地→PID+启动时间 hash; 远程→M11 Ed25519 AgentIdentity 公钥 hash
  熔断: 连续 3 个 1s 窗口>100%配额→隔离 30s (429+Retry-After:30)
  配额(per fingerprint+client_type): CLI 50/s; WebUI 30/s; A2A 30/s; /_admin/ 10/s
  响应: HTTP 429+Retry-After

---

## 2. 对内调度

### 2.0 Resource Governor

ResourceGovernor (`pkg/edge/scheduler/scheduler.go`，与 M3 共享统一资源降级体系，**阈值权威来源: `spec/state.yaml §thresholds.memory_pressure`**，通过 `config.LoadMemoryPressureThresholds()` 加载，与 M3 OSMemoryGuard 共享同一配置节，禁止独立硬编码):
  字段: maxConcurrent(int) / inFlight(atomic.Int32) / cpuThreshold(float64,70%) / memThresholdMB(int,1024MB,**对应 L2 紧急阈值**（空闲 <1.0GB）,[Tier-0-Limit])
  注: `memThresholdMB=1024MB` 是 Admit step 2 的快速拒绝门限（对应三级降级中的 L2 紧急，而非 L3 临界 512MB）。用于"粗筛"非用户交互任务；L3 临界（512MB）由 OSMemoryGuard.CurrentPressureLevel() == Critical 触发，在 step 3 双层校验中体现。

  Admit(priority int, estimatedCostMB int):
    1. priority=0→始终放行
    2. CPU>cpuThreshold 或空闲<memThresholdMB→拒绝非用户交互
    3. >50MB 任务双层校验: (a)OSMemoryGuard.CurrentPressureLevel() 30s 滑窗 (b)runtime.ReadMemStats+sysinfo.FreeMemory() 瞬时; Normal 且瞬时>512MB→放行; Elevated→仅 priority<=1; Critical→拒绝所有非用户交互
    4. inFlight>=maxConcurrent→优先级降序抢占
    5. inFlight++; 返回 AdmitResult{Allowed:true}
  Release: inFlight--; 空闲>memThresholdMB+256MB 回滞→Cond.Broadcast()

  降级(三级统一，与 M3 OSMemoryGuard 共享阈值):
    L1 (预警): 空闲 <1.5GB 或 CPU >70% → 拒绝 priority>=3
    L2 (紧急): 空闲 <1.0GB 或 CPU >85% → 拒绝 priority>=2
    L3 (临界): 空闲 <512MB 或 OOM → 拒绝 priority>=1 + 通知 M3

  **local_only 死锁恢复**: `local_only` 模式下，OSMemoryGuard 可能因内存压力强制卸载本地 LLM（M1 §12），导致所有 Agent 任务因无法调用 LLM 而堆积在 Pending/Retrying 状态（持续占用内存），形成"内存不足 → 卸载 LLM → 任务堆积 → 无法释放内存 → 无法重载 LLM"的相互死锁。ResourceGovernor 在以下条件同时满足时触发主动回收:
    (a) 当前为 `local_only` 模式（M1 LocalProvider.Probe() 返回 ErrLocalModelUnavailable 持续 > 30s）
    (b) OSMemoryGuard.CurrentPressureLevel() >= L2（紧急）
    (c) 待处理任务数 > 0
    → 通知 M8 Orchestrator 强制将 Priority >= 2 的非核心任务回退（Rollback）至 Suspended 状态（附加 oom_evicted 标志）以释放内存，Priority=1 的前台辅助任务同理降级为 Suspended 并写入 Cold Archive（M2 §3 WorkspaceManager）。每释放 256MB 空闲内存后重试 LLM 重载（M1 LocalProvider.Probe()）。若全部非 Priority=0 任务已清理仍无法重载 → 通知用户通过 HITL 手动介入（M13 §2.4）

### 2.1 任务队列与全局并发信号量

TaskStore: Enqueue/Dequeue/MarkComplete/MarkFailed/ListPending/Close
  实现: SurrealDBTaskStore(key:task:{id}) / SQLiteTaskStore; [Tier-0-Limit] 非热路径
  类型定义: `pkg/edge/scheduler/scheduler.go`（ResourceGovernor / TaskQueue）、`pkg/edge/hitl/gateway.go`（HITLGateway）、`pkg/edge/scheduler.go`（TrafficSplitter，该文件余量已 Deprecated，迁移至 `pkg/edge/scheduler/` 中）

**Global Semaphore (并发限制)**:
在 M13 引入全局跨模块并发控制信号量（Global Semaphore）。确保 LLM 推理节点（M1/M4/M9 均会调用 LLM）的**总并发度**上限不超出硬件或 API 的物理承载极限。当多个 worker (Agent, BackgroundTask, Cron 等) 争抢 LLM 资源时，通过 M13 Global Semaphore 进行排队，防止过载打爆本地 GPU/内存 或触发 API 供应商 429。默认配置 Tier 0 仅允许 GlobalSemaphore=1，Tier 1+ 视配置（`config.GlobalConcurrency`）决定。

TaskQueue 交付语义: at-least-once, 幂等键 = ScheduledTask.ID。

### 2.2 定时任务

Scheduler.Start:
  每1h: memorySystem.RunConsolidation
  每天 02:00: evalRunner.RunNightly
  每5min: killSwitch.CheckAndAct([TokenBurnRate])
  每周日 03:00: memorySystem.CheckEmbeddingDrift
  每30min(空闲): selfImprove.IdleLoop

### 2.3 Resource Reaper

ResourceReaper:
  组成: storageFabric, skillLibrary, memorySystem; minInterval(24h)
  Reap(前置 isDeepIdle();超6:00未获→M9 SuspendAll→清理→ResumeAll→M11 Audit):
    1. PruneDeprecatedSkills: 30天未检索+成功率<30%→删除 Wasm
    2. PruneOrphanEntities: 无入/出边+90天未更新
    3. PruneWorkspaceFiles: >7天且关联 Task Status∈{Done,Failed}→`os.RemoveAll(workspace/<task_id>/)`; 按 CreatedAt 升序回收至 < maxSize×0.7; 紧急模式(写入拦截触发)→同步 RunNow，跳过定时等待
    4. CompressColdArchive: >180天 JSONL→zstd
    5. storageFabric.Vacuum
    6. storageFabric.CompactSurrealDB
    7. storageFabric.RebuildStaleIndexes
  注册: "0 4 * * *"

### 2.4 HITL ([ESCALATE])

HITLGateway: pending(map[string]*ApprovalRequest)/store(*HITLStore)/notifier(Notifier)/killSwitch([KillSwitch])/auditLog

Notifier: NotifyApproval(req)/NotifyResolved(req,outcome)
  实现: SlackNotifier(Webhook)/EmailNotifier(SMTP)
  重试: 3次(100ms→500ms→2s);失败→回退 Slack→Email;Email→本地(chat:stderr+BEL;serve:syslog CRITICAL+Web UI /_admin/alerts)
  上限: min(HITL timeout×10%,2min);确定性失败不重试

HITLStore: Persist(INSERT OR REPLACE)/UpdateStatus(UPDATE status/comment/resolved_at)/LoadPending(SELECT WHERE status='pending')

ApprovalRequest:
  ID/AgentID/Action(string)/Detail(string)/RiskLevel(string)/CreatedAt/Timeout(30min,Per-TaskType可配置)
  TimeoutPolicy: kill_pause(默认)/auto_deny/auto_approve(policy_version>=2+管理员授权)
  Status: pending/approved/denied/timeout

  auto_approve 硬编码约束(internal/config/immutable_constants.go,CI 不可变内核):
    禁止: write_network, privileged, delete_data, execute_system, modify_policy
    白名单: read_local_file, log_rotate, cache_evict, stats_collect
    [Taint-Medium]感知: ActiveContext.TaintLevel>=[Taint-Medium]→auto_approve 失效,升级 HITL
    敏感路径 glob: **/.env*, **/*id_rsa*, **/*.pem, **/credentials*, **/*.key, **/secret*, **/.ssh/**, **/.aws/**, **/.gcloud/**, **/kubeconfig*
    Symlink 防御: filepath.EvalSymlinks(filepath.Abs(filepath.Clean(path))) 先于 glob
    原子 etag 校验: auto_approve 放行决策到 JIT Token 签发为临界区——签发前原子比对当前 [Cedar-Gate] policy_version/etag 与决策时刻记录的 decision_etag。etag 不一致 → 决策上下文已过期，拒绝 auto_approve，操作升级为 HITL 审批。此校验防止 auto_approve 放行后数毫秒内 Cedar 策略热更新导致 Token 刚签发即被 M7 L3PolicyMonitor 掐断（M7 §4.7），避免产生难以归因的 `l3_policy_revoked_network_killed` 审计震荡

RequestApproval:
  1. 加入 pending 2. 持久化 SQLite 3. Notifier 发送 4. [EventLog] Subscribe ApprovalResolved
  5. 等待: 收到→nil/ErrApprovalDenied; 超时→用户交互:ErrApprovalTimeout+S_ROLLBACK(不触发[KillSwitch]); 后台:auto_deny

ResolveApproval:
  1. pending 查找→Approved/Denied+Comment 2. 持久化+审计 3. [MutationBus]→[EventLog]

ReloadOnStartup:
  1. 加载 pending→超时检查 2. 超时→ApprovalTimeout;未超时→加入 pending+重启计时器
  3. notification_sent=false→重通知(dedup:SHA-256(requestID+retry_seq))
  4. 重通知>50→仅最近50+其余 needs_manual_review+M3 CRITICAL

### 2.5 TrafficSplitter

AgentVersion: Version(string)/Type(baseline/candidate/shadow/rollback)/ConfigRef/PromptSetRef/SkillSnapshotID/ModelID/CreatedAt/EvalResults(*EvalRunReport,opt)

TrafficSplitter: baseline(*AgentVersion)/candidate(*AgentVersion)/percent(atomic.Int32)
  Route: percent<=0→baseline;>=100→candidate;else SessionID hash%100<percent→candidate
  SetPercent: atomic.Store; Rollback: atomic.Store 0+告警
  分工: M9 决策+回滚检测; M13 执行; M12 对比

---

## 3. MCP

StartMCPServer: 1.创建 Server(name="polaris-agent",v0.1.0) 2.注册 execute_skill/search_knowledge(InputSchema) 3.注册 memory://episodic/recent 4.StdioTransport

MCPServerConfig: Name/Command/Args([]string)/Env(map[string]string)/AutoConnect(bool,true)/Timeout(30s)
MCPConfig: Servers([]MCPServerConfig)

ConnectMCP: 1.遍历 Servers 2.CommandTransport exec.Command→MCP 会话 3.session.ListTools→Tool{Name,Source=SourceMCP,SourceURI=server.Name}→toolRegistry

---

## 6. 降级与失败模式

| 故障场景 | 降级路径 | 恢复策略 |
|---------|---------|---------|
| HTTP server 阻塞/超载 | 限流 (429+Retry-After) + 504 超时 | 负载降低后恢复 |
| SSE 连接断开 | 客户端 EventSource 自动重连（指数退避） | — |
| TaskQueue SurrealDB-Core 持久化失败 | 降级纯内存队列 (at-most-once) + CRITICAL 告警 | SurrealDB-Core 恢复后切回 at-least-once |
| Cron 定时器错失 | gocron 自动补偿 (错失→立即执行一次) | — |
| HITL Notifier 全部通道失败 | 本地回退 (chat:stderr+BEL / serve:syslog CRITICAL) | — |
| ResourceGovernor 拒绝新任务 (L3 临界) | 503 + Retry-After | 内存恢复后 Admit 放行 |

与 OSMemoryGuard 协同: ResourceGovernor 实时读取 OSMemoryGuard.CurrentPressureLevel() → Admit 准入。shared semantics: L1/L2/L3 阈值同源(见 00-Global-Dictionary §1-ter XR-07)。

## 默认参数

完整阈值与重评触发条件: `spec/state.yaml §thresholds.m13_scheduler`。最终值落 `config/m13.toml`。

## 7. 跨模块依赖与契约

| 关联模块 | 关键契约 | 位置 |
|---------|---------|------|
| M1 Inference | Provider 路由（API 调用 → SafeDialer 出口）| M1 §4 |
| M2 Storage | EventLog 持久化、MutationBus 串行写 | M2 §2.1, §2.3 |
| M3 Observability | OSMemoryGuard 三级降级共享阈值、ResourceGovernor 联合决策 | M3 §6, M13 §2.0 |
| M4 Agent Kernel | Agent Intent 输入（CLI/HTTP → Agent.Input）| M4 §2 |
| M8 Orchestrator | HITL 挂起/恢复、TrafficSplitter 流量分发 | M8 §1.5, M13 §2.5 |
| M9 Self-Improve | ProgressiveRollout 执行分发、ResourceReaper 闲时清理 | M9 §2.3, M13 §2.3 |
| M11 Policy Safety | KillSwitch FullStop → SealedMiddleware 503、SafeDialer 网络出口 | M11 §4, §6 |
| 接口定义 | SafeDialer/Blackboard/TaskEntry/ScheduledTask | internal/protocol/interfaces.go, types.go |
| 全局字典 | ESCALATE/KillSwitch/SSRFGuard 定义 | 00-Global-Dictionary §3, §4 |
| 时序图 | KillSwitch 触发链（M13 SealedMiddleware 503 响应）| DIAGRAMS.md#killswitch |

---

## 8. Web UI 规约

> 前端实现的单一权威源。栈: Alpine.js + Tailwind CSS v4 + Vite 6 + go:embed + marked | [FeatureGate.FeatureWebUI] | [Tier-0-Limit]

### 8.1 目录结构

```
web/
├── embed.go                  # go:embed all:dist → WebUIFS embed.FS
├── vite-plugin-fragments.js  # 构建时展开 <page-fragment src="…"> 为内联 HTML
├── src/
│   ├── index.html            # 壳页（侧边栏 + 10个page-fragment + 浮动日志抽屉）
│   ├── pages/                # 页面 HTML 片段
│   │   ├── chat.html         # 新对话（含权限模式下拉，绑定 $store.computer）
│   │   ├── sessions.html     # 会话历史（独立）
│   │   ├── search.html       # 全文搜索（独立）
│   │   ├── skills.html       # 已安装技能列表（独立）
│   │   ├── plugins.html      # 插件聚合大市场（插件/应用/MCP/技能/市场）
│   │   ├── automation.html   # 聚合Tab：日常定时任务 · 触发器 · 待办审批
│   │   ├── monitor.html      # 聚合Tab：状态 · 洞察 · Agent
│   │   ├── settings.html     # 聚合Tab：提供方 · 渠道 · 配置 · 电脑操控
│   │   └── eval.html         # 评测套件（独立）
│   ├── js/
│   │   ├── app.js            # Alpine stores 入口 + marked 配置 + URL路由初始化
│   │   ├── sse.js            # SSEClient（fetch+ReadableStream + 指数退避重连）
│   │   ├── utils.js          # 文本过滤与通用工具
│   │   ├── i18n.js           # 中/英 i18n 数据
│   │   └── store/            # 按域拆分的 Alpine 状态管理
│   │       ├── chat.js       # 对话状态机
│   │       ├── logs.js       # 日志 SSE + 抽屉状态
│   │       ├── nav.js        # 页面路由 + 侧效触发
│   │       ├── statusBar.js  # 顶栏轮询
│   │       └── …（approvals/sessions/skills/plugins/providers/channels/config/
│   │              agents/insights/cron/eval/search/onboard/toast/i18n/modelRoles/computer）
│   └── css/style.css         # Tailwind v4 入口（@import "tailwindcss"）+ 主题色 token
├── package.json              # 生产依赖: alpinejs + marked；开发: @tailwindcss/vite + vite
└── vite.config.js            # Vite 构建配置
dist/                         # Vite 输出（gitignore；make build-ui 生成）
```

---

### 8.2 页面结构

### 2.1 导航架构（已实施）

侧边栏 **10 个入口**，3 个聚合页通过 Tab 内嵌子视图，日志独立为浮动抽屉：

| 导航入口 | 路由 | 子 Tab / 说明 | 主要 API |
|----------|------|--------------|---------|
| 新对话 | `/` | 权限模式下拉内联于输入区 | `POST /v1/agent/stream` (SSE) |
| 搜索 | `/search` | — | `GET /v1/search` 提交触发 |
| 会话 | `/sessions` | — | `GET /v1/sessions` |
| 插件 | `/plugins` | 聚合Tab：插件 · 应用 · MCP · 技能 · 市场，统一管理与检索订阅 | `/v1/plugins/catalog` · `/v1/plugins/sync` |
| 自动化 | `/automation` | 聚合Tab：日常定时任务 · 外部触发器 · 人工待办审批 (HITL) | `/v1/cron` · `/v1/approvals/pending` |
| 监控 | `/monitor` | Tab: 状态 · 洞察 · Agent | `/v1/status` 轮询 · `/v1/insights` · agent_state/agent_config |
| 设置 | `/settings` | Tab: 提供方 · 渠道 · 配置 · 电脑操控 | `/v1/providers` · `/v1/channels` · `/v1/config` · `/v1/preferences` |
| 评测 | `/eval` | — | 提交触发 |
| **日志抽屉** | （FAB 唤出，不占导航位） | 浮动侧滑面板 | `GET /v1/logs/stream` (EventSource) |

### 2.2 旧路由兼容映射

`app.js` 的 `legacyPageMap` 在 DOMContentLoaded 时将旧 URL 重定向至新聚合页，无需服务端 redirect：

```js
const legacyPageMap = {
  status: 'monitor', insights: 'monitor', logs: 'monitor',
  providers: 'settings', channels: 'settings', config: 'settings',
  approvals: 'automation', cron: 'automation',
  agents: 'monitor', skills: 'plugins',
  capabilities: 'plugins', computer: 'settings',
}
```

### 2.3 Tab 懒加载策略

聚合页内各子 Tab 首次激活时才触发数据加载，通过页级 `x-data` 中的 boolean flag 实现去重：

```html
<!-- 示例：settings 页 channels tab 首次激活时加载 -->
x-data="{ tab: 'providers', _chLoaded: false, _cfgLoaded: false }"
@click="tab='channels'; if(!_chLoaded){ $store.channels.load(); _chLoaded=true }"
```

**例外**：Providers 和 ModelRoles 在 `nav.navigate('settings')` 时即刻预加载（首屏需要），其余 Tab 惰性加载。

---

### 8.3 核心协议与渲染

> 本节描述实现细节；UI **硬规则**集中在 §8.5 `inv_webui_NN` 表（单一 grep 源）。下方 bullet 标注的 `(inv_*)` 指向对应硬规则。

### 3.1 Chat SSE 渲染

- **流端点**: `POST /v1/agent/stream` (`text/event-stream`)。因需 POST body，使用 `fetch+ReadableStream`（`sse.js:SSEClient`），非原生 `EventSource`。
- **状态机** `(inv_webui_08)`: `IDLE → SUBMITTING → THINKING → STREAMING → TOOL_RUNNING → STREAMING → COMPLETE → IDLE`。
- **异常退避**: 1s→2s→4s→8s→16s→30s，超 10 次转 ERROR。
- **中断恢复**: 接收 `error(interrupted)`，保留 `currentTokens` 并打橙色"⚠ 已中断"徽章，消息持久化入列。
- **幂等去重** `(inv_webui_10)`: `dedupeRunID(sessionID, input)` 在 5s 窗口内对相同 (sessionID, input) 返回同一 runID，防止重复提交。

### 3.2 文本规范化与 Markdown

- **安全沙箱** `(inv_webui_09)`: `marked` 渲染，白名单过滤 HTML（清 `script`, `on*`, `javascript:`），所有 `<a>` 强制 `rel="noopener noreferrer"`，确保 XSS 安全。
- **静默过滤**: `sanitizeContent()` 剥除 XML (`<tool_call>`), `[[reply_to_*]]`, `NO_REPLY`。
- **思维剥离**: `event:thinking` 仅入 `ThinkingPanel`，不混入用户消息气泡。

### 3.3 日志 SSE 流（浮动抽屉模式）

- 使用原生 `EventSource`（GET，无 POST body 需求）连接 `GET /v1/logs/stream`。
- 连接生命周期绑定抽屉开关 `(inv_webui_06)`：`openDrawer()` 触发 `connect()`，`closeDrawer()` 触发 `disconnect()`，**避免后台常驻 SSE 连接**。
- 环形缓冲 1000 条，满则截断至 500 条。
- 3s 固定间隔断线重连（无退避，低频日志容忍延迟）。
- 级别过滤通过 `levelFilter` 参数重新建立连接（`setLevel()` → `connect()`）。

---

### 8.4 UI 组件与交互

| 组件/交互 | 触发/机制/结果 |
|----------|--------------|
| **StatusBar** | 10s 轮询 `/v1/status`。Token 烧率 >80% 出橙色徽章，>95% 红色告警并提醒将压缩。 |
| **ApprovalCard** | 风险分级色阶（蓝/橙/红），含闪烁倒计时。任务导航项显示待审数量角标。 |
| **CompactionDivider** | 遇压缩检查点 `at_message_id` 注入占位块，支持从此分支发起新会话。 |
| **OnboardWizard** | 首次引导（DOMContentLoaded 后 400ms 延迟检查）。配置 Provider/Model/Channel 三步流程。 |
| **浮动日志抽屉** | 右下角 FAB 按钮唤出，CSS `transform: translateX` 侧滑动画。打开时建立 EventSource，关闭时断开 `(inv_webui_06)`。未读日志时 FAB 显示绿点。 |
| **Tab 内嵌导航** | Monitor/Settings/Tasks/Capabilities 四页内嵌 `.tab-bar`。Tab 面板用 `x-show`（非 `x-if`）保留 DOM 状态 `(inv_webui_11)`，避免 `setInterval` 在切换时重置。 |
| **键盘快捷键** | `Enter` 提交，`Shift+Enter` 换行，`Ctrl+C` 中断流，`/` 唤出斜杠补全。 |
| **主题切换** | `--color-surface` 变量系（Tailwind `@theme`）。支持 system/dark/light/terminal，持久化至 localStorage。 |
| **语言切换** | `$store.i18n.setLang('zh'|'en')`，i18n 数据集中在 `js/i18n.js`，涵盖全量 UI key。 |

---

### 8.5 构建与不变量

**构建/运行**:
- 生产: `make build-ui` → `npm install && npm run build` → 输出至 `web/dist/`。
- 开发: `make dev-ui` → Vite dev server `:5173`，代理 `/v1` 至 `:29999`。
- `make build` 自动先调 `make build-ui`（Rust FFI → Web UI → Go binary）。

**[Tier-0] 核心不变量** — UI 硬规则单一 grep 源；`(inv_webui_NN)` 在 §8.3/§8.4 处可交叉引用。

| ID | 约束 | 验证位置 |
|----|------|---------|
| `inv_webui_01` | `dist/` 不入 Git；`make build` 必先调 `make build-ui`。 | `web/.gitignore` + `Makefile` |
| `inv_webui_02` | npm `dependencies` 仅 `alpinejs` + `marked`。`devDependencies` 仅 `@tailwindcss/vite` + `vite`。零 CDN 依赖（内网离线可用）。 | `web/package.json` |
| `inv_webui_03` | `FeatureGate.FeatureWebUI=false` 或密封(`SEALED`)态时，API 拒服，UI 出强警告横幅。 | `pkg/substrate/observability/feature_gate.go` |
| `inv_webui_04` | 写操作携带 `X-Session-Token`（`sessionStorage.getItem('polaris_token')`）。 | `web/src/js/sse.js` + middleware |
| `inv_webui_05` | 轮询故障禁止静默（连续 3 次挂出 Warning Banner）。 | `web/src/js/store/statusBar.js` |
| `inv_webui_06` | 日志 SSE 连接不得常驻后台。必须随抽屉关闭而 `disconnect()`。 | `web/src/js/store/logs.js` |
| `inv_webui_07` | Tab 聚合页内每个子 Tab 的数据加载至多触发一次（lazy flag 防重）。 | `web/src/pages/*.html` x-data lazy flags |
| `inv_webui_08` | Chat SSE 状态机仅在 `IDLE → SUBMITTING → THINKING → STREAMING → TOOL_RUNNING → STREAMING → COMPLETE → IDLE` 路径迁移；禁跳态。 | `web/src/js/store/chat.js` 状态转移 |
| `inv_webui_09` | `marked` 渲染必经白名单过滤：清 `script` / `on*` 属性 / `javascript:` URI；`<a>` 强制 `rel="noopener noreferrer"`。零例外。 | `web/src/js/app.js` marked 配置 |
| `inv_webui_10` | `dedupeRunID(sessionID, input)` 在 5s 窗口对相同元组返回同一 runID；防双击/重提交。 | `web/src/js/store/chat.js:dedupeRunID` |
| `inv_webui_11` | Tab 内嵌导航必用 `x-show`（非 `x-if`），保留 DOM 状态防 `setInterval` 在切换时重置。 | `web/src/pages/{monitor,settings,automation}.html` |
