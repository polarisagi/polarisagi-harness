# pkg/action/ (L1 执行层: M7 工具/沙箱/MCP)

> Canonical arch doc: [M07-Tool-Action-Layer.md](../../docs/arch/M07-Tool-Action-Layer.md)

**硬约束**:
1. Sandbox 强制: 工具必走 Sandbox-L1/L2/L3, 禁裸 os/exec 或 net.Dial
2. Capability Token: 跨界访问必带 Token (Cedar 签发校验)
3. 协议通信: M7-A2A 适配器与 MCP 走 `internal/protocol/`, 禁字符串 topic
4. CodeAct: Tier 0 禁用; Tier 1+ 需 Sandbox-L3+Audit, 计入 ReasoningTokens
5. 依赖单向: 禁 import pkg/{swarm,governance,edge}

**高频陷阱**:
- Tier 0 Mac/Win 无 L3, 自动降级 L2 Wasm+平台沙箱(禁静默失败)
- 注册顺序影响 Wasm Gold/Silver/Bronze 分层 (Tier 0 上限 5/20/25)
- 出站 net 必经 SafeDialer (XR-06)
- Tool 注册必声明 CapabilityLevel+RiskLevel (Cedar 准入)

**文件索引**:
- [标杆] `tool/tool.go`: InMemoryToolRegistry (M7 主入口)
- [参照] `sandbox_impl.go`: 抽象+InProcessSandbox (新增沙箱)
- [参照] `builtin_tools.go`: RegisterBuiltinTools (新增内置)
- [参照] `wazero_runtime.go`: Wasm 实例化与限额
- [参照] `code_act.go`: ad-hoc 代码执行 (Tier 1+)

**跨模块**:
- M4 消费 `protocol.Tool/ToolRegistry/CapabilityLevel` (cognition→action 单向)
- 接口签名变更走 B5 `[proto-break]`