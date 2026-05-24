# 模块 7: Tool & Action Layer

> MCP 双向化 | 三级沙箱 | 能力分级 read_only→privileged | Go+Wasm(wazero) | [HE-Rule-2] [HE-Rule-5]
> CANONICAL SOURCE: 沙箱架构、wazero 实现、StreamingActionBus
> **§跳读**: 0-bis:6 职责 / 0-ter:18 不变量速查 / 1:29 MCP双向 / 2:75 A2A / 3:96 注册 / 4:110 三级沙箱(CANONICAL) / 5:250 PolicyGate / 6:303 Capability / 7:320 动作扩展 / 8:436 Usage演化 / 12:494 (SOFT)降级 / 13:512 跨模块契约 / 14:532 Plugin / 15:570 Hook
## 0-bis. 职责边界

- M7 **是**: 工具注册中心（ToolRegistry）+ 五大工具类别管理 | M7 **不是**: 工具的语义定义者（各模块注册自己的工具）
- M7 **是**: MCP 双向桥接（Host 消费 + Server 暴露） | M7 **不是**: MCP 协议实现（依赖 Go MCP SDK）
- M7 **是**: 沙箱分级执行（Sbx-L1/L2/L3） | M7 **不是**: 沙箱选型决策（那是 M11 PolicyGate）
- M7 **是**: Capability Token 签发与校验（沙箱门口） | M7 **不是**: 凭证长期存储（那是 M11 CredentialVault）
- M7 **是**: ExecuteTool 调用入口（含 SideEffectPreCheck） | M7 **不是**: 工具调用时机决策（那是 M4 状态机）
- M7 **是**: 技能 Wasm 执行（委托 wazero） | M7 **不是**: 技能发现与匹配（那是 M6 SkillSelector）
- M7 **是**: 所有出站网络连接强制经 M11 SafeDialer | M7 **不是**: 网络策略制定（那是 M11 SSRFGuard）

---

## 0-ter. 不变量速查表

- 编号: inv_M7_01 | 不变量: 所有工具（含 builtin）必须经 Capability Token 验证——禁止后门路径 | 验证方式: CI `no_backdoor_lint`
- 编号: inv_M7_02 | 不变量: MCP 获取内容默认 taint=high——trusted_sources 白名单例外 | 验证方式: M11 Connector-Taint-Table
- 编号: inv_M7_03 | 不变量: 沙箱选择不可被调用方手动覆盖——`AssignSandboxTier()` 由 M11 PolicyGate 决定 | 验证方式: 代码审计
- 编号: inv_M7_04 | 不变量: Capability Token 委托链最大深度 3——权限只能收缩不可放大 | 验证方式: M7 §4.6 ValidateDelegation
- 编号: inv_M7_05 | 不变量: 不可逆操作（write_network/privileged）执行前须 DryRun + HITL | 验证方式: M7 §5.3 Shadow Sink + §5.4 DryRunMode
- 编号: inv_M7_06 | 不变量: 所有出站连接强制经 M11 SafeDialer.DialContext——禁止裸 net.Dial/grpc.Dial/http.Get | 验证方式: CI `safe_dialer_lint`

---

## 1. MCP 双向架构

MCP Server 暴露端: `mcp.Server("polaris-agent" v0.1.0)` → 注册工具 `execute_skill`（skillLib.LookupByName→Wasm执行） + 注册资源 `memory://episodic/recent`（memory.Episodic().GetRecent(20)）

MCP Client 消费端: `ConnectExternalMCP(serverCmd)` → CommandTransport/StreamableHTTP → OAuth 2.1 + PKCE 认证（远程 server）→ ListTools → 发现外部工具

**MCP OAuth 2.1 + PKCE 流程**: Client → 重定向至 IdP → 带回 access_token → 注入 MCP request header `Authorization: Bearer <access_token>`。OAuth access_token 用于跨进程认证；Capability Token 用于本地沙箱内的能力收缩，两者分工明确。

**Streamable HTTP** 为默认远程传输层；SSE 仅向后兼容（legacy）。决策见 [ADR-0017](./decisions/ADR-0017-mcp-streamable-http-default.md)。

**MCP Transport 污点保护反序列化**：MCP Client 路径强制使用 `TaintPreservingDecoder`（`pkg/action/taint_preserving_decoder.go` + `pkg/extensions/mcp/mcp_client.go`），禁用 `encoding/json` 直解动态 schema——所有 string 叶子包装为 `TaintedString`（Source=MCP, Origin=server_name），初始 `[TaintLevel]` 按 M11 §2.4 `[Connector-Taint-Table]` 判定。决策与被驳回方案见 [ADR-0018](./decisions/ADR-0018-mcp-taint-preserving-decoder.md)。

统一错误映射（transport-agnostic）:

- 传输层: stdio | 典型错误: broken pipe, EOF, exit≠0 | Code: CONNECTION_LOST
- 传输层: stdio | 典型错误: stderr JSON-RPC error | Code: REMOTE_ERROR
- 传输层: SSE (legacy) | 典型错误: 连接超时>30s | Code: CONNECTION_TIMEOUT
- 传输层: Streamable HTTP | 典型错误: 连接超时>30s | Code: CONNECTION_TIMEOUT
- 传输层: Streamable HTTP | 典型错误: 无 close frame | Code: CONNECTION_LOST
- 传输层: HTTP | 典型错误: 502/503/504 | Code: REMOTE_UNAVAILABLE
- 传输层: HTTP | 典型错误: 4xx / 401 OAuth 过期 | Code: CLIENT_ERROR
- 传输层: HTTP | 典型错误: DNS/TCP 失败 | Code: CONNECTION_FAILED
- 传输层: gRPC (A2A) | 典型错误: UNAVAILABLE/DEADLINE_EXCEEDED | Code: REMOTE_UNAVAILABLE
- 传输层: gRPC (A2A) | 典型错误: PERMISSION_DENIED | Code: CLIENT_ERROR

重试: CONNECTION_LOST/FAILED/TIMEOUT→2次指数退避；CLIENT_ERROR→0；REMOTE_ERROR/UNAVAILABLE→1次

### 1.1 MCP 扩展原语与安全护栏 (Sampling/Roots/Elicitation/Prompts)

M7 桥接层对 MCP 扩展原语强制物理级护栏：

- **Sampling (Server 请求本地 LLM)**
  - Deny-by-default，仅 `allow_sampling` Capability Token 放行
  - Empty Context 隔离执行，禁止携带 ImmutableCore / Episodic
  - 产出强制 `[TaintHigh]`（禁入 instruction slot），Token 计入 Session 预算，受 M3 TokenBurnRate 熔断
- **Roots (Server 请求文件树边界)**
  - 严格收敛于 Sandbox `allowed_paths`，拦截真实路径，仅返回沙箱挂载点（如 `/tmp/sandbox/{skill_id}/`），防目录穿越与探活
- **Elicitation (Server 请求人工交互)**
  - 禁止阻塞宿主，封装为异步结构化意图 → M8 `[Blackboard]` → M13 HITLGateway 展示
  - 默认 5 分钟超时未响应 → 向 Server 返回异常，防会话死锁
- **Prompts (Server 提供预置模板)**
  - 视为不可信外部负载，强制 `ZoneTaintedData`
  - 初始 `[TaintLevel]`=High（白名单 Server → Medium），配合 Spotlighting 防间接 Prompt Injection

---

## 2. A2A Agent Card

```json
{
  "name": "Polaris Code Reviewer", "version": "1.0.0",
  "url": "http://localhost:9090/.well-known/agent-card.json",
  "capabilities": {"streaming": true, "pushNotifications": false},
  "authentication": {"schemes": ["bearer"]},
  "defaultInputModes": ["text", "file"], "defaultOutputModes": ["text", "file"],
  "skills": [{"id": "go-concurrency-review", "name": "Go Concurrency Review",
    "description": "Detects goroutine leaks, race conditions, and channel misuse",
    "tags": ["go", "concurrency", "code-review"]}]
}
```

Agent Card 服务端路径: `/.well-known/agent-card.json`（A2A v0.3+ 强制）。远程 Agent Card 签名校验见 M11 §2.6 VerifyExternalAgentCard。

单机: A2A 同进程黑板模式（M8）；跨机: HTTP/gRPC 端点。构建时按部署配置选择。

---

## 3. 工具注册系统

Tool/CapabilityLevel/SideEffect/RiskLevel/SandboxTier/ToolSource/ToolResult 类型定义见 `internal/protocol/types.go`。ToolRegistry 接口见 `internal/protocol/interfaces.go`。

Schema 版本化（防技能断裂）: 新增可选字段=Patch, 新增必填字段=Minor, 移除/重命名字段=Major。Minor/Patch 向后兼容；Major→Logic Collapse 重生成（`needs_adaptation`）。工具来源: Built-in(~20) | MCP(inf) | Skill(inf) | A2A(inf) | LLM-generated(临时，[Sandbox-L3])

**ToolRegistry.Quarantine(toolID, reason)**: 工具发现安全漏洞时紧急摘除——调用后该工具立即从可用列表摘除，所有进行中调用接收 ErrToolQuarantined。配套 CLI `polaris tool quarantine <toolID>`。

**OAuth Scope 字段**: Tool 类型补充 OAuthScope 字段，MCP 远程工具与 Cedar 策略关联:
  - `mcp:{server_id}:{scope}` token 格式
  - Cedar 策略可基于 scope 做 permit/forbid 决策

---

## 4. 三级沙箱架构（CANONICAL SOURCE）

### 4.1 Tier x Platform 能力矩阵

L3 平台选择由 `AutoConfig.computeSandboxConfig()` 自动化，`FeatureGate.FeatureL3Sandbox` 检测平台+内存。Tier 0 内存不足 → ErrFeatureUnavailable，不降级原生子进程。调用方仅检查 `GlobalFeatureGate().IsEnabled(FeatureL3Sandbox)`。详见 M03 §5。

L3 平台原生 microVM (统一 SandboxProvider 接口，调用方平台无感):
- **Linux**: Firecracker (~125MB/VM, 需硬件 KVM)；KVM 不可用 → gVisor (runsc) 用户态内核
- **macOS**: Virtualization.framework (~80MB/VM)
- **Windows**: WSL2 + Hyper-V (~150MB/VM)

Tier 0 L3 不可用: 全平台 Tier 0 内存不足启动 microVM (每 L3 ≥256MB)。CapWriteNetwork/Privileged 在 Tier 0 → ErrTier0SandboxLimit。**不提供原生子进程降级**（避免突破安全底线）。

**ContainerSandbox Linux 命名空间隔离**（`pkg/action/sandbox_linux.go`）:
当 SandboxRouter 路由至 `ContainerSandbox`（Tier1+ Linux）时，`cmd.SysProcAttr` 自动注入：
- `CLONE_NEWPID`：子进程获得独立 PID 命名空间，无法枚举/信号攻击宿主进程
- `CLONE_NEWNS`：独立挂载命名空间，防止子进程污染全局 mount 表
- `Pdeathsig=SIGKILL`：父进程退出时自动 SIGKILL 子进程，消灭孤儿
非 Linux 平台（`sandbox_other.go`）返回 nil，路由层已降级至 WasmSandbox，不到达此路径。
Landlock LSM 文件系统白名单（`LandlockRestrictSelf`）须在子进程内调用，需 reexec 模式（`POLARIS_SANDBOX_EXEC` 环境变量触发），为后续 Tier1+ 路线图项。

- Tier: [Tier-0-Limit] | 平台: 全平台 | L3 主方案: 不可用（内存不足） | L2 降级: 最高支持 L2 Wasm；L3 权限需求 → ErrTier0SandboxLimit
- Tier: [Tier-1-Limit] | 平台: Linux | L3 主方案: Firecracker（KVM 不可用 fallback gVisor，≥256MB） | L2 降级: L2 Wasm
- Tier: [Tier-1-Limit] | 平台: macOS | L3 主方案: Virtualization.framework（≥256MB） | L2 降级: L2 Wasm
- Tier: [Tier-1-Limit] | 平台: Windows | L3 主方案: WSL2（≥256MB） | L2 降级: L2 Wasm
- Tier: [Tier-2-Limit]+ | 平台: 全平台 | L3 主方案: 同 Tier-1-Limit，可分配更多内存（≥512MB） | L2 降级: L2 Wasm

`maxSandboxTier()`: M3 HardwareProbe 探测可用内存 + 平台原生虚拟化能力 → <256MB free → SandboxWasm(L2 上限) → ≥256MB + 平台 microVM 可用 → SandboxMicroVM(L3 平台原生)。Tier 0 对 L3 权限请求直接拒绝（ErrTier0SandboxLimit），不退化到 OS 原生子进程沙箱。

### 4.2 自动分级

`AssignSandboxTier(tool) -> SandboxTier`:
1. Source→最小级别: Builtin→InProcess；LLMGenerated→Wasm；MCP/A2A→Wasm
2. Capability提升: ReadOnly/WriteLocal/WriteNetwork→>=Wasm；Privileged→MicroVM(L3)
3. SideProcessSpawn→MicroVM(L3)
4. Tier0 越权拦截: 步骤 3 判定为 MicroVM(L3) 且当前环境为 Tier0 (maxSandboxTier()==L2) → 直接返回 ErrTier0SandboxLimit 拒绝执行，禁止越权降级到原生子进程（防止突破安全底线）

Auto-Curriculum: M9 `bash_restricted` 强制 L2 Wasm，字符集 `[A-Za-z0-9 ./\-_=:,]`，禁止管道/重定向/命令替换/`~/.polaris-harness`。`bash` 永久禁止。

### 4.3 wazero 实现（CANONICAL SOURCE）

`ExecuteTool` 流程:
- 0 PII SecureUnredact (执行边界 Redact→Opaque Token→Unredact):
  InputSchema `x-polaris-pii:true` 字段点对点还原 `args["email"]=vault.Resolve(token,fieldPath)`
  自由文本 command 字段不声明 → 永不扫描；外部 API 调用使用结构化 HTTP 工具 (`http.call` + JSON body schema)
  SessionPIIVault per-session，key=(sessionID,tokenID)，需 sessionID + pii_resolve 权限
  Vault 缺失或权限不足 → 保留原文 + WARN + 审计 unredact_permission_denied
- 1 ModuleConfig: 隔离命名+只读时钟+安全随机源
- 2 Host Functions 注入: >=read_only→只读FS（AllowedPaths）；>=write_local→写入FS；>=write_network→网络代理（AllowedDomains）；privileged→走L3
- 3 编译+实例化→调用"run"（encodeString传JSON）
- 4 解析输出→ToolResult
- 5 PostExecution PII Redact: ToolResult (含 Stdout/Stderr) 双路径:
  (a) 原始 → WorkingMemory (session-scoped，Agent 推理用完整数据)
  (b) 红化 → M11 PIIGuard.Redact(RedactReplace) → [MutationBus] → [EventLog] 永久存储。PII 匹配项替换 `[REDACTED_{TYPE}]`，不进入审计链
  对称防护: Step 0 SecureUnredact + Step 5 Redact 闭合 SessionPIIVault 单向击穿——明文 PII/Token/凭证永不进入不可变审计。FSM Snapshot (M4 §8) 保留原始值供同 session 崩溃恢复，Session 关闭随 Vault 销毁

资源硬限制（超限→ErrSandboxResourceExhausted，不重试）:

| 维度 | Built-in | User | LLM生成 |
|------|---------|------|--------|
| CPU / 壁钟 | 30s / 90s | 10s / 30s | 5s / 15s |
| 调用次数 | 10000 | 5000 | 2000 |
| I/O 总量 | 100MB | 10MB | 1MB |
| 内存(maxPages) | 256 (16MB) | 128 (8MB) | 64 (4MB) |

共用约束: 每次 I/O ≤ 64KB，同函数 ≤ 100 calls/s（超频 throttle 10ms），Host Func 单次 ≤ 100ms（**仅限低层 I/O 原语**；MCP/A2A 宿主侧独立运行不受此限）；超额 → cancel 优雅 → 1s 后 CloseWithExitCode 强制。
强制契约: 阻塞调用前必须 `select{case <-ctx.Done():return ctx.Err() default:}`，CI lint `host_func_audit` 强制检查。
Syscall 防逃逸: Go 堆缓冲区（严禁线性内存切片）→ 独立 goroutine 执行 → ctx.Done() 检查 → copy() 回线性内存；ctx 取消 → 丢弃 + recover()。
长程任务（>4min）须异步: Wasm 提交参数 → M13 TaskQueue 执行 → 返回 JobID → 轮询进度；CapToken 续期绑定宿主侧 Worker，Wasm 90s 壁钟硬限不变（compile=10×5min/crawl=8×5min/index=8×5min）。

### 4.4 WASI 权限矩阵

- WASI 能力: fd_read/write | L1 Built-in: stdio only | L2 User: stdio only | L3 LLM生成: stdio only
- WASI 能力: path_open (read) | L1 Built-in: 工作目录+/usr/local/bin/ | L2 User: /tmp/sandbox/ | L3 LLM生成: /tmp/sandbox/{skill_id}/
- WASI 能力: path_open (write) | L1 Built-in: 工作目录 | L2 User: /tmp/sandbox/ | L3 LLM生成: /tmp/sandbox/{skill_id}/
- WASI 能力: sock_send/recv | L1 Built-in: Egress白名单域名 | L2 User: ❌ | L3 LLM生成: ❌
- WASI 能力: clock_time_get | L1 Built-in: ✅ | L2 User: ✅ | L3 LLM生成: ✅
- WASI 能力: random_get | L1 Built-in: ✅ | L2 User: ✅ | L3 LLM生成: ✅
- WASI 能力: environ_get | L1 Built-in: ❌ | L2 User: ❌ | L3 LLM生成: ❌
- WASI 能力: proc_exit | L1 Built-in: ❌ | L2 User: ❌ | L3 LLM生成: ❌
- WASI 能力: args_get | L1 Built-in: 仅tool input JSON | L2 User: 仅tool input JSON | L3 LLM生成: 仅tool input JSON

目录挂载: Builtin→/workspace+/usr/local/bin(只读)；User→/tmp/sandbox；LLMGenerated→/tmp/sandbox/{skill_id}(0700)

### 4.5 Workspace Bridge

`workspace_read(artifactID,offset,length)->([]byte,error)`:
- 0 路径校验: filepath.Clean→分量级`..`检测+IsAbs拦截。Linux 5.6+: `Openat2(workspaceRootFd, path, RESOLVE_NO_SYMLINKS|RESOLVE_IN_ROOT)`。非Linux: component-by-component walk→逐级openat+Fstat校验dev/inode
- 0.1 读取禁止路径（eval 数据隔离）: 目标路径前缀匹配以下任一 → 立即返回 ErrEvalDataAccessForbidden + CRITICAL 审计，不触发 Capability Token 校验（快速拒绝，防止绕过）:
    `~/.polaris-harness/eval/holdout/`（Holdout Set，防 M9 过拟合，M12 §5）
    `~/.polaris-harness/eval/training/`（Training Set，仅 Eval API 层允许 M9 通过 Ed25519 签名访问，不走 Workspace Bridge）
  注: `~` 展开为运行时 polaris_home 变量（与 M11 Cedar Layer 2 的 context.polaris-harness_eval_holdout_path 同源）。物理层 Openat2(RESOLVE_IN_ROOT) 已阻止路径逃逸，此检查为防御纵深。
- 1 验证Capability Token读权限→2 `Pread(fd,buf,offset)`→3 每次<=64KB→4 Audit Trail

`workspace_write(artifactID,data)->(int,error)`:
- 0 路径白名单校验: 仅允许写入以下三类路径:
  (a) `~/.polaris-harness/workspaces/<task_id>/`（M2 WorkspaceManager 托管目录）
  (b) 经 [Sandbox-L2] 显式挂载的临时目录 `/tmp/sandbox/{skill_id}/`
  (c) 启动时传入的 Workspace Root（用户项目根目录），需经 [Cedar-Gate] 显式授权——Cedar 策略 `permit write_local when resource.path in WorkspaceRoot` 控制可写子路径范围
  默认拒绝所有其他绝对和相对路径。白名单外路径 → ErrWorkspacePathNotAllowed + WARN + 审计
- 0.1 禁止覆盖保护: 即使白名单内，仍禁止覆盖 `~/.polaris-harness/config/`、`~/.polaris-harness/secrets/`、`~/.polaris-harness/data/`（含 SQLite/SurrealDB-Core 数据库文件）、`~/.polaris-harness/audit/`——此四目录为系统关键数据区，独立于 Workspace 白名单做第二层拒绝
- 前置: CapabilityLevel>=write_local
- 0.5 Taint Gate（路径 × TaintLevel 决策表）:

  | 路径 | TaintLevel | 结果 |
  |------|-----------|------|
  | (c) Workspace Root | ≤Medium | 允许；TaintLevel 继承+[Taint-Prop]；Cedar 做最终授权 |
  | (c) Workspace Root | ≥High | ErrTaintGateBlockedWrite；重定向 ephemeral/ |
  | (a) task workspaces | ≤Medium | 允许；TaintLevel 继承+[Taint-Prop] |
  | (a) task workspaces | ≥High | ErrTaintGateBlockedWrite；需人工介入 |
  | (b) 临时沙箱 | ≤Low | 允许；TaintLevel 继承 |
  | (b) 临时沙箱 | ≥Medium | ErrTaintGateBlockedWrite；重定向 ephemeral/ |
- 每次<=64KB，追加模式

### 4.6 Capability 委托链

`ValidateDelegation(originalToken,targetTool)`:
- 规则1 权限收缩: effectiveCapability = min(caller.Capability, target.Capability)
- 规则2 沙箱单调: target.SandboxTier >= caller.SandboxTier。L2→L1拒绝
- 规则3 溯源: derivedToken{ParentTokenID,DerivationDepth,EffectiveCapability,CallChain}。DerivationDepth>=3→拒绝（最大深度3）
- 规则4 白名单: 仅CanInvokeTools标记的Host Function可发起嵌套调用
- 规则5 MCP隔离: MCP Client子进程在调用方沙箱上下文启动（继承WASI权限+Capability约束）

运行时策略重检: Host Function I/O前比对Cedar policy etag与Wasm实例化时policy_etag_at_start。etag变更→重调[Cedar-Gate] Review→FORBID返回ErrPolicyRevoked。etag比对O(1)，仅变更时触发完整评估。

TOCTOU防御: (a)read_local等可逆→接受窗口；(b)write_network等不可逆→PreCheck→I/O→PostCheck 三重校验:
  PreCheck: etag + M8 Blackboard Lease Version 比对
  I/O: 执行工具调用（须尊重 ctx.Done()——M8 Reaper 回收任务前会 cancel agent context）
  PostCheck: 重新校验 etag + Lease Version。etag 变更→审计 policy_etag_diverged_during_io(CRITICAL)+标记 policy_stale；Lease Version 不匹配（任务已被 Reaper 回收）→ 审计 side_effect_orphaned(CRITICAL) + 写入 M3 decision_log（非 events 表——任务所有权已转移至新 Agent，孤儿记录仅作审计轨迹，不参与 Blackboard 状态变更）
(c)privileged→不走快捷路径，每次完整 PolicyGate.Review。M8 Reaper 在重置任务前先 `cancel(agent.ContextCancel)` 并等待 5s 宽限期（M8 §1.7），工具实现须在 5s 内通过 ctx.Done() 感知取消并中止

配额隔离: per-instance（10000/5000/2000），非全局共享。深度3+白名单+per-instance=三层纵深。

### 4.7 L3 策略监视器

L3 microVM (Firecracker / Virtualization.framework / WSL2) 的 I/O 路径不经过 wazero Host Function——syscall 由 VM 内核栈或 gVisor sentry 拦截。这些路径均不触发 Host Function 层的 etag 重检，因此需要独立的策略监视器。

L3PolicyMonitor goroutine (每个 L3 sandbox 一个):
- (a) 订阅 [Cedar-Gate] SubscribeEtagChange（channel 推送）
- (b) etag 变更 → 重评 CapToken: 允许 → 更新 etag；FORBID → 三级阻断:
    (1) 关闭网络出口（各平台拆除方式见 `pkg/action/tool/sandbox_l3.go`）
    (2) context cancel M4（通知 Agent 任务被策略吊销）
    (3) 审计 `l3_policy_revoked_network_killed` (CRITICAL)
- (c) 毫秒级阻断，闭合 etag 变更到下一次 I/O 之间的策略逃逸窗口（最长 4min CapToken TTL 窗口）
- (d) L3 不可用时（Tier 0 全平台 / 平台 microVM 不可用）→ L3PolicyMonitor 不实例化

---

## 5. Policy Gate

### 5.1 Cedar DSL

```cedar
forbid(principal, action, resource) when {
    resource.source == "llm_generated" && !resource.has_been_reviewed };

permit(principal in Role::"Agent", action == Action::"call_tool", resource) when {
    resource.capability == "read_only" && context.session_trust_level >= 1 };

permit(principal in Role::"Agent", action == Action::"call_tool", resource) when {
    resource.capability == "write_local" && context.session_trust_level >= 2 &&
    resource.allowed_paths.contains(context.target_path) };

permit(principal in Role::"Agent", action == Action::"call_tool", resource) when {
    resource.capability == "write_network" && context.session_trust_level >= 3 &&
    context.approval_status == "approved" };
```

### 5.2 5阶段执行

- **Gate1 Cedar 策略评估**: FORBID → 触发 HITL
- **Gate2 Capability Token 验证**: Ed25519 签名 + 5min TTL + scope + 未撤销
- **Gate3 Rate Limiter**: 全局 / 每工具 / 每 Session
- **Gate4 Taint 追踪**: 外部输入标记 tainted，不入 system prompt。工具参数逐字段标 TaintLevel——任一 ≥ `[Taint-Medium]` 且 Capability ≥ write_network → 拒绝（需 SanitizeByUserApproval）。覆盖文件写入（§4.5）+ 网络出口（M11 SafeDialer）两层
- **Gate5 出站预检**: 目标 URL 静态 CIDR 匹配（阻 `127.0.0.0/8` `10.0.0.0/8` `172.16.0.0/12` `192.168.0.0/16` `::1`），白名单域名放行。实际连接委托 M11 SafeDialer（§6）执行五阶段 SSRF 防护（DNS + TOCTOU + IP 锁定）
- 通过 → 执行 L1/L2/L3

三层防线: 语义([Cedar-Gate])→数据([TaintLevel])→网络(SSRF)

### 5.3 Shadow Sink

write_network/privileged 不可逆操作:
- Phase1 Shadow Dry-Run: 路由到 `localhost:{port}/_shadow/{tool_name}`→基于output_schema自动生成mock响应→Agent Kernel验证schema一致性
- Phase2 HITL: 展示工具名+参数摘要+Shadow结果→approve生成一次性Token(TTL 见 `spec/state.yaml §m7_tool.dryrun_protect_window_seconds`)/deny拒绝
- Phase3 Real Execution: 一次性Token签发→执行

`generateMockFromSchema`: string→"[MOCK:{name}]"；number→min/max中值；boolean→false；array→1元素；object→递归

### 5.4 DryRunMode

ToolMeta Reversible=false 时，M7.DryRun(call): (a)参数校验(schema+类型+范围)→(b)权限检查→(c)目标存在性(本地stat/域名白名单+SSRF CIDR/recipient格式)——**禁止真实网络请求**(TCP dial/HTTP HEAD/DNS)→(d)副作用预估(bytes/rows/cost)→返回DryRunResult{Feasible,Warnings[],EstimatedImpact,Reason}。feasible+无warning→自动执行；feasible+有warning→HITL；not feasible→error

- Tool类型: builtin(fs.write) | DryRun行为: 参数校验+路径存在性+权限+写入大小预估
- Tool类型: builtin(shell.exec) | DryRun行为: 命令解析+白名单+参数验证
- Tool类型: MCP proxy | DryRun行为: 仅本地Schema校验+SSRF预检，禁止转发真实请求
- Tool类型: skill(wasm) | DryRun行为: 调用validate()入口，否则schema-only

DryRun结果→[EventLog]（tool.dry_run_result），Reflexion回顾。

---

## 6. Capability Token

短寿命权限令牌，沙箱门口 JIT 签发。实现见 `pkg/action/capability_token.go`。

**JIT Minting 流程**:
Planner 决定调用工具 → 生成 ToolIntent（不签发 Token）→ M8 Blackboard CAS 认领 → HITL 审批（可能 10+ 分钟）→ Worker 进入沙箱 Gate → Gate1-5 全过 → JIT Mint Token (MaxCalls=1, TTL=5min) → 沙箱执行。
审批期间无有效 Token 泄露。

**Token 续期**: 长任务（预估 >4min）宿主 goroutine 每 (TTL-60s) 续期。续期校验:
  - 租约: Agent Lease 未过期
  - 策略: 比对 Cedar-Gate etag；变更则 PolicyGate.Review 重评——允许 → 新 Token + 更新 etag；FORBID → 取消沙箱 + 审计 `token_renewal_policy_revoked` (CRITICAL)
默认 MaxRenewals=5 次（30min 窗口）。长程覆盖: compile=10 次/60min, crawl=8 次/48min, index=8 次/48min。

**委托链溯源**: 每 Token 记录 ParentID，最大深度 3。effectiveCapability = min(caller, target)——权限只缩不放。

---

## 7. 动作空间扩展

### 7.1 Computer Use / GUI 自动化架构

**架构定位**: 采用外置的独立 **Rust MCP Sidecar (`polaris-computer-mcp`)** 作为底层驱动，确保主干 Agent 进程的绝对安全与 `Zero-CGO` 纯洁性。严禁在 Sidecar 中内嵌任何 AI 模型（OmniParser / VLM 均保留在主干推理网关）。

**核心技术栈**:
1. **感知层 (Sensor)**:
   - **截图**: 使用 `xcap` ([GitHub](https://github.com/nashaofu/xcap)) 跨平台截屏。
   - **语义 UI 树**: 直接调用 OS 原生 API (Win: `uiautomation-rs`, Mac: `axuielement`, Linux: `AT-SPI2`)，弃用抽象封装库以确保精度。
2. **执行层 (Actuator)**:
   - **键鼠注入**: 使用 `enigo` ([GitHub](https://github.com/enigo-rs/enigo)) 跨平台模拟。
   - **Linux 特化兜底**: 弃用实验性的 Wayland `libei`，直接采用 `evdev` 向 `/dev/uinput` 写入内核级输入信号。

Execute: ForegroundIntent→physical；BackgroundTask/AutoCurriculum→headless。
- **physical 模式**: 依赖 `polaris-computer-mcp` 本地微服务。
- **headless 模式**: Tier 1+ → `Xvfb :99 -screen 0 1280x800x24` 启动虚拟显示器执行。

**执行耗时追踪**: 底层追踪表（如 `decision_log` 或 `agent_actions`）必须录入 `created_at` 与 `updated_at` 时间戳，供前端渲染耗时。

GUI Action Loop: see→decide→act循环maxSteps次。Capture+UITree→VLM DecideAction(在主干)→发送MCP Command→executeAction(left_click/type等)→GUIResult

**HITL 拦截门控**（`interceptComputerUse`，`pkg/cognition/kernel/agent_execute.go`）:
- 触发工具: `computer_use` 和 `browser_use`（均需经此门控）
- 受 `StateContext.Preferences["computer_use_mode"]` 控制（由 `Agent.SetPreferences` 注入，来源 `preferences` 表）:
  - `auto_review`（默认）: 仅危险动作触发 HITL
  - `default`: 所有调用均触发 HITL
- 危险动作定义:
  - `computer_use`: `key / type / left_click / right_click / double_click / left_click_drag`
  - `browser_use`: `click / type / key`
- HITL 路径: `a.hitl.Prompt()` → 用户 approve/deny → deny 返回 `CodeForbidden`（任务中止）

### 7.2 LAM（Large Action Model）— ComputerUseEngine

实现见 `pkg/action/lam/lam.go`（`ComputerUseEngine`，实现 `LargeActionModel` 接口）。将自然语言意图转为 GUI 动作并执行，路径：`intent + ScreenState → VLM → computerUseArgs JSON → ExecutorFn`。

**硬件门控**: `FeatureComputerUseGUI`（`pkg/substrate/observability`）在 `ExecuteAction` 入口前检查。未通过 → 返回 `ToolResult{Success:false, Error:"FeatureComputerUseGUI not enabled"}`，不抛错。

**双路径架构**:
```
Tier 0 / DOM-only 路径（screenshot == nil 或 >2MB）:
  userContent = "DOM 结构:\n{dom}\n\n用户意图: {intent}"
  → 零图片 token，保护 Tier 0 内存预算

Tier 1+ / vision 路径（screenshot ≤ 2MB）:
  userContent = "屏幕分辨率: {W}x{H}\nDOM 结构:\n{dom}\n\n用户意图: {intent}"
  → base64(ScreenshotBytes) 注入 protocol.Message.Parts
```

**VLM 响应结构** (`vlmActionOutput`):
```
action:     screenshot | left_click | right_click | mouse_move | type | key
coordinate: [x, y]（可选）
text:       输入文本（可选）
reasoning:  推理说明（仅日志，不转发 executor）
```

**ExecutorFn 注入模式**: `executor ExecutorFn` 由调用方注入（通常 `action.NewComputerUseTool().Execute`），解耦 `pkg/action/lam` 与 `pkg/action` 父包，防止循环依赖。`executor=nil` → dry-run 模式，返回解析的动作 JSON 供调试。

**LAMConfig**:
```
Enabled:        bool
PerceptionMode: string  // "auto" (按内存自动降级) | "local_omniparser" (强制本地) | "cloud_vlm" (强制云端多模态)
ResolverModel:  string  // 视觉解析模型，如 "deepseek-chat" 或 "claude-3-5-sonnet"
```

**ActionDiscretizer** `[接口预留，Tier-1+ 连续动作空间]`: 类型定义见 `pkg/action/continuous_action.go`。连续向量 → 离散工具调用投影，Vision 解析路径待激活。Discretize 算法设计见文件注释。

### 7.3 StreamingActionBus（CANONICAL SOURCE）

`pkg/action/streaming_action_bus.go` 已实现 `StreamAction()`（含速率控制令牌桶 + ActionClipper 向量钳制 + maxSteps=1000 步数限制）。`DisplayServer` 接口已定义，平台适配（Xvfb/VNC/Wayland）待 Tier-1+ 接入，nil 时以 no-op 安全降级。

StreamAction 6步流程（已实现）:
1. 类型校验: 仅mouse_delta/key_sequence，其余→ErrStreamingUnsupportedType
2. 速率限制: 滑动窗口+令牌桶，1s/max60(鼠标)或30(键盘)，超限背压等待100ms
3. 边界钳制: mouse_delta dx/dy→[-MaxDeltaPerStep,MaxDeltaPerStep]；key_sequence→ASCII白名单
4. 步数限制: 超maxSteps→ErrMaxStreamingStepsExceeded
5. 帧缓冲写入: DisplayServer.SendAction（nil时no-op降级）
6. 观察返回: StreamingActionResult{Success,FrameID,ScreenFrame,StepCount}

M4 S_EXECUTE路由: tool_call→ActionDiscretizer→ToolCall；mouse_delta→StreamingActionBus→虚拟帧缓冲；key_sequence→StreamingActionBus→虚拟帧缓冲；其他→ErrUnsupportedActionType

LAM路径: 工具调用→ActionDiscretizer(~1-5ms离散化)；GUI→StreamingActionBus(<0.5ms背压)；混合→按Type分流并行双通道

Security: StreamingActionBus不绕过Capability——mouse_delta/key_sequence需CapGUIAutomation（Tier1+默认启用，Tier0需显式授权）。

### 7.4 `[CodeAct]` — 即时代码执行行动空间

> 对齐 2024 CodeAct (Wang et al.) / OpenInterpreter / SWE-agent。区别于 [Logic-Collapse]（沉淀型 Wasm 技能，走 staging）与 LLMGenerated wasm（走 Auto-Curriculum 流水线）——CodeAct 是 **ad-hoc 一次性代码 + 立即执行**，不沉淀。

**用途**: M4 S_EXECUTE 可选行为空间。当任务需要"组合多个工具 + 中间计算 + 条件分支"时，LLM 直接生成 Python/JS 代码片段，由 M7 执行——比多次 tool_call 更紧凑、组合性更强。

**`inv_global_07` 强制约束**（无豁免）:
- Source: `ToolSource=LLMGenerated`
- Sandbox: `[Sandbox-L3]` 平台原生 microVM（Tier 0 拒绝执行，返回 `ErrTier0SandboxLimit`）
- Capability Token: 一次性，MaxCalls=1，TTL=60s
- Audit: 完整代码 + stdout/stderr + exit_code 写 EventLog（`event_type='codeact_exec'`）
- Cedar 策略: deny-by-default，需 `permit code_act when context.session_trust_level >= 3 AND context.approval_status == "approved"`

**`ExecuteCodeAct` 流程**:
```
1. Schema 校验: language ∈ {python, javascript}; code_size <= 16KB; 禁止 import network/subprocess (lint)
2. Taint 检查: code 字段 [TaintLevel] <= [Taint-Medium] (LLM 生成默认 Medium); >= High → 拒绝
3. JIT Mint Capability Token: capability=code_act, MaxCalls=1, TTL=60s
4. 进入 L3 microVM: 加载 Python/Node runtime 镜像 (Tier 2+ 预热, Tier 1 冷启动 ~3s)
5. 注入受限工具集: 仅当前 task 已授权的 M7 工具子集作为 host function (Capability 委托链规则)
6. 执行: ctx 5min 硬上限 (覆盖 [BestOfN] / [UserInterrupt] cancel)
7. PostExec Redact: 同 §4.3 Step 5 (PII Guard + 双路径)
8. 返回 ToolResult { Stdout, Stderr, ExitCode, AuditID }
```

**与 LLMGenerated wasm 技能的区别**:

| 维度 | CodeAct (本节) | LLMGenerated Wasm (M9 Auto-Curriculum) |
|------|---------------|---------------------------------------|
| 沉淀 | 否（一次性） | 是（写入 Skill Library） |
| Staging 流水线 | 不走 | 完整 7 阶段 (M9 §1.5) |
| Sandbox | L3 microVM (Python/JS runtime) | L2 wazero Wasm |
| 用途 | 即时复杂组合 | 高频可复用模式 |
| 风险评估 | 单次 + Audit | 长期 + Eval 验证 |
| HT0 可用 | 否 | 否（也需 [Sandbox-L3] for Wasm 生成阶段，Wasm 执行可用 L2） |

**M4 S_EXECUTE 决策树**（扩展 §5 RouteReasoning）:
- 单工具调用 → 标准 tool_call
- 组合 ≥3 工具 + 中间计算 → 候选 CodeAct（M4 询问 LLM 偏好 + Cedar 校验）
- 高频可复用 → 后台进入 Logic Collapse / Auto-Curriculum 沉淀

---

## 8. Tool Usage Policy Evolution

Logic Collapse (M6) 创建新技能，本机制提升已有工具使用策略——从历史调用轨迹学习最优参数和失败模式。

### 8.1 与 Logic Collapse 的分工

- 维度: 进化对象 | Logic Collapse (M6): 创建新技能 | Tool Usage Policy Evolution (M7): 已有工具的使用策略
- 维度: 触发条件 | Logic Collapse (M6): 50+ 成功 | Tool Usage Policy Evolution (M7): 每次调用后更新统计
- 维度: 输出 | Logic Collapse (M6): Wasm + SKILL.md | Tool Usage Policy Evolution (M7): 策略提示词 + 参数建议
- 维度: 粒度 | Logic Collapse (M6): 工具级 | Tool Usage Policy Evolution (M7): 调用级

### 8.2 策略模型

持久化类型定义见 `pkg/action/tool_usage_policy.go`:

- **ToolUsagePolicy** — 工具的最优参数建议和适用场景。字段: `ToolName` / `ParamHints map[string]ParamHint`（最优参数建议）/ `BestFor []string`（适用场景）/ `NotRecommendedFor []string`（不适用场景）
- **ParamHint** — 参数级别的最优值约束。字段: `DefaultValue any` / `Description string` / `MinValue any` / `MaxValue any`

以下数据由 PolicyEvolver（§8.3）运行时维护，不持久化:
- **FailurePattern** — 失败模式签名（ErrorType × 输入特征），含频率计数和 LLM 生成的缓解策略
- **CoToolPattern** — 工具组合模式（ToolName × Relationship ∈ {before, after}），按频率排序
- **运行时统计** — `SuccessRate`（加权平均）、`AvgLatencyMs`、`UseCount`，每次调用后更新

### 8.3 PolicyEvolver

`pkg/action/tool_usage_policy.go` 已实现：`RecordOutcome`（SuccessRate 加权更新 + 失败模式提取）、`GetContextHint`（UseCount≥20 时返回 ParamHint + 失败警告 + CoTool 建议）、`evolvePolicy`（内部演化逻辑）。M4 DAG 注入路径尚未接入（调用方未调用 GetContextHint）。

PolicyEvolver 算法:

```
extractFailurePattern(ToolName, Error, InputParams):
  1. 解析Error → ErrorTimeout/Permission/Schema/Network/Resource/Unknown
  2. 生成模式签名: ErrorType+input特征 → 唯一样式字符串
  3. 查找CommonFailures: 存在→Frequency++; 不存在→新增
  4. Frequency>=3 → LLM生成Mitigation

Update(ToolCallResult): 每次调用后异步执行
  1. 更新UseCount/SuccessRate(加权平均)/AvgLatencyMs
  2. 成功 → 发现最优参数值, 更新ParamHint
  3. 出错 → extractFailurePattern
  4. 写入policies map

GetContextHint(ToolName):
  1. 策略不存在或UseCount<20→空   2. ParamHints(Confidence>0.7)
  3. FailurePattern告警(Freq>0.3)   4. CoToolPatterns(Freq>0.5)
  5. 返回";"连接字符串
```

**注入位置**: M4 DAG节点执行前, GetContextHint → LLM tool selection prompt上下文。不修改工具定义和schema。

**启用条件**: ≥20次历史调用自动激活。Tier 0低频下以冷启动默认值运行; Tier 1+持续优化。

---

> 安全闭环: [TaintLevel] [Taint-Prop]→[Cedar-Gate]→CapabilityToken→[Sandbox-L1/L2/L3]→RateLimit→[EventLog]

---

## 12. 降级与失败模式

- 故障场景: 沙箱启动失败 (L2 Wasm) | 降级路径: 拒绝执行该工具 + [EventLog] | 恢复策略: 重启沙箱宿主后恢复
- 故障场景: 沙箱启动失败 (L3 gVisor) | 降级路径: 拒绝执行该工具 + AuditEvent | 恢复策略: gVisor 重装后恢复
- 故障场景: Capability Token 校验失败 | 降级路径: 拒绝执行 + AuditEvent | 恢复策略: 重新申请 Token
- 故障场景: MCP 外部 server 不可达 | 降级路径: mark_unreachable → 该工具从可用列表移除 | 恢复策略: 心跳恢复后重新注册
- 故障场景: Rate Limiter 触发 | 降级路径: 429 排队 + Retry-After | 恢复策略: 窗口过期自动恢复
- 故障场景: 不可逆操作 DryRun 失败 | 降级路径: 拒绝执行 + HITL 升级 | 恢复策略: 手动审批后签发一次性 Token
- 故障场景: Linux Firecracker KVM 不可用 | 降级路径: 降级 gVisor (runsc) 用户态内核 | 恢复策略: KVM 可用后自动切换回 Firecracker
- 故障场景: macOS Virtualization.framework 不可用 (旧版本系统) | 降级路径: L3 不可实例化 → ErrFeatureUnavailable | 恢复策略: 系统升级后恢复
- 故障场景: Windows WSL2 不可用 | 降级路径: L3 不可实例化 → ErrFeatureUnavailable | 恢复策略: 启用 WSL2 + Hyper-V 后恢复

与 OSMemoryGuard 协同: L1 预警 → 禁止启动新 Wasm 实例 / L2 紧急 → kill 空闲沙箱 / L3 临界 → kill 全部非关键沙箱，仅保留当前交互任务。

## 默认参数

完整阈值与重评触发条件: `spec/state.yaml §thresholds.m7_tool`。

## 13. 跨模块契约

> 接口签名权威源在 `internal/protocol/interfaces.go` + `types.go`。本表仅列依赖方向 + 一句话语义 + 锚点。

| 方向 | 接口/契约 | 用途 / 锚点 |
|------|----------|-------------|
| M4→M7 | ToolRegistry.ExecuteTool | DAG S_EXECUTE 节点调用；tool_call→Registry→Policy Gate→Sandbox→Execute→ToolResult。M4 §4 |
| M6→M7 | Sandbox L2 Wasm Runner | Wasm 二进制→`[Wasm-Sandbox]` wazero + WASI 权限矩阵。M6 §5 |
| M1→M7 | Tool Schema | LLM tool_call 传入 InputSchema。M1 §2 |
| M8→M7 | SideEffectPreCheck | 每次 ExecuteTool 前强制执行。M8 §1.6 |
| M9→M7 | bash_restricted L2 | 强制 Wasm + Ephemeral Namespace。M9 §2.2 |
| M7→M2 | EventLog session_events | Tool Call audit trail；Workspace Bridge → WorkspaceManager VFS 代理。M2 §2.1, §3 |
| M7→M11 | Cedar-Gate / CredentialVault / SafeDialer | 策略评估 / Token 验证 / 出站统一出口。M11 §3, §5, §6 |
| M7→M11 | TaintTracker | TaintLevel 传播。M11 §2 |
| Schema | Tool / ToolResult / CapabilityLevel / ToolRegistry | `internal/protocol/types.go`, `interfaces.go` |
| 全局字典 | Sandbox-L1/L2/L3、Wasm-Sandbox、Cedar-Gate、CredentialVault | 00-Global-Dictionary §4, §5 |
| 时序图 | Taint Tracking 全链路（外部输入→SanitizeBySchema→workspace 写入）| DIAGRAMS.md#taint-tracking |

---

## 14. Plugin Registry（ADR-0015 §2.1）

> End-User 可通过 Plugin 打包分发技能+MCP 组合（plugin.yaml manifest），无需修改源码。
> 参见 [ADR-0015](./decisions/ADR-0015-codex-feature-integration.md)。

**Plugin manifest 格式** (`plugin.yaml`):
```yaml
name: github
version: 1.0.0
description: "GitHub MCP integration"
skills:        # SKILL.md 路径列表，相对 plugin.yaml 目录解析
  - skills/pr-review/SKILL.md
mcp_servers:   # MCP Server 配置列表
  - name: github-mcp
    transport: stdio
    command: npx
    args: ["-y", "@github/mcp-server"]
    trusted: false
```

**扫描路径**（优先级低→高合并，同名后者覆盖前者）:
1. `~/.polaris-harness/plugins/` — 用户级
2. `.polaris/plugins/` — 项目级

**加载流程**:
```
ScanDir → ParseManifest → Plugin.AbsSkillPaths → plugin.ParseSKILLmd → M6 SkillRegistry.Register
                                                → MCPServerDef → M7 MCPManager.Add
```

**安全约束**:
- Plugin Skills 经 `trust:local` 标签注入，Sbx-L1 限制（不得升 Sbx-L2，除非 cosign 签名已验证）
- Plugin MCP 默认 `trusted=false` → Taint=High（M7 inv_M7_02）

**代码位置**: `pkg/extensions/marketplace/` (marketplace.go / registry.go / loader.go)

---

## 15. Hook 框架（ADR-0015 §2.2）

> End-User 可在生命周期事件注入 shell 脚本（零依赖），对应 ARCHITECTURE.md §1 `[ShellHooks]` 设计意图。
> 输出强制 TaintLevel=High，通过 M11 PolicyGate 才可注入 Agent 上下文。

**事件触发点**:

| 事件 | 触发模块 | 说明 |
|------|---------|------|
| `SessionStart` | M13 Gateway | 连接建立后 |
| `PreToolUse` | M7 (sandbox 执行前) | 支持工具名 matcher 正则 |
| `PostToolUse` | M7 (sandbox 执行后) | 携带工具输出（stdout） |
| `UserPromptSubmit` | M13 消息入队 | 携带原始 prompt |
| `Stop` | M4 FSM S_IDLE | Agent 回到空闲 |

**配置格式** (`~/.polaris-harness/hooks/hooks.yaml`):
```yaml
hooks:
  PreToolUse:
    - matcher: "^Bash$"
      hooks:
        - type: command
          command: "/path/to/pre_tool_check.sh"
          status_message: "Checking command"
          timeout: 30s
  Stop:
    - hooks:
        - type: command
          command: "/path/to/session_summary.sh"
```

**安全不变量**:
- Hook 脚本输出封装为 `TaintLevel=High` 的 TaintedString，不得直接注入 Immutable Zone
- Hook 执行超时 30s（可配置），超时不中断主流程，记录 EventLog 警告事件
- 并发 Hook（同事件多个匹配）由 errgroup 并发执行，互不影响

**代码位置**: `pkg/action/hook/` (hook.go / runner.go / registry.go)
