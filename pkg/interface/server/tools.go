package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// ToolInfo 工具列表 API 响应条目。
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "builtin" | "mcp"
	RiskLevel   int    `json:"risk_level,omitempty"`
	Connected   bool   `json:"connected,omitempty"` // MCP 工具专用
}

// SkillInfo skill 列表 API 响应条目。
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
}

// handleListTools 返回所有已注册工具（builtin + MCP）。
// GET /v1/tools
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	var tools []ToolInfo

	// Builtin tools 来自 ToolRegistry
	if s.toolReg != nil {
		for _, t := range s.toolReg.List() {
			tools = append(tools, ToolInfo{
				Name:        t.Name,
				Description: t.Description,
				Source:      string(t.Source),
				RiskLevel:   int(t.RiskLevel),
			})
		}
	}

	// MCP tools 来自 MCPManager
	if s.mcpMgr != nil {
		for _, schema := range s.mcpMgr.ListToolSchemas() {
			tools = append(tools, ToolInfo{
				Name:        schema.Name,
				Description: schema.Description,
				Source:      "mcp",
				Connected:   true,
			})
		}
	}

	if tools == nil {
		tools = []ToolInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tools": tools}) //nolint:errcheck
}

// handleListSkills 返回所有已注册 skill。
// GET /v1/skills
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	var skills []SkillInfo

	if s.skillReg != nil {
		metas, err := s.skillReg.List(r.Context(), protocol.SkillFilter{IncludeDeprecated: true})
		if err == nil {
			for _, m := range metas {
				skills = append(skills, SkillInfo{
					Name:    m.Name,
					Version: m.Version,
					Enabled: !m.Deprecated,
					Source:  m.Runtime,
				})
			}
		}
	}

	if skills == nil {
		skills = []SkillInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"skills": skills, "total": len(skills)}) //nolint:errcheck
}

// handleListToolSchemas 返回可注入 LLM 的 tool schema 列表（供调试用）。
// 复用 buildToolSchemas，暴露给前端检查工具注入是否正确。
func (s *Server) handleListToolSchemas(w http.ResponseWriter, _ *http.Request) {
	schemas := s.buildToolSchemas()
	if schemas == nil {
		schemas = []protocol.ToolSchema{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"schemas": schemas, "total": len(schemas)}) //nolint:errcheck
}

// handleExecuteTool 直接执行工具（内置或 MCP）。
// POST /v1/tools/{name}/execute
func (s *Server) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	if s.toolExec == nil {
		http.Error(w, "tool executor not available", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing tool name", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	res, err := s.toolExec(r.Context(), name, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res) //nolint:errcheck
}

// handleInstallSkill 接受 Wasm 载荷或源码，注册到技能库中。
// POST /v1/skills/install
func (s *Server) handleInstallSkill(w http.ResponseWriter, r *http.Request) {
	if s.skillReg == nil {
		http.Error(w, "skill registry not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Runtime     string `json:"runtime"` // "wasm"
		Payload     []byte `json:"payload"` // Base64 Wasm bytes
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	meta := protocol.SkillMeta{
		Name:       req.Name,
		Version:    req.Version,
		Runtime:    req.Runtime,
		Deprecated: false,
	}

	if err := s.skillReg.Register(r.Context(), meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.clearToolSchemaCache()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "skill installed"}) //nolint:errcheck
}
