package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// writeSSE 写出标准 text/event-stream 帧：event: <type>\ndata: <json>\n\n
func writeSSE(w http.ResponseWriter, flusher http.Flusher, eventType string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	flusher.Flush()
}

// handleAgentStream 处理 SSE 方式的流式对话。
// 直接从 ProviderRegistry 选取最优 Provider 调用 StreamInfer，
// 绕过尚未打通的 FSM→Blackboard 链路（MVP 直通模式）。
//
// SSE 事件协议（与前端 app.js _onEvent 对齐）:
//
//	thinking  → {"content":"..."} 占位思考指示
//	token     → {"content":"<增量文本>"}
//	complete  → {"session_id":"<id>"}
//	error     → {"code":"...","message":"..."}
func (s *Server) handleAgentStream(w http.ResponseWriter, r *http.Request) { //nolint:gocyclo
	var req struct {
		Input     string `json:"input"`
		SessionID string `json:"session_id,omitempty"`
		RunID     string `json:"run_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Input) == "" {
		http.Error(w, "input required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // 关闭 nginx 缓冲

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// SSE 长连接：禁用写超时
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	ctx := r.Context()

	// ── 会话管理 ──────────────────────────────────────────────────────────
	sessionID := strings.TrimSpace(req.SessionID)
	isNewSession := sessionID == ""
	if isNewSession {
		sessionID = newSessionID()
	}
	if err := s.ensureSession(ctx, sessionID); err != nil {
		writeSSE(w, flusher, "error", map[string]string{
			"code": "session_error", "message": err.Error(),
		})
		return
	}
	// session.new hook：用户发起新会话时触发（req.SessionID 为空意味着 /new 后首条消息）
	if isNewSession {
		s.hooks.Fire("session.new", map[string]string{
			"POLARIS_SESSION_ID": sessionID,
			"POLARIS_CHANNEL":    "web",
		})
	}

	// message.before hook：同步拦截，非零退出 = 拒绝本条消息
	if blocked, reason := s.hooks.FireBefore("message.before", map[string]string{
		"POLARIS_MESSAGE":    req.Input,
		"POLARIS_SESSION_ID": sessionID,
		"POLARIS_CHANNEL":    "web",
	}); blocked {
		writeSSE(w, flusher, "error", map[string]string{
			"code": "hook_blocked", "message": reason,
		})
		return
	}

	// 加载历史消息（多轮上下文）
	history, err := s.loadMessages(ctx, sessionID)
	if err != nil {
		writeSSE(w, flusher, "error", map[string]string{
			"code": "history_error", "message": err.Error(),
		})
		return
	}
	isFirstTurn := len(history) == 0

	// ── Transcript ────────────────────────────────────────────────────────
	// 非阻塞：打开失败只警告，不中断对话。
	tw, twErr := openTranscript(s.transcriptDir, sessionID, isFirstTurn)
	if twErr != nil {
		slog.Warn("server: transcript open failed", "session", sessionID, "err", twErr)
	}
	if tw != nil {
		defer tw.Close()
	}

	// 追加本轮用户消息
	history = append(history, protocol.Message{Role: "user", Content: req.Input})
	if err := s.saveMessage(ctx, sessionID, "user", req.Input); err != nil {
		slog.Error("server: saveMessage user", "session", sessionID, "err", err)
	}
	if tw != nil {
		tw.WriteTurn("user", req.Input, 0, 0)
	}

	// ── 选取最优 Provider ─────────────────────────────────────────────────
	// 优先用 "default" 角色（对话模型），次选 "general"（参与全局 LB）
	p := s.registry.PickProvider("default")
	if p == nil {
		p = s.registry.PickProvider("general")
	}
	if p == nil {
		if tw != nil {
			tw.WriteError("no_provider", "未配置任何启用的 LLM 厂商")
		}
		writeSSE(w, flusher, "error", map[string]string{
			"code":    "no_provider",
			"message": "未配置任何启用的 LLM 厂商，请在「模型」页添加并启用厂商",
		})
		return
	}

	// 上下文压缩：provider 已就绪，history 包含本轮新用户消息，超阈值则压缩
	if s.compressor.NeedsCompact(history) {
		writeSSE(w, flusher, "status", map[string]any{"type": "compacting", "message": "正在压缩上下文..."})
		if compacted, res, err := s.compressor.Compact(ctx, sessionID, history, p); err == nil && !res.Skipped {
			history = compacted
			writeSSE(w, flusher, "status", map[string]any{
				"type":          "compacted",
				"tokens_before": res.TokensBefore,
				"tokens_after":  res.TokensAfter,
				"message":       fmt.Sprintf("上下文已压缩：%d → %d tokens", res.TokensBefore, res.TokensAfter),
			})
		}
	}

	// ── 推理（含 tool_use 循环，最多 10 轮）────────────────────────────────
	writeSSE(w, flusher, "thinking", map[string]string{"content": "..."})

	toolSchemas := s.buildToolSchemas()
	inferStart := time.Now()
	var sb strings.Builder
	var inferErr string
	var totalTokens int

	const maxToolRounds = 10
	for range maxToolRounds {
		inferReq := &protocol.InferRequest{
			Messages:    history,
			MaxTokens:   4096,
			Temperature: 0.7,
			Tools:       toolSchemas,
		}
		ch, err := p.StreamInfer(ctx, inferReq)
		if err != nil {
			slog.Error("server: StreamInfer failed", "session", sessionID, "err", err)
			if tw != nil {
				tw.WriteError("infer_error", truncate(err.Error(), 300))
			}
			writeSSE(w, flusher, "error", map[string]string{
				"code": "infer_error", "message": truncate(err.Error(), 300),
			})
			return
		}

		// 收集本轮 text delta、reasoning delta 和 tool_call 事件
		var roundText strings.Builder
		var roundReasoning strings.Builder
		var toolCalls []map[string]json.RawMessage
	roundLoop:
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					break roundLoop
				}
				switch ev.Type {
				case protocol.StreamThinking:
					roundReasoning.WriteString(ev.Content)
				case protocol.StreamTextDelta:
					if ev.Content != "" {
						writeSSE(w, flusher, "token", map[string]string{"content": ev.Content})
						roundText.WriteString(ev.Content)
						sb.WriteString(ev.Content)
					}
					if t := ev.Usage.InputTokens + ev.Usage.OutputTokens; t > 0 {
						totalTokens = t
					}
				case protocol.StreamToolCall:
					var call map[string]json.RawMessage
					if json.Unmarshal([]byte(ev.Content), &call) == nil {
						toolCalls = append(toolCalls, call)
					}
				case protocol.StreamError:
					if inferErr == "" {
						inferErr = ev.Content
					}
				}
			case <-ctx.Done():
				return
			}
		}

		// 没有 tool_call → 推理完成，退出循环
		if len(toolCalls) == 0 || s.toolExec == nil {
			break
		}

		// 有 tool_call：构造 assistant 消息（含 tool_use parts），执行工具，加 tool_result
		assistantParts := make([]any, 0, 1+len(toolCalls))
		if roundText.Len() > 0 {
			assistantParts = append(assistantParts, map[string]any{"type": "text", "text": roundText.String()})
		}
		for _, tc := range toolCalls {
			var toolID, toolName string
			var inputRaw json.RawMessage
			if b, ok := tc["id"]; ok {
				json.Unmarshal(b, &toolID) //nolint:errcheck
			}
			if b, ok := tc["name"]; ok {
				json.Unmarshal(b, &toolName) //nolint:errcheck
			}
			if b, ok := tc["input"]; ok {
				inputRaw = b
			}
			assistantParts = append(assistantParts, map[string]any{
				"type":  "tool_use",
				"id":    toolID,
				"name":  toolName,
				"input": inputRaw,
			})
		}
		assistantMsg := protocol.Message{Role: "assistant", Parts: assistantParts}
		if roundReasoning.Len() > 0 {
			assistantMsg.ReasoningContent = roundReasoning.String()
		}
		history = append(history, assistantMsg)

		// 执行每个工具，收集 tool_result
		toolResultParts := make([]any, 0, len(toolCalls))
		for _, tc := range toolCalls {
			var toolID, toolName string
			var inputRaw json.RawMessage
			if b, ok := tc["id"]; ok {
				json.Unmarshal(b, &toolID) //nolint:errcheck
			}
			if b, ok := tc["name"]; ok {
				json.Unmarshal(b, &toolName) //nolint:errcheck
			}
			if b, ok := tc["input"]; ok {
				inputRaw = b
			}
			writeSSE(w, flusher, "tool_call", map[string]string{"name": toolName})
			result, execErr := s.toolExec(ctx, toolName, inputRaw)
			var resultText string
			if execErr != nil {
				resultText = "error: " + execErr.Error()
			} else if result != nil {
				resultText = string(result.Output)
			}
			slog.Info("server: tool executed", "name", toolName, "ok", execErr == nil)
			toolResultParts = append(toolResultParts, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolID,
				"content":     resultText,
			})
		}
		history = append(history, protocol.Message{Role: "user", Parts: toolResultParts})
	}
	inferLatencyMs := time.Since(inferStart).Milliseconds()

	// 推理成功返回但无内容（超时/内容过滤/空响应）
	if sb.Len() == 0 && inferErr == "" {
		inferErr = "推理返回空内容，请检查模型配置或重试"
	}
	if inferErr != "" {
		slog.Warn("server: empty inference response", "session", sessionID, "reason", inferErr, "err", perrors.New(perrors.CodeInternal, "log event"))
		if tw != nil {
			tw.WriteError("empty_response", inferErr)
		}
		writeSSE(w, flusher, "error", map[string]string{"code": "empty_response", "message": inferErr})
		return
	}

	// ── 持久化 assistant 回复 ─────────────────────────────────────────────
	reply := sb.String()
	if reply != "" {
		if err := s.saveMessage(ctx, sessionID, "assistant", reply); err != nil {
			slog.Error("server: saveMessage assistant", "session", sessionID, "err", err)
		}
		if tw != nil {
			tw.WriteTurn("assistant", reply, inferLatencyMs, totalTokens)
		}
	}
	if isFirstTurn {
		_ = s.updateSessionTitle(ctx, sessionID, req.Input)
	}
	s.touchSession(ctx, sessionID)

	slog.Info("server: turn complete",
		"session", sessionID,
		"latency_ms", inferLatencyMs,
		"tokens", totalTokens,
		"reply_bytes", len(reply),
	)

	// message.after hook：fire-and-forget，不阻塞响应
	s.hooks.Fire("message.after", map[string]string{
		"POLARIS_REPLY":      reply,
		"POLARIS_SESSION_ID": sessionID,
		"POLARIS_CHANNEL":    "web",
	})

	writeSSE(w, flusher, "complete", map[string]any{"session_id": sessionID})
}
