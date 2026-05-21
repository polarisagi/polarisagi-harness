# scripts/

开发工具脚本。日常操作通过 `make` 入口调用；下表列出各脚本用途与触发方式。

| 脚本 | make 入口 | 说明 | 触发场景 |
|---|---|---|---|
| `sync_doc_toc.go` | `make docs-sync` / `make docs-check` | 扫描 `docs/arch/M*.md` 的 `## N.` 标题，重写 §跳读 行号；`-check` 模式仅校验不写盘 | 修改任意 M_X.md 后；CI 自动校验 |
| `build_skill.sh` | `make build-skill SKILL=<name>` | 编译单个 Skill 的 `impl.go` → `impl.wasm` | 开发单个 Skill 时 |
| `build_skills.sh` | `make build-skills` | 批量编译 `skills/builtin/*/impl.go` → `.wasm` | 首次安装 / Skill 代码变更后 |
| `generate_impl.sh` | —（一次性） | 为缺少 `impl.go` 的 Skill 目录生成 MVP stub | 新建 Skill 目录骨架时手动运行 |
| `restart.sh` | —（本地开发） | 重编译前端 + Go 二进制并重启服务（端口 29999） | 本地联调；`--full` 参数同时重编 Rust FFI |
| `constitutional_review.sh` | —（CI 触发） | 调用 OpenAI-compatible LLM 对 PR diff 做宪法违例审查，结果 post 到 PR comment | PR 合入 main 时由 `.github/workflows/constitutional-review.yml` 自动执行 |

## constitutional_review.sh 配置

需要在 GitHub 仓库 **Settings → Secrets and variables** 中配置：

| 类型 | 名称 | 说明 |
|---|---|---|
| Secret | `REVIEWER_API_KEY` | API 密钥（任意 OpenAI-compatible 接口） |
| Variable | `REVIEWER_API_BASE` | API base URL |
| Variable | `REVIEWER_MODEL` | 模型名，留空则默认 `deepseek-chat` |

常见 Provider 填法：

```
# DeepSeek（项目推荐默认）
REVIEWER_API_BASE = https://api.deepseek.com/v1
REVIEWER_MODEL    = deepseek-chat

# OpenRouter（多 Provider 聚合）
REVIEWER_API_BASE = https://openrouter.ai/api/v1
REVIEWER_MODEL    = deepseek/deepseek-chat-v3-0324

# OpenAI
REVIEWER_API_BASE = https://api.openai.com/v1
REVIEWER_MODEL    = gpt-4o-mini
```

`REVIEWER_API_KEY` 未配置时 CI 静默跳过审查（warning-only，不阻断合入）。
