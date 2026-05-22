package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"

	"gopkg.in/yaml.v3"
)

// ─── 数据模型 ─────────────────────────────────────────────────────────────────

type automation struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	TriggerType     string `json:"trigger_type"`
	CronSchedule    string `json:"cron_schedule"`
	ChannelID       string `json:"channel_id"`
	WorkingDir      string `json:"working_dir"`
	ReasoningEffort string `json:"reasoning_effort"`
	ResultAction    string `json:"result_action"`
	SandboxLevel    int    `json:"sandbox_level"`
	CedarRulesJSON  string `json:"cedar_rules_json"`
	Enabled         bool   `json:"enabled"`
	LastRunAt       string `json:"last_run_at"`
	NextRunAt       string `json:"next_run_at"`
	RunCount        int    `json:"run_count"`
	LastRunStatus   string `json:"last_run_status"`
	LastRunError    string `json:"last_run_error"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type automationRun struct {
	ID             string `json:"id"`
	AutomationID   string `json:"automation_id"`
	Trigger        string `json:"trigger"`
	Status         string `json:"status"`
	SessionID      string `json:"session_id"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	ErrorMsg       string `json:"error_msg"`
	PromptSnapshot string `json:"prompt_snapshot"`
}

func newAutoID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return "auto_" + hex.EncodeToString(b)
}

func newRunID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return "run_" + hex.EncodeToString(b)
}

// ─── GET /v1/automations ──────────────────────────────────────────────────────

func (s *Server) handleListAutomations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, name, prompt, trigger_type, cron_schedule, channel_id,
		       working_dir, reasoning_effort, result_action,
		       sandbox_level, cedar_rules_json, enabled,
		       last_run_at, next_run_at, run_count, last_run_status, last_run_error,
		       created_at, updated_at
		FROM automations ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []automation
	for rows.Next() {
		var a automation
		var enabledInt int
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Prompt, &a.TriggerType, &a.CronSchedule, &a.ChannelID,
			&a.WorkingDir, &a.ReasoningEffort, &a.ResultAction,
			&a.SandboxLevel, &a.CedarRulesJSON, &enabledInt,
			&a.LastRunAt, &a.NextRunAt, &a.RunCount, &a.LastRunStatus, &a.LastRunError,
			&a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			continue
		}
		a.Enabled = enabledInt == 1
		list = append(list, a)
	}
	if list == nil {
		list = []automation{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"automations": list}) //nolint:errcheck
}

// ─── POST /v1/automations ─────────────────────────────────────────────────────

func (s *Server) handleCreateAutomation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		TriggerType     string `json:"trigger_type"`
		CronSchedule    string `json:"cron_schedule"`
		ChannelID       string `json:"channel_id"`
		WorkingDir      string `json:"working_dir"`
		ReasoningEffort string `json:"reasoning_effort"`
		ResultAction    string `json:"result_action"`
		CedarRulesJSON  string `json:"cedar_rules_json"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if req.TriggerType == "" {
		req.TriggerType = "cron"
	}
	if (req.TriggerType == "cron" || req.TriggerType == "both") && strings.TrimSpace(req.CronSchedule) == "" {
		http.Error(w, "cron_schedule is required for trigger_type=cron/both", http.StatusBadRequest)
		return
	}
	if req.ReasoningEffort == "" {
		req.ReasoningEffort = "medium"
	}
	if req.ResultAction == "" {
		req.ResultAction = "session"
	}
	if req.CedarRulesJSON == "" {
		req.CedarRulesJSON = "[]"
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := newAutoID()
	nextRun := ""
	if req.CronSchedule != "" {
		nextRun = calcNextRun(req.CronSchedule, now)
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO automations(
			id, name, prompt, trigger_type, cron_schedule, channel_id,
			working_dir, reasoning_effort, result_action,
			sandbox_level, cedar_rules_json, enabled,
			next_run_at, created_at, updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, req.Name, req.Prompt, req.TriggerType, req.CronSchedule, req.ChannelID,
		req.WorkingDir, req.ReasoningEffort, req.ResultAction,
		2, req.CedarRulesJSON, boolToInt(enabled),
		nextRun, now, now,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "created"}) //nolint:errcheck
}

// ─── PUT /v1/automations/{id} ─────────────────────────────────────────────────

func (s *Server) handleUpdateAutomation(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	var req struct {
		Name            *string `json:"name"`
		Prompt          *string `json:"prompt"`
		TriggerType     *string `json:"trigger_type"`
		CronSchedule    *string `json:"cron_schedule"`
		ChannelID       *string `json:"channel_id"`
		WorkingDir      *string `json:"working_dir"`
		ReasoningEffort *string `json:"reasoning_effort"`
		ResultAction    *string `json:"result_action"`
		CedarRulesJSON  *string `json:"cedar_rules_json"`
		Enabled         *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var j automation
	var enabledInt int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, name, prompt, trigger_type, cron_schedule, channel_id,
		       working_dir, reasoning_effort, result_action,
		       sandbox_level, cedar_rules_json, enabled
		FROM automations WHERE id=?`, jobID).
		Scan(&j.ID, &j.Name, &j.Prompt, &j.TriggerType, &j.CronSchedule, &j.ChannelID,
			&j.WorkingDir, &j.ReasoningEffort, &j.ResultAction,
			&j.SandboxLevel, &j.CedarRulesJSON, &enabledInt)
	if err != nil {
		http.Error(w, "automation not found", http.StatusNotFound)
		return
	}
	j.Enabled = enabledInt == 1

	if req.Name != nil {
		j.Name = *req.Name
	}
	if req.Prompt != nil {
		j.Prompt = *req.Prompt
	}
	if req.TriggerType != nil {
		j.TriggerType = *req.TriggerType
	}
	if req.CronSchedule != nil {
		j.CronSchedule = *req.CronSchedule
	}
	if req.ChannelID != nil {
		j.ChannelID = *req.ChannelID
	}
	if req.WorkingDir != nil {
		j.WorkingDir = *req.WorkingDir
	}
	if req.ReasoningEffort != nil {
		j.ReasoningEffort = *req.ReasoningEffort
	}
	if req.ResultAction != nil {
		j.ResultAction = *req.ResultAction
	}
	if req.CedarRulesJSON != nil {
		j.CedarRulesJSON = *req.CedarRulesJSON
	}
	if req.Enabled != nil {
		j.Enabled = *req.Enabled
	}

	now := time.Now().UTC().Format(time.RFC3339)
	nextRun := ""
	if j.CronSchedule != "" {
		nextRun = calcNextRun(j.CronSchedule, now)
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE automations
		SET name=?, prompt=?, trigger_type=?, cron_schedule=?, channel_id=?,
		    working_dir=?, reasoning_effort=?, result_action=?,
		    cedar_rules_json=?, enabled=?, next_run_at=?, updated_at=?
		WHERE id=?`,
		j.Name, j.Prompt, j.TriggerType, j.CronSchedule, j.ChannelID,
		j.WorkingDir, j.ReasoningEffort, j.ResultAction,
		j.CedarRulesJSON, boolToInt(j.Enabled), nextRun, now, jobID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"}) //nolint:errcheck
}

// ─── DELETE /v1/automations/{id} ──────────────────────────────────────────────

func (s *Server) handleDeleteAutomation(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	s.db.ExecContext(r.Context(), `DELETE FROM automation_runs WHERE automation_id=?`, jobID) //nolint:errcheck
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM automations WHERE id=?`, jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}) //nolint:errcheck
}

// ─── GET /v1/automations/{id}/runs ────────────────────────────────────────────

func (s *Server) handleListAutomationRuns(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 100 {
		limit = v
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, automation_id, trigger, status, session_id,
		       started_at, finished_at, error_msg, prompt_snapshot
		FROM automation_runs
		WHERE automation_id=?
		ORDER BY started_at DESC LIMIT ?`, jobID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []automationRun
	for rows.Next() {
		var run automationRun
		if err := rows.Scan(
			&run.ID, &run.AutomationID, &run.Trigger, &run.Status, &run.SessionID,
			&run.StartedAt, &run.FinishedAt, &run.ErrorMsg, &run.PromptSnapshot,
		); err != nil {
			continue
		}
		list = append(list, run)
	}
	if list == nil {
		list = []automationRun{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"runs": list}) //nolint:errcheck
}

// ─── POST /v1/automations/{id}/trigger ────────────────────────────────────────

func (s *Server) handleTriggerAutomation(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	var a automation
	var enabledInt int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, name, prompt, working_dir, reasoning_effort,
		       result_action, cedar_rules_json, enabled
		FROM automations WHERE id=?`, jobID).
		Scan(&a.ID, &a.Name, &a.Prompt, &a.WorkingDir,
			&a.ReasoningEffort, &a.ResultAction, &a.CedarRulesJSON, &enabledInt)
	if err != nil {
		http.Error(w, "automation not found", http.StatusNotFound)
		return
	}
	a.Enabled = enabledInt == 1
	if !a.Enabled {
		http.Error(w, "automation is disabled", http.StatusConflict)
		return
	}

	runID := s.executeAutomation(r.Context(), &a, "manual")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"run_id": runID, "status": "triggered"}) //nolint:errcheck
}

// ─── Cron 后台运行器 ──────────────────────────────────────────────────────────

func (s *Server) startCronRunner(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cronTick(ctx)
			}
		}
	}()
}

// cronTick 扫描 next_run_at <= NOW() 的任务并触发执行。
func (s *Server) cronTick(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, prompt, trigger_type, cron_schedule,
		       working_dir, reasoning_effort, result_action, cedar_rules_json
		FROM automations
		WHERE enabled=1
		  AND (trigger_type='cron' OR trigger_type='both')
		  AND cron_schedule != ''
		  AND (next_run_at = '' OR next_run_at <= ?)`,
		now)
	if err != nil {
		slog.Warn("cronTick: query failed", "err", err)
		return
	}
	defer rows.Close()

	var due []automation
	for rows.Next() {
		var a automation
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Prompt, &a.TriggerType, &a.CronSchedule,
			&a.WorkingDir, &a.ReasoningEffort, &a.ResultAction, &a.CedarRulesJSON,
		); err != nil {
			continue
		}
		due = append(due, a)
	}
	rows.Close()

	for i := range due {
		a := &due[i]
		go s.executeAutomation(ctx, a, "cron")
	}
}

// executeAutomation 创建 run 记录、调用 Agent 执行、更新状态。
// 返回 runID，异步执行不阻塞调用方。
func (s *Server) executeAutomation(ctx context.Context, a *automation, trigger string) string {
	runID := newRunID()
	now := time.Now().UTC().Format(time.RFC3339)

	// 写 run 记录（running 状态）
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_runs(id, automation_id, trigger, status, prompt_snapshot, started_at)
		VALUES(?,?,?,?,?,?)`,
		runID, a.ID, trigger, "running", a.Prompt, now,
	); err != nil {
		slog.Warn("automation: insert run failed", "run", runID, "err", err)
	}

	// 更新 automations 执行状态
	nextRun := calcNextRun(a.CronSchedule, now)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE automations
		SET last_run_at=?, next_run_at=?, last_run_status='running', updated_at=?
		WHERE id=?`,
		now, nextRun, now, a.ID,
	); err != nil {
		slog.Warn("automation: update status failed", "id", a.ID, "err", err)
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		status := "ok"
		errMsg := ""

		// 调用 Agent 执行 prompt（复用 handleAgentQuery 路径）
		// SetTaskIntent 注入用户意图字节，SendIntent 驱动 FSM 从 IDLE→PLANNING
		if s.agent != nil {
			s.agent.SetTaskIntent([]byte(a.Prompt))
			s.agent.SendIntent(protocol.TriggerIntentReceived)
			slog.Info("automation: agent triggered", "id", a.ID, "run", runID,
				"dir", a.WorkingDir, "effort", a.ReasoningEffort)
		} else {
			slog.Info("automation: agent not available, skipping exec", "id", a.ID)
		}

		finishedAt := time.Now().UTC().Format(time.RFC3339)

		// 更新 run 记录
		if _, err := s.db.ExecContext(bgCtx, `
			UPDATE automation_runs
			SET status=?, finished_at=?, error_msg=?
			WHERE id=?`,
			status, finishedAt, errMsg, runID,
		); err != nil {
			slog.Warn("automation: update run failed", "run", runID, "err", err)
		}

		// 更新 automations 统计
		if _, err := s.db.ExecContext(bgCtx, `
			UPDATE automations
			SET last_run_status=?, last_run_error=?, run_count=run_count+1, updated_at=?
			WHERE id=?`,
			status, errMsg, finishedAt, a.ID,
		); err != nil {
			slog.Warn("automation: update stats failed", "id", a.ID, "err", err)
		}
	}()

	return runID
}

// ─── Cron 表达式解析（简化版，支持标准5字段格式 + @daily/@weekly 别名）────────

// calcNextRun 基于当前时间计算下次触发时间（RFC3339）。
// 仅实现分钟级精度的简单解析，满足日常/每周/每月场景。
func calcNextRun(expr, fromRFC3339 string) string {
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		from = time.Now().UTC()
	}

	// 语义别名展开
	switch strings.TrimSpace(expr) {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	}

	// 去掉秒字段（6字段 → 5字段）
	parts := strings.Fields(expr)
	if len(parts) == 6 {
		parts = parts[1:]
	}
	if len(parts) != 5 {
		return ""
	}

	minute := parts[0]
	hour := parts[1]

	// 只解析固定值（不支持 */n、范围等复杂语法）
	// 对于 `*` 取步长 1，固定值取该值
	minVal := -1
	hourVal := -1
	if v, e := strconv.Atoi(minute); e == nil {
		minVal = v
	}
	if v, e := strconv.Atoi(hour); e == nil {
		hourVal = v
	}

	// 从 from+1min 开始向前推，找下一个匹配时刻（最多搜索 1 年）
	t := from.Add(time.Minute).Truncate(time.Minute)
	for range 525600 { // 最多 365 天 × 1440 分钟
		if (minVal == -1 || t.Minute() == minVal) && (hourVal == -1 || t.Hour() == hourVal) {
			return t.UTC().Format(time.RFC3339)
		}
		t = t.Add(time.Minute)
	}
	return ""
}

// ─── 自动化模板 ───────────────────────────────────────────────────────────────

// automationTemplate 对应 automations/templates/*.yaml 中的单条记录。
type automationTemplate struct {
	Icon            string   `yaml:"icon"             json:"icon"`
	Name            string   `yaml:"name"             json:"name"`
	Description     string   `yaml:"description"      json:"description"`
	Prompt          string   `yaml:"prompt"           json:"prompt"`
	TriggerType     string   `yaml:"trigger_type"     json:"trigger_type"`
	CronSchedule    string   `yaml:"cron_schedule"    json:"cron_schedule"`
	ReasoningEffort string   `yaml:"reasoning_effort" json:"reasoning_effort"`
	Tags            []string `yaml:"tags"             json:"tags,omitempty"`
	Source          string   `yaml:"source"           json:"source,omitempty"`
	Author          string   `yaml:"author"           json:"author,omitempty"`
}

// loadLocalTemplates 扫描 dir 下所有 *.yaml 文件，合并解析为模板列表。
func loadLocalTemplates(dir string) []automationTemplate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var all []automationTemplate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			slog.Warn("automation-templates: read failed", "file", e.Name(), "err", err)
			continue
		}
		var tpls []automationTemplate
		if err := yaml.Unmarshal(b, &tpls); err != nil {
			slog.Warn("automation-templates: parse failed", "file", e.Name(), "err", err)
			continue
		}
		all = append(all, tpls...)
	}
	return all
}

// GET /v1/automation-templates
// 返回本地 automations/templates/*.yaml 中的所有模板（后续可扩展远程源）。
func (s *Server) handleListAutomationTemplates(w http.ResponseWriter, r *http.Request) {
	templates := loadLocalTemplates("automations/templates")
	if templates == nil {
		templates = []automationTemplate{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"templates": templates}) //nolint:errcheck
}
