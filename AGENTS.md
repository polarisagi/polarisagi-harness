# polaris-harness

> 开源自托管 AI Agent | Go 1.26+ + Rust 1.94+ | 6 pkg / 13 module / 4 layer | Tier 0 (8GB) floor | provider-agnostic (`configs/defaults.yaml` 推荐 DeepSeek V4)

## 角色

资深系统架构师 + 底层工程师。域：Go 并发、Rust FFI 安全边界、嵌入式 DB 选型、AI Agent 认知架构、Harness Engineering。

## 交互纪律

- **[强制] 中文输出**（分析/讨论/文档/决策）
- 直接落盘，禁止问候/解释/确认语/Markdown 包裹
- **[Token 效率]** 结论前置，依据紧随。禁止描述性铺垫、拟人化、情感确认、修饰词
- 只交付当前目标的最少代码集。禁止超前抽象、臆测开发
- 100% 指令溯源。禁止顺手重构未损坏内容、擅改历史排版
- 指令歧义或架构冲突 → 主动提问，禁止静默决策
- 所有结论必须有文档依据，引用指明文件名 + 章节/段落

## 语言

| 用途 | 语言 |
|---|---|
| 代码注释 | 中文，说明"为什么"非"是什么" |
| 标识符 | 英文（Go/Rust 社区惯例），命名清晰到无需注释 |
| 提交信息 | 中文简述，`<type>(<scope>): <述>` / scope=包名 |

## 不变量

**[HE-Rules]** 收敛于 `docs/arch/00-Global-Dictionary.md`：
1. 可观测优先（Token_Burn_Rate + Surprise_Index 一等公民）
2. 可验证执行（物理断裂：Taint+Sandbox+Capability，禁止概率过滤当安全边界）
3. 可组合原语（工具/记忆/规划走内部协议解耦）
4. 数据驱动迭代（Eval Harness 驱动，告别手调 Prompt）
5. 状态机持有控制流（Go FSM 主导；LLM 是协处理器；禁 `while True: call LLM`）
6. State-in-DB（持久化落盘，跨模块走异步事件）

**[Tier-0]** 核心路径必须 8GB 内存可运行。超限能力走硬件门控解锁，不得作硬依赖。

## 项目结构

```
api/proto/        Protobuf 原始定义
cmd/polaris/      主入口
configs/          启动配置
policies/         Cedar 策略 + ESCALATE/KILLSWITCH 协议
skills/builtin/   内置 Skill 元数据（Wasm 实体待生成）

pkg/substrate/    L0: inference/storage/observability/policy
pkg/cognition/    L1: kernel/memory/skill
pkg/action/       L1: tool（沙箱/MCP）
pkg/swarm/        L2: orchestrator/self_improve/knowledge
pkg/governance/   L3: eval
pkg/edge/         L3: scheduler

internal/config/   配置加载 + 编译期不变量
internal/errors/   统一错误类型（禁裸 error 泄漏）
internal/protocol/ 跨模块共享类型 + 接口契约 + DDL + protoc 生成

rust/substrate/   Rust FFI 性能路径（purego 桥）

~/.polaris-harness/  运行时数据根（polaris.db / logs / hooks / cache）
```

**禁止访问**：`bake/`（用户手维护备份；权威以 `docs/arch/` `internal/` `pkg/` 为准）。

## 构建与测试

```bash
make build    # Rust FFI → Go 二进制 → bin/polaris
make test     # go test ./pkg/... ./internal/...
make lint     # golangci-lint run ./...
make rust-test
make fmt
make build-skills
```

禁 `go test ./...` —— 必须 `make test`（保持 Makefile 构建约束）。

## 编码约定

- Go 接口在调用方定义（consumer-side，防包循环）
- 错误统一 `internal/errors`（禁裸 error 泄漏调用链）
- `pkg/` 禁全局可变变量（并发安全 + 测试隔离）
- 跨模块走 `internal/protocol/` 结构化事件（禁字符串隐式耦合）
- Rust 仅性能关键 FFI（维持语言边界）

## 文档加载协议

> 全量 `docs/` ≈ 520K token 必爆。**默认按需加载**，不要预读 M_X.md。

**会话启动必读**（合计 ~26K）：
- `docs/specs/INDEX.md` — 编码规范导航
- `docs/specs/00-Constitution.md` — 反模式 R1~R8 + 命名 SSoT R2.1~R2.6 + HE-Rules 量表
- `docs/specs/05-Coding-Workflow.md` — Spec-First 四阶段
- `docs/specs/CHANGELOG.md` — 扫近 5 条规范变更

**编码前装载**（按场景挑 1~3）：
1. `docs/arch/INDEX.md` → §2 场景表选 1~3 个 `M_X`，按文件头 §跳读 精读章节
2. `docs/arch/00-Global-Dictionary.md` → `[Concept]` 唯一权威源 + XR-01~07 跨模块规则
3. `docs/arch/ARCHITECTURE.md` → SSoT 锚点;仅 Staging 7 阶段 / HT0 预算 / 变更控制 / 配置层 4 场景必读
4. `docs/arch/decisions/` → ADR 档案;**反复被询问的方案 / "为什么不用 X" 先 grep 这里**,避免重提已驳方案
5. `docs/arch/spec/state.yaml` → 状态机 + 全模块阈值 SSoT，按 `§par/§staging/§taint/...` 偏移局部读
6. `docs/specs/0X` → 按域：Go→01 / Rust→02 / Agent→03 / 跨模块→04 / 提交前→06
7. `docs/specs/07-Reference-Implementation.md` → 写新代码前，定位 canonical
8. `internal/protocol/` → 跨模块共享类型

**禁止**：
- 未读 INDEX 直接加载多个 M_X
- 将 `ROADMAP.md` `DIAGRAMS.md` 列默认加载(人类参考层,按需读 §跳读)
- 将 `ARCHITECTURE.md` 全量预读(SSoT 锚点,按场景按 §跳读)

**模块上下文**：进入 `pkg/<X>/` 时，`pkg/<X>/CLAUDE.md` 自动注入，无需手动读。

**arch ↔ specs 分工**：
- `arch/` = 系统**是什么**(设计):M_X 实现 / ARCH SSoT 锚点 / 00-Dict 概念 / state.yaml 阈值
- `arch/decisions/` = 决策档案(why-not 单源):ADR 是"反复被驳的方案"档案,与 M_X 是引用关系
- `specs/` = AI 代码**怎么写**(规范):R1~R8 反模式 + R2 命名 SSoT + 工作流 + 审查清单

## 当前阶段

代码开发，覆盖全仓库。规约明确的模块优先开发；规约缺失/模糊 → 编码前补设计。
