# docs/specs/ 变更日志

> 规范本身的演进记录。AI 每次会话开头扫描最近 5 条以感知规范增量。

格式：`YYYY-MM-DD | 文件 | 变更摘要`

## 2026-05-22（文档卫生规约）

**规范新增**：
- `08-Doc-Hygiene.md` | 新增 docs/arch/ 维护边界。H1 三层判定（契约/决策/实现）+ H2 修饰物清理 + H3 数值双写消除 + H4 EntryPoint 化前置条件 + H5 决策迁 ADR + H6 Tier C 禁区 + H7 锚点化 + H8 五条验收门 + H9 Pilot 协议
- `INDEX.md` | 加载策略表新增第 08 行（改架构文档前加载）

**背景**：评估外部 Schema-first 极简方案后，确认全盘 EntryPoint 化会摧毁 PII/Taint/Capability 顺序契约（违反 [HE-Rule-5]/[HE-Rule-6]）。改采"差分式精简"——清修饰、消双写、保契约。首次 Pilot 选 M04。

**Pilot 反馈（同日修订 H8）**：
- M04 Pilot 实跑显示，契约密集型文件做完合规 A1+A2 后字符量微增（+0.76%）——A2 下推让 `spec/state.yaml §m4_kernel.xxx` 引用比裸数字更长
- H8 门 1 原"目标 -15%~-25%"假设所有 M_X 等价，实测假设错误
- 修订：门 1 改为"行动度量优先（A1≥1 + A2 全覆盖）+ 文件类型分级 token 参考值"。契约密集型 -5%~+5%，平衡型 -8%~-18%，实现密集型 -15%~-25%
- 价值：暴露规约缺陷正是 Pilot 的目的，符合 H9 协议

## 2026-05-16（规范体系初始化）

**规范规则新增**：
- `00-Constitution.md` | 新增 R7（可读性硬上限：函数≤60行/文件≤400行/嵌套≤3/圈复杂度≤15）
- `00-Constitution.md` | 新增 R8（参考实现强制引用：写新代码前必须 Read canonical 标杆）
- `04-Module-Boundary.md` | 新增 B5（契约版本化与破坏性变更协议）
- `05-Coding-Workflow.md` | W2 前置 Stage 0（上下文锚定），新增 W6（PR 纪律：原子变更/契约分离/PR 描述模板/对抗审查）
- `06-Review.md` | 新增 C8（参考实现对齐）、C9（PR 体积检查）

**参考实现体系建立**：
- `07-Reference-Implementation.md` | 新增标杆代码索引，全部 `pkg/` 的 canonical 文件确认（见表）
- `pkg/*/CLAUDE.md` | 6 份模块级 AI 上下文文件（substrate/cognition/action/swarm/governance/edge）

**支撑体系建立**：
- `../arch/00-Global-Dictionary.md` | 新增 §13 标识符↔概念映射表（命名一致性 SSoT）
- `../arch/decisions/` | 新建 ADR 目录，初始化 ADR-0001~0014（依赖选型回填 + R1.3/R1.4/lint/对抗审查决策）
- `../arch/spec/state.yaml` | 补 `s_interrupt` 状态（spec_consistency_test 发现 Go↔yaml 漂移）
- `.golangci.yml` | 启用 4 个规范 linter（depguard/errorlint/nestif/gocyclo）
- `.github/workflows/constitutional-review.yml` | PR 触发对抗审查 GitHub Action

**ADR 执行状态**（代码已落地，记录于各 ADR 修订记录）：
- ADR-0002：skill.go 本地接口/类型全部删除，统一 protocol.SkillMeta（-~200行死代码）
- ADR-0011：cedar_ffi.go + surreal_store.go 完成 cgo→purego 迁移，ABI 1.0 协议
- ADR-0012：spec_consistency_test.go 落地，4 项 Tier 1 SSoT 守护
- ADR-0013：.golangci.yml 启用 4 linter，CI fail-closed
- ADR-0014：constitutional-review.yml + scripts/constitutional_review.sh 落地
