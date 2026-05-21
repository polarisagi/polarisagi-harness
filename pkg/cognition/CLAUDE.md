# pkg/cognition/ (L1 认知核心: M4 内核 / M5 记忆 / M6 技能)

> Canonical arch docs: [M04-Agent-Kernel.md](../../docs/arch/M04-Agent-Kernel.md) · [M05-Memory-System.md](../../docs/arch/M05-Memory-System.md) · [M06-Skill-Library.md](../../docs/arch/M06-Skill-Library.md)

**硬约束**:
1. 控制流: LLM 调用必被 Go FSM 包裹 (HE-Rule-5), 禁 `for { llm.Call() }`
2. 三 Zone Context: 必含 Immutable/MutableSkill/TaintedData (Immutable 不可省)
3. Agent 隔离: 独立 ContextAssembler, 禁共享父上下文; 仅通过 M8 Blackboard 交换
4. Wasm Skill: 必经 SkillExecutor + Cedar Gate, 禁裸 wazero.NewRuntime
5. Memory 写: episodic_events 是派生投影, 必走 MutationBus, 禁直接 INSERT
6. 依赖单向: 禁 import pkg/{swarm,governance,edge}

**高频陷阱**:
- FSM 11 态 (含 S_INTERRUPT), 修改必同步 `docs/arch/spec/state.yaml`
- ReplayMode=true 时外部副作用必短路 (EmitEvent/ToolCall/Outbox = no-op)
- Memory 检索走 HybridRetriever (BM25+Vector+Graph); 禁单路召回
- UserInterrupt <200ms 必传播 context.Cancel 至所有 LLM/工具
- 跨轮 reasoning state 走 msgpack 加密 (Tier 0 默认 off)

**文件索引**:
- [标杆] `kernel/state_machine.go`: StateMachine (FSM 落盘)
- [标杆] `memory/memory.go`: MemImpl (四层记忆)
- [标杆] `skill/skill.go`: WasmSkillExecutor (内存注册)
- [标杆] `skill/sqlite_registry.go`: SQLiteRegistryImpl (持久化)
- [参照] `context_assembler.go`: 三 Zone 装配
- [参照] `consolidation.go`: Memory 整合 4 阶段
- [参照] `skill_pipeline.go`: 验证管线 5 步

**跨模块**:
- 调用 L0 仅经 `internal/protocol/` (Store/Provider/PolicyGate/SkillExecutor)
- M4/M5/M6 暴露给 L2 的接口见 `internal/protocol/interfaces.go`
- 改接口签名 → B5 `[proto-break]`