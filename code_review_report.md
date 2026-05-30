## Critical（安全边界漏洞 / 数据损坏风险）
- [M7/代码执行] CodeAct.Execute 存在安全门 nil 旁路漏洞 → 风险：若 policyGate 未注入（值为 nil），判断逻辑 `if ca.policyGate != nil` 将被跳过，导致未经验证的 LLM 危险代码被直接送入沙箱执行（Fail-Open）。 → 修复方向：移除 nil 判断，若安全依赖为 nil，强制返回 CodeInternal/CodeForbidden 错误（Fail-Closed）。
- [M9/Staging 流水线] RolloutGate 阶段严重缺失 → 风险：`pkg/swarm/rollout.go` 仅实现了 4 个阶段的骨架，缺少架构规定的 7 阶段流水线，且直接跳过了至关重要的 Stage 2 (`schema_validate`)。会导致不兼容的 Schema 变更或者包含数据损坏风险的逻辑直达线上环境。 → 修复方向：严格按照 M9 架构补全 7 个 RolloutGate 阶段，并前置 Schema 验证拦截器。
- [M11/审计防线] 事件日志不可变性丧失 Hash Chain 验证 → 风险：`internal/protocol/schema/001_events.sql` 中未建 `hash` 和 `prev_hash` 字段。尽管 MutationBus 实现了追加写入，但无法通过密码学证明 EventLog 链未被底层直接篡改，破坏了系统的不可否认性。 → 修复方向：在 events 表增加 `hash` 和 `prev_hash` 字段，并在 DatabaseWriter 落盘前同步计算链式哈希。

## High（功能遗留 / 架构违规 / 潜在崩溃）
- [L3/边缘接口与扩展] 绕过沙箱与 ToolRegistry 直接调用原生命令 → 风险：`pkg/interface/server/` 和 `pkg/governance/shadow_executor.go` 等模块直接调用 `exec.Command`（如 git clone, ffmpeg），逃逸了 ToolRegistry 统一拦截和 Wasm/L3 沙箱约束。 → 修复方向：将这些宿主机调用统一封装为合法的 Builtin Tool，或者剥离到受控的隔离外部进程执行代理中。
- [L3/网络通信] 广泛绕过 SSRFGuard 防护 → 风险：在 `pkg/action/tool/builtin_tools.go`, `pkg/interface/channels/` 等位置，频繁直接使用 `&http.Client{}` 或者原生网络 SDK，没有强制通过 M11 提供的 `protocol.SafeDialer`。可能引发服务器端请求伪造（SSRF）漏洞。 → 修复方向：所有网络请求组件在初始化时必须注入 `SafeDialer` 作为 Transport，并在 CI 中启用 `safe_dialer_lint` 扫描。
- [M9/自进化引擎] SurpriseIndex 计算实现为存根且阈值硬编码 → 风险：`pkg/swarm/surprise.go` 中 Embedding 计算被简化为判断关键词是否为空，MEMF 仅仅做了简单的 MAX 取值；路由阈值 `0.3/0.6` 硬编码在了 `Route()` 中未从 `state.yaml` 加载。影响模型双通道路由决策的精确性。 → 修复方向：补全实际的 Cosine Similarity 计算与 MEMF Match，并通过 `config.Get().Thresholds` 动态读取路由阈值参数。
- [M7/Wazero 沙箱] Wasm 实例缓存池无大小上限防线 → 风险：`pkg/action/wazero_runtime.go` 中的金/银/铜三层缓存仅使用了原生的 map 进行存储，完全没有驱逐策略，导致长时间运行极易耗尽 Tier0 的 8GB 内存限制。 → 修复方向：引入 LRU 或基于 TTL 的缓存驱逐机制，并强约束 Gold(5)、Silver(20)、Bronze(25) 的最大数量上限。
- [M9/资源管理] SurpriseCalculator 工作协程永久泄漏 → 风险：`surprise.go` 在初始化时启动了 4 个 `workerLoop` 监听 `queue`，但该结构体并未提供 `Close()` 或 Context 传递机制，导致模块重启时协程会一直驻留成为内存泄漏点。 → 修复方向：为 `SurpriseCalculator` 补充 `context.Context` 监听控制与 `Close` 函数。
- [配置层/不可变内核] 关键阈值与白名单变量未冻结 → 风险：`internal/config/immutable_constants.go` 中的 `AutoApproveAllowedActions` 与 `ImmutableKernelPackages` 使用了可修改的 `var` 关键字。恶意扩展可在运行期直接覆写这些变量，绕过内核层面的安全隔离。 → 修复方向：将其改为无法在运行期变动的只读访问器，或使用不可变的底层实现替代包级别全局 `var` 变量。

## Medium（资源管理 / 边界情况 / 参数硬编码）
- [配置层/系统启动] 配置加载缺失 Fail-Fast 验证 → 风险：`internal/config/config.go` 中仅执行 `yaml.Unmarshal`，对于必填项缺失和格式错误没有任何后置 Assert 拦截，将引发运行期空指针或越界 Panic。 → 修复方向：在 `Load()` 解析后加入必填验证钩子，遇到错误时阻塞并致命退出（Fail-Fast）。
- [M11/KillSwitch] FULLSTOP 状态未设计规范的 Unseal 接口 → 风险：虽然正确实现了 `.fullstop` 的写入封印逻辑，但是代码库中未提供配套的提权解锁（Unseal）API，导致只能通过物理登录容器手动删文件来恢复服务。 → 修复方向：在 M13 接口层与 KillSwitch 之间增加需强身份鉴权与 HITL 操作审计的 `Unseal(adminToken)` 接口。
- [M2/扩展组件] MCP 子进程环境过滤过于保守 → 风险：`pkg/extensions/mcp/env.go` 中的 `sanitizeParentEnv` 函数依赖于 `_KEY`、`_SECRET` 等关键词后缀的"黑名单"机制过滤，存在非常大的绕过空间与误漏隐患。 → 修复方向：变更为"白名单"机制（仅继承 PATH, HOME 等无害系统变量），其余变量显式授权传递。
- [M4/Agent内核] 状态机映射表遗漏了 `S_INTERRUPT` 内部转移 → 风险：由于 `S_INTERRUPT` 通过直接在 `Dispatch` 头部的特定判断处理，这绕过了标准的 `stateToTriggerMap` 定义。这可能导致在需要全局导出状态拓扑进行分析监控时存在盲区。 → 修复方向：统一步骤处理与映射管理标准化，使得全部状态进入正式 FSM 视图。

## Low（代码质量 / 命名 / 注释）
该维度代码风格和实现普遍遵守规范，但存在部分需要轻微整治的地方（如：零散的 panic 恢复注释未实现，`CodeAct` 的语言拦截条件不够灵活）。无造成阻塞的核心问题。

## 架构层依赖违规 (补充维度验证)
该维度无发现。经检查 `pkg/substrate/` (Arch-L0) 内部完全没有出现对 `pkg/cognition/`, `pkg/swarm/`, `pkg/edge/` 或 `pkg/action/` 的逆向或跳跃层级包导入，依赖层级遵守了严格的单向约束。
