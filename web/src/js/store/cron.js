import Alpine from 'alpinejs'
import { authHeaders, levelGe, sanitizeContent } from '../utils.js'
// ══════════════════════════════════════════════════════════════════════════
// store: cron（定时任务管理）
// ══════════════════════════════════════════════════════════════════════════
Alpine.store('cron', {
  list: [],
  loading: false,
  showModal: false,
  editMode: 'create',
  form: { id: '', name: '', prompt: '', schedule: '@daily', session_id: '', enabled: true, workspace_dir: '', cedar_rules_json: '', env_type: '本地', model_id: 'auto', reasoning_effort: '中' },

  async load() {
    this.loading = true
    try {
      const r = await fetch('/v1/automations', { headers: authHeaders() })
      const d = await r.json()
      this.list = d.automations || []
    } catch { } finally { this.loading = false }
  },

  openCreate() {
    this.form = { id: '', name: '', prompt: '', schedule: '@daily', session_id: '', enabled: true, workspace_dir: '', cedar_rules_json: '', env_type: '本地', model_id: 'auto', reasoning_effort: '中' }
    this.editMode = 'create'
    this.showModal = true
  },

  openEdit(job) {
    this.form = { ...job }
    this.editMode = 'edit'
    this.showModal = true
  },

  async save() {
    const body = { ...this.form }
    try {
      let r
      if (this.editMode === 'create') {
        r = await fetch('/v1/automations', { method: 'POST', headers: authHeaders(), body: JSON.stringify(body) })
      } else {
        r = await fetch(`/v1/automations/${body.id}`, { method: 'PUT', headers: authHeaders(), body: JSON.stringify(body) })
      }
      if (!r.ok) throw new Error(`HTTP ${r.status}`)
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
    if (!confirm('确认删除这个定时任务？')) return
    try {
      await fetch(`/v1/automations/${id}`, { method: 'DELETE', headers: authHeaders() })
      this.list = this.list.filter(j => j.id !== id)
      Alpine.store('toast').show('ok', '已删除')
    } catch (e) {
      Alpine.store('toast').show('error', `删除失败: ${e.message}`)
    }
  },
})

