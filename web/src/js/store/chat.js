import Alpine from 'alpinejs'
import { authHeaders, levelGe, sanitizeContent } from '../utils.js'
import { SSEClient, dedupeRunID } from '../sse.js'
// ══════════════════════════════════════════════════════════════════════════
// store: chat（主对话状态机）
// ══════════════════════════════════════════════════════════════════════════
Alpine.store('chat', {
  // States: IDLE | SUBMITTING | THINKING | STREAMING | TOOL_RUNNING | COMPLETE | ERROR
  state: 'IDLE',
  taskID: null,
  sessionID: null,
  messages: [],          // [{role, content, toolCalls, aborted}]
  currentTokens: '',     // 流式追加缓冲
  thinkingText: '',      // 不进 messages[]
  thinkingOpen: true,
  errorMsg: '',
  _historyIdx: -1,
  imageParts: [],        // [{ data, mimeType }]
  ttsEnabled: false,

  get isActive() { return this.state !== 'IDLE' && this.state !== 'COMPLETE' && this.state !== 'ERROR' },

  toggleTTS() {
    this.ttsEnabled = !this.ttsEnabled;
  },

  addImage(dataUrl) {
    // dataUrl format: data:image/png;base64,iVBORw...
    const match = dataUrl.match(/^data:(image\/\w+);base64,(.+)$/);
    if (match) {
      this.imageParts.push({ mimeType: match[1], data: match[2] });
    }
  },

  async submit(input) {
    if (!input.trim() && this.imageParts.length === 0 || this.isActive) return

    // 幂等 runID
    const runID = dedupeRunID(this.sessionID || '', input)

    // 追加用户消息
    this.messages.push({ role: 'user', content: input, toolCalls: [], aborted: false })
    this._inputHistory.unshift(input)
    if (this._inputHistory.length > 50) this._inputHistory.pop()
    this._historyIdx = -1

    this.currentTokens = ''
    this.thinkingText = ''
    this.thinkingOpen = true
    this.errorMsg = ''
    this.state = 'SUBMITTING'

    const imagePartsPayload = [...this.imageParts];
    this.imageParts = [];

    window._activeSseClient = new SSEClient({
      url: '/v1/agent/stream',
      body: {
        input,
        session_id: this.sessionID,
        run_id: runID,
        image_parts: imagePartsPayload,
      },
      onEvent: (type, data) => this._onEvent(type, data),
      onError: (err) => this._onError(err),
      onComplete: () => this._onComplete(),
    })
    window._activeSseClient.start()
  },

  interrupt(action = 'abort') {
    if (!this.taskID) return
    fetch(`/v1/agent/${this.taskID}/interrupt`, {
      method: 'POST',
      headers: authHeaders(),
      body: JSON.stringify({ action }),
    })
    // 乐观更新：如果是 abort，立即标记中断徽章
    if (action === 'abort' && this.currentTokens) {
      this._finalizeMessage(true)
    }
  },

  speakText(text) {
    if (!this.ttsEnabled || !window.speechSynthesis) return;
    const utterance = new SpeechSynthesisUtterance(text);
    window.speechSynthesis.speak(utterance);
  },

  _onEvent(type, data) {
    switch (type) {
      case 'thinking':
        this.state = 'THINKING'
        this.thinkingText += data.content || ''
        break
      case 'token':
        this.state = 'STREAMING'
        this.currentTokens += data.content || ''
        break
      case 'tool_call':
        this.state = 'TOOL_RUNNING'
        // 追加到当前流式消息的 toolCalls
        this._pendingToolCall = { name: data.name || '', input: data.input || {}, output: null }
        break
      case 'tool_result':
        this.state = 'STREAMING'
        if (this._pendingToolCall) {
          this._pendingToolCall.output = data.output || ''
          // 将工具调用记入当前 messages
          if (this.messages.length > 0) {
            const last = this.messages[this.messages.length - 1]
            if (last.role === 'assistant') {
              last.toolCalls.push({ ...this._pendingToolCall })
            }
          } else {
            // 还没有 assistant 消息，先创建占位
            this.messages.push({ role: 'assistant', content: '', toolCalls: [{ ...this._pendingToolCall }], aborted: false })
          }
          this._pendingToolCall = null
        }
        break
      case 'complete':
        if (data && data.session_id) {
          this.sessionID = data.session_id
          localStorage.setItem('polaris_session_id', data.session_id)
        }
        this._onComplete()
        break
      case 'error':
        this._onError(data)
        break
    }

    // 从响应体读取 taskID
    if (data && data.task_id && !this.taskID) {
      this.taskID = data.task_id
    }
  },

  _onComplete() {
    if (this.state === 'ERROR') {
      window._activeSseClient = null
      return
    }
    this._finalizeMessage(false)
    this.state = 'COMPLETE'
    this.thinkingOpen = false
    window._activeSseClient = null
    Alpine.store('statusBar').poll()
  },

  _onError(err) {
    const isAbort = err.code === 'aborted' || err.code === 'interrupted'
    this._finalizeMessage(isAbort)
    this.state = 'ERROR'
    this.errorMsg = err.message || '连接中断'
    window._activeSseClient?.stop()
    window._activeSseClient = null
  },

  _finalizeMessage(aborted = false) {
    const content = sanitizeContent(this.currentTokens)
    if (!content && !aborted) return
    // 检查是否已有 assistant 消息（tool_result 路径可能提前创建）
    const last = this.messages[this.messages.length - 1]
    if (last && last.role === 'assistant' && !last.content) {
      last.content = content
      last.aborted = aborted
    } else if (content || aborted) {
      this.messages.push({ role: 'assistant', content, toolCalls: [], aborted })
    }
    
    if (!aborted && content) {
      this.speakText(content.replace(/<[^>]+>/g, '')); // Strip basic HTML for TTS
    }
    this.currentTokens = ''
  },

  clearView() {
    this.messages = []
    this.currentTokens = ''
    this.thinkingText = ''
    this.errorMsg = ''
    this.state = 'IDLE'
    window._activeSseClient?.stop()
    window._activeSseClient = null
    this.taskID = null
  },

  newSession() {
    this.clearView()
    this.sessionID = null
    this._inputHistory = []
    this._historyIdx = -1
    localStorage.removeItem('polaris_session_id')
  },

  historyUp(currentInput) {
    if (this._inputHistory.length === 0) return currentInput
    this._historyIdx = Math.min(this._historyIdx + 1, this._inputHistory.length - 1)
    return this._inputHistory[this._historyIdx]
  },

  historyDown() {
    if (this._historyIdx <= 0) { this._historyIdx = -1; return '' }
    this._historyIdx--
    return this._inputHistory[this._historyIdx]
  },

  async loadSession(sessionID) {
    this.clearView()
    this.sessionID = sessionID
    try {
      const r = await fetch(`/v1/sessions/${sessionID}?max_chars=50000`, { headers: authHeaders() })
      if (!r.ok) return
      const d = await r.json()
      this.messages = (d.messages || []).map(m => ({
        role: m.role,
        content: sanitizeContent(m.content),
        toolCalls: m.tool_calls || [],
        aborted: m.aborted || false,
        compactionAfter: d.compaction_events?.some(e => e.at_message_id === m.id) || false,
      }))
    } catch { /* 静默失败，空历史 */ }
  },
})

