# 05 AI 编码工作流（Spec-First 过程）

> 每次 AI 编码前必须加载本文件并按流程执行。不可跳过。

## W1 会话初始化

加载顺序与文件清单见 `docs/specs/INDEX.md` 加载策略表。总预算 ≈ 20K，不可再增。
进入编码前必须输出 W2 Stage 0 上下文锚定声明 + W6.3 PR 描述模板。

## W2 Spec-First 四阶段过程

**阶段 0 — 上下文锚定（AI 自行执行，无 Human 审核）**

写代码前，AI 必须按序读取并在响应开头声明已读：

1. 任务目标模块的 `pkg/<X>/CLAUDE.md`（模块级强约束）
2. `07-Reference-Implementation.md` 中对应模块的 canonical 文件
3. 相关 ADR：`grep -ri <关键词> docs/arch/decisions/` 至少试一次
4. `docs/specs/CHANGELOG.md` 最近 5 条（感知规范增量）

声明格式（紧随任务接收后输出）：

```
已锚定上下文:
- 模块约束: pkg/cognition/CLAUDE.md
- 标杆:    pkg/cognition/memory/episodic_store.go
- 相关 ADR: ADR-0007（污点降级路径）
- 规范增量: 2026-05-16 R7 可读性硬上限
```

未声明上下文锚定 → Human 拒绝进入阶段 A。

**阶段 A — 定义契约（AI 输出，Human 审核）**

```
输出格式:
## 接口契约
- 修改文件: file_a.go, file_b.go
- 新增文件: file_c.go（如果确实需要）
- 影响模块: 包名列表
- 接口变更: 签名前后对照
- 是否需要 internal/protocol/ 变更: 是/否 + 具体变更
```

Human 批准前，AI 不写任何实现代码。

**阶段 B — 先写测试，再写实现（按文件粒度）**

```
对每个文件:
1. 写测试文件（_test.go），定义边界条件、正常路径、失败路径
2. 编译测试（go build 通过）
3. 写实现文件（填空式的，不要改签名）
4. 运行测试通过（go test 通过）
```

AI 在一个文件内可以测试和实现交替写（先 test→写函数签名→go build→写实现→go test），但跨文件必须按顺序。

**阶段 C — 自查**

```
自查清单:
- [ ] 00-Constitution.md 反模式：逐条检查无违例
- [ ] HE-Rules 6 条工程量表：每条都有交代
- [ ] 命名规范字典：新增标识符符合规范
- [ ] 测试通过：make test
- [ ] 编译通过：make build
- [ ] 没有顺手重构未损坏的代码（保持 100% 指令溯源性）
```

## W3 变更影响声明

阶段 A 输出的契约即变更影响声明（见 W2 阶段 A 模板）。PR 提交时再次浓缩为 W6.3 描述模板，避免重复维护。

## W4 引用纪律

- 引用架构文档时：指明文件名 + 章节号。禁止笼统引用"见架构文档"
- 引用规范文件时：指明文件编号 + 条款编号。如"00-Constitution.md R1.1"
- 引用已有代码时：指明文件名 + 行号。如"参考 orchestrator.go:24"

## W5 对话纪律

- 不保留不相关的历史上下文（上一轮的结果带入下一轮是必须的，但无用的对话历史应清理）
- 不要加载无关的 M_X.md
- 不要加载整个 docs/arch/spec/state.yaml——按章节 Read offset/limit 局部加载
- **一任务一会话**：每个独立逻辑变更（单函数 Spec、单 bug 修复）开新会话，上轮成果作为上下文基线传入。超过 20 轮的长会话上下文注意力衰减，规范遗忘风险显著上升

## W6 PR 纪律

### W6.1 原子变更

- 1 PR = 1 逻辑变更，diff 上限见 `00-Constitution.md R8`
- 超出必须拆分；非可拆场景写 ADR 并在 PR 标题加 `[oversized]`
- 单 PR 不夹带"顺手修改"（破坏 R1 指令溯源性）

### W6.2 契约与实现分离

Spec-First 阶段 A 的契约变更（`internal/protocol/` 改动）必须独立 commit：

- commit message 加 `[spec]` tag
- 该 commit 仅含接口/类型/常量定义，不含实现
- 实现 commit 跟随其后，message 加 `[impl]` tag
- 破坏性变更走 `04-Module-Boundary.md B5.2` 流程

### W6.3 PR 描述模板

```markdown
## 变更类型
[ ] feat / fix / refactor / docs / test
[ ] 包含破坏性变更（如有 link ADR）

## 锚定上下文
- 参考实现: pkg/xxx/yyy.go
- 对齐 / 偏离: 对齐 | 偏离（写明原因）
- 相关 ADR: ADR-NNNN（如无可省）

## 变更摘要
- 总文件数: N（新增 M，修改 K，删除 0）
- diff 行数: __ 行
- 核心逻辑变更: 2 句话
- 高风险区域: 文件名 + 风险

## 自查清单
- [ ] 06-Review.md C1~C9 全部勾选
- [ ] R7 可读性硬上限未违反
- [ ] R8 参考实现已引用
```

### W6.4 评审者并行触发

PR 创建后 CI 触发独立 AI reviewer agent（执行带 3，对抗审查）。人类 review 与 AI reviewer 并行而非串行。AI reviewer 仅喂 Diff + `00-Constitution.md`，要求"指出违例并引用 R 编号，无违例则输出 NONE"。
