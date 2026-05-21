# pkg/swarm/ (L2 协同学习: M8 编排/M9 自进化/M10 RAG)

> Canonical arch docs: [M08-Multi-Agent-Orchestrator.md](../../docs/arch/M08-Multi-Agent-Orchestrator.md) · [M09-Self-Improvement-Engine.md](../../docs/arch/M09-Self-Improvement-Engine.md) · [M10-Knowledge-RAG.md](../../docs/arch/M10-Knowledge-RAG.md)

**硬约束**:
1. Blackboard: 认领必 CAS 原子; Lease 60s, 心跳 15s, Reaper 1s
2. 7 阶段 Staging: M9/M6 输出合并前必走完 7 阶段流水线 (失败→拒绝/回滚)
3. LLM-as-Judge (XR-05): 自进化输出必经异构模型审查
4. 底层共享: M10(RAG) 与 M5(Memory) 共用 hybrid_retrieve, 配置独立单实例
5. 依赖单向: 禁 import pkg/{governance,edge}

**高频陷阱**:
- SurpriseIndex: M9 推送至 M3, M9 禁直读 M4 缓存
- Rollout 推进: M9 决策, M13 执行, M9 禁自行切流
- Memory 写入必经 Cedar 门控
- 安全一票否决: newly_failing safety=regress, 无例外
- BFS 图参数硬卡: depth=2, maxNeighbors=20, nodes=200

**文件索引**:
- [标杆] `blackboard.go`: 根层 M8 入口 (含 TaskEntry)
- [标杆] `self_improve/engine.go`: M9 三环架构
- [标杆] `knowledge/rag_impl.go`: M10 摄入管线
- [参照] `sqlite_blackboard.go`: 持久化后端
- [参照] `graph_build.go`: 知识图谱增量
- [参照] `prompt_optimizer.go`: GEPA/MemAPO 优化
- [参照] `reflexion.go`: M9 内环反思
- [参照] `curriculum.go`: M9 中环边缘任务发现
- [参照] `rollout.go`: M9 外环阶梯
- [参照] `supervisor/tree.go`: 监督树

**跨模块**:
- L1 通信与 L3 暴露均走 `internal/protocol/`
- 改 Blackboard / Staging 步骤 → B5 `[proto-break]`