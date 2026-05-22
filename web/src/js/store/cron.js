import Alpine from 'alpinejs'
import { authHeaders } from '../utils.js'

// ══════════════════════════════════════════════════════════════════════════
// store: cron（自动化任务管理）
// ══════════════════════════════════════════════════════════════════════════

// 内置模板
const TEMPLATES = [
  {
    icon: '🐛',
    name: '每日缺陷扫描',
    prompt: '扫描最近的 commit（自上次运行以来，或过去 24 小时内），查找可能的 bug 并提出最小修复方案，依据规则：只使用仓库中的具体证据（commit SHA, PR, 文件路径, diff, 失败的测试, CI 信号），不要猜测 bug；如果证据不足，请说明并跳过；优先选择最小且安全的修复；避免重构和无关清理。',
    trigger_type: 'cron',
    cron_schedule: '0 9 * * 1-5',
    reasoning_effort: 'high',
  },
  {
    icon: '📋',
    name: '每周工作回顾',
    prompt: '根据已合并的 PR 起草每周发布说明（如有链接请附上），严格检查：仅报告当前仓库中的内容，不要凭空想象当周历史，如果代码库中没有历史记录，说明并跳过；避免对审查者或评论者施加不当影响；区分"已完成"与"进行中"。',
    trigger_type: 'cron',
    cron_schedule: '0 9 * * 1',
    reasoning_effort: 'medium',
  },
  {
    icon: '🔄',
    name: '站会更新',
    prompt: '为站会总结 git 状态，依据规则：陈述应对 commitPR 文件有具体支撑，不要臆测未来工作，保持简洁，确保合同团队同步，并有足够的细节以便快速连线并保持合作。',
    trigger_type: 'cron',
    cron_schedule: '0 8 * * 1-5',
    reasoning_effort: 'low',
  },
  {
    icon: '🚨',
    name: 'CI 失败修复',
    prompt: '总结上一个 CI 窗口中的 CI 失败和不稳定测试；提出首要修复建议，依据规则：只使用确切的工具作为依据；测试、CI 日志、代码变更历史日志；避免过于自信地直接断言原因；区分"已观察"与"推测"。',
    trigger_type: 'cron',
    cron_schedule: '0 10 * * 1-5',
    reasoning_effort: 'high',
  },
  {
    icon: '📊',
    name: 'PR 深度评审',
    prompt: '根据近期 PR 和 Code Review，建议下一步需要深入提升的具体技能，依据规则：每条建议都要有具体证据（PR 主题、审查意见、反复出现的问题），避免泛空泛的建议；复盘出现的问题；忽视空洞泛化的措辞；每条建议都要具体可执行。',
    trigger_type: 'cron',
    cron_schedule: '0 17 * * 5',
    reasoning_effort: 'medium',
  },
  {
    icon: '🎮',
    name: '项目监控',
    prompt: '为当前项目创建每日状态报告：包括最新提交摘要、未解决的 issue、开放的 PR 状态、测试覆盖率趋势（如可获取）。使用简洁的 Markdown 格式输出。',
    trigger_type: 'cron',
    cron_schedule: '0 9 * * *',
    reasoning_effort: 'medium',
  },
]

// cron 预设选项（存储合法 cron 表达式，显示人类可读文本）
const SCHEDULE_PRESETS = [
  { label: '每小时', value: '0 * * * *' },
  { label: '每天 09:00', value: '0 9 * * *' },
  { label: '工作日 09:00', value: '0 9 * * 1-5' },
  { label: '每周一 09:00', value: '0 9 * * 1' },
  { label: '每天 18:00', value: '0 18 * * *' },
  { label: '每月 1 日', value: '0 9 1 * *' },
]

// 推理档位（不暴露模型名称，系统自动映射）
const REASONING_LEVELS = [
  { value: 'low', label: '低', desc: '快速响应，适合简单汇报' },
  { value: 'medium', label: '中', desc: '均衡，大多数任务默认' },
  { value: 'high', label: '高', desc: '深度分析，适合代码审查' },
  { value: 'ultra', label: '超高', desc: '最强推理，适合复杂规划' },
]

// 结果分发
const RESULT_ACTIONS = [
  { value: 'session', label: '保存到会话', desc: '生成对话记录，可在"会话"页查看' },
  { value: 'silent', label: '静默执行', desc: '只记录日志，不推送' },
]

// 人类可读的 cron 表达式
function cronLabel(expr) {
  const preset = SCHEDULE_PRESETS.find(p => p.value === expr)
  if (preset) return preset.label
  if (!expr) return '未设定'
  return expr
}

// 状态色
function statusColor(s) {
  if (s === 'ok') return 'var(--color-success, #22c55e)'
  if (s === 'error') return 'var(--color-error, #ef4444)'
  if (s === 'running') return 'var(--color-accent)'
  return 'var(--color-text-dim)'
}

function statusIcon(s) {
  if (s === 'ok') return '✓'
  if (s === 'error') return '✗'
  if (s === 'running') return '⟳'
  return '○'
}

Alpine.store('cron', {
  list: [],
  loading: false,

  // 创建/编辑弹窗
  showModal: false,
  editMode: 'create',
  showTemplates: false,

  // 执行历史侧栏
  showRuns: false,
  runsJobID: '',
  runsJobName: '',
  runs: [],
  runsLoading: false,

  form: {
    id: '',
    name: '',
    prompt: '',
    trigger_type: 'cron',
    cron_schedule: '0 9 * * 1-5',
    channel_id: '',
    working_dir: '',
    reasoning_effort: 'medium',
    result_action: 'session',
    cedar_rules_json: '[]',
    enabled: true,
  },

  // 暴露给模板的常量
  templates: TEMPLATES,
  schedulePresets: SCHEDULE_PRESETS,
  reasoningLevels: REASONING_LEVELS,
  resultActions: RESULT_ACTIONS,

  cronLabel,
  statusColor,
  statusIcon,

  async load() {
    this.loading = true
    try {
      const r = await fetch('/v1/automations', { headers: authHeaders() })
      const d = await r.json()
      this.list = d.automations || []
    } catch { } finally { this.loading = false }
  },

  openCreate() {
    this.form = {
      id: '', name: '', prompt: '',
      trigger_type: 'cron', cron_schedule: '0 9 * * 1-5', channel_id: '',
      working_dir: '',
      reasoning_effort: 'medium', result_action: 'session',
      cedar_rules_json: '[]', enabled: true,
    }
    this.editMode = 'create'
    this.showTemplates = false
    this.showModal = true
  },

  openEdit(job) {
    this.form = { ...job }
    this.editMode = 'edit'
    this.showTemplates = false
    this.showModal = true
  },

  applyTemplate(t) {
    this.form.name = t.name
    this.form.prompt = t.prompt
    this.form.trigger_type = t.trigger_type || 'cron'
    this.form.cron_schedule = t.cron_schedule || '0 9 * * 1-5'
    this.form.reasoning_effort = t.reasoning_effort || 'medium'
    this.showTemplates = false
  },

  async save() {
    const body = { ...this.form }
    try {
      let r
      if (this.editMode === 'create') {
        r = await fetch('/v1/automations', {
          method: 'POST', headers: authHeaders(), body: JSON.stringify(body),
        })
      } else {
        r = await fetch(`/v1/automations/${body.id}`, {
          method: 'PUT', headers: authHeaders(), body: JSON.stringify(body),
        })
      }
      if (!r.ok) {
        const msg = await r.text()
        throw new Error(`HTTP ${r.status}: ${msg}`)
      }
      this.showModal = false
      await this.load()
      Alpine.store('toast').show('ok', '保存成功')
    } catch (e) {
      Alpine.store('toast').show('error', `保存失败: ${e.message}`)
    }
  },

  async toggle(job) {
    try {
      await fetch(`/v1/automations/${job.id}`, {
        method: 'PUT',
        headers: authHeaders(),
        body: JSON.stringify({ enabled: !job.enabled }),
      })
      await this.load()
    } catch { }
  },

  async del(id) {
    if (!confirm('确认删除这个自动化任务？运行历史也会一并删除。')) return
    try {
      await fetch(`/v1/automations/${id}`, { method: 'DELETE', headers: authHeaders() })
      this.list = this.list.filter(j => j.id !== id)
      Alpine.store('toast').show('ok', '已删除')
    } catch (e) {
      Alpine.store('toast').show('error', `删除失败: ${e.message}`)
    }
  },

  async trigger(job) {
    try {
      const r = await fetch(`/v1/automations/${job.id}/trigger`, {
        method: 'POST', headers: authHeaders(),
      })
      if (!r.ok) throw new Error(`HTTP ${r.status}`)
      Alpine.store('toast').show('ok', `已触发：${job.name || job.id.slice(0, 12)}`)
      // 延迟刷新以便 last_run_status 更新
      setTimeout(() => this.load(), 1500)
    } catch (e) {
      Alpine.store('toast').show('error', `触发失败: ${e.message}`)
    }
  },

  async openRuns(job) {
    this.runsJobID = job.id
    this.runsJobName = job.name || job.id.slice(0, 12)
    this.runs = []
    this.runsLoading = true
    this.showRuns = true
    try {
      const r = await fetch(`/v1/automations/${job.id}/runs?limit=20`, { headers: authHeaders() })
      const d = await r.json()
      this.runs = d.runs || []
    } catch { } finally { this.runsLoading = false }
  },
})
