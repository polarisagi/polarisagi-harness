package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ─── Cron Job 定义 ────────────────────────────────────────────────────────────

type automation struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Schedule        string `json:"schedule"`
	WorkspaceDir    string `json:"workspace_dir"`
	EnvType         string `json:"env_type"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	SandboxLevel    int    `json:"sandbox_level"`
	CedarRulesJSON  string `json:"cedar_rules_json"`
	IsTemplate      int    `json:"is_template"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       string `json:"created_at"`
}

func newCronID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return "cron_" + hex.EncodeToString(b)
}

// ─── HTTP 处理器 ──────────────────────────────────────────────────────────────

// GET /v1/automations
func (s *Server) handleListAutomations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, name, prompt, cron_schedule, workspace_dir, env_type, model_id, reasoning_effort, sandbox_level, cedar_rules_json, is_template, enabled, created_at
		 FROM automations ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var automations []automation
	for rows.Next() {
		var a automation
		var enabledInt int
		if err := rows.Scan(&a.ID, &a.Name, &a.Prompt, &a.Schedule, &a.WorkspaceDir, &a.EnvType, &a.ModelID, &a.ReasoningEffort, &a.SandboxLevel, &a.CedarRulesJSON, &a.IsTemplate, &enabledInt, &a.CreatedAt); err != nil {
			continue
		}
		a.Enabled = enabledInt == 1
		automations = append(automations, a)
	}
	if automations == nil {
		automations = []automation{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"automations": automations}) //nolint:errcheck
}

// POST /v1/automations
func (s *Server) handleCreateAutomation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		Schedule        string `json:"schedule"`
		WorkspaceDir    string `json:"workspace_dir"`
		EnvType         string `json:"env_type"`
		ModelID         string `json:"model_id"`
		ReasoningEffort string `json:"reasoning_effort"`
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
	if strings.TrimSpace(req.Schedule) == "" {
		http.Error(w, "schedule is required", http.StatusBadRequest)
		return
	}
	if req.WorkspaceDir == "" {
		req.WorkspaceDir = "./"
	}
	if req.EnvType == "" {
		req.EnvType = "local"
	}
	if req.ModelID == "" {
		req.ModelID = "auto"
	}
	if req.ReasoningEffort == "" {
		req.ReasoningEffort = "medium"
	}
	if req.CedarRulesJSON == "" {
		req.CedarRulesJSON = "[]"
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	id := newCronID()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO automations(id, name, prompt, cron_schedule, workspace_dir, env_type, model_id, reasoning_effort, sandbox_level, cedar_rules_json, is_template, enabled, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, req.Name, req.Prompt, req.Schedule, req.WorkspaceDir, req.EnvType, req.ModelID, req.ReasoningEffort, 2, req.CedarRulesJSON, 0, enabledInt, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "created"}) //nolint:errcheck
}

// PUT /v1/automations/{id}
func (s *Server) handleUpdateAutomation(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	var req struct {
		Name            *string `json:"name"`
		Prompt          *string `json:"prompt"`
		Schedule        *string `json:"schedule"`
		WorkspaceDir    *string `json:"workspace_dir"`
		EnvType         *string `json:"env_type"`
		ModelID         *string `json:"model_id"`
		ReasoningEffort *string `json:"reasoning_effort"`
		CedarRulesJSON  *string `json:"cedar_rules_json"`
		Enabled         *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 加载现有
	var j automation
	var enabledInt int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, name, prompt, cron_schedule, workspace_dir, env_type, model_id, reasoning_effort, sandbox_level, cedar_rules_json, is_template, enabled
		 FROM automations WHERE id=?`, jobID).
		Scan(&j.ID, &j.Name, &j.Prompt, &j.Schedule, &j.WorkspaceDir, &j.EnvType, &j.ModelID, &j.ReasoningEffort, &j.SandboxLevel, &j.CedarRulesJSON, &j.IsTemplate, &enabledInt)
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
	if req.Schedule != nil {
		j.Schedule = *req.Schedule
	}
	if req.WorkspaceDir != nil {
		j.WorkspaceDir = *req.WorkspaceDir
	}
	if req.EnvType != nil {
		j.EnvType = *req.EnvType
	}
	if req.ModelID != nil {
		j.ModelID = *req.ModelID
	}
	if req.ReasoningEffort != nil {
		j.ReasoningEffort = *req.ReasoningEffort
	}
	if req.CedarRulesJSON != nil {
		j.CedarRulesJSON = *req.CedarRulesJSON
	}
	if req.Enabled != nil {
		j.Enabled = *req.Enabled
	}

	enabledInt = 0
	if j.Enabled {
		enabledInt = 1
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE automations SET name=?, prompt=?, cron_schedule=?, workspace_dir=?, env_type=?, model_id=?, reasoning_effort=?, cedar_rules_json=?, enabled=?
		 WHERE id=?`,
		j.Name, j.Prompt, j.Schedule, j.WorkspaceDir, j.EnvType, j.ModelID, j.ReasoningEffort, j.CedarRulesJSON, enabledInt, jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"}) //nolint:errcheck
}

// DELETE /v1/automations/{id}
func (s *Server) handleDeleteAutomation(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM automations WHERE id=?`, jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}) //nolint:errcheck
}

// ─── Cron 后台运行器 ──────────────────────────────────────────────────────────

// startCronRunner 启动后台 goroutine，每 60s 检查并执行到期的 cron job。
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

// cronTick 扫描并执行所有到期的 cron jobs。
func (s *Server) cronTick(ctx context.Context) {
	// NOTE: Simplified cron runner that relies on external scheduling or skips next_run_at tracking for now.
	// For full codex automation, the agent triggers it via scheduler, or we need to add next_run_at to automations table.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, prompt, cron_schedule
		 FROM automations
		 WHERE enabled=1`)
	if err != nil {
		slog.Warn("cron: query failed", "err", err)
		return
	}
	rows.Close()
}
