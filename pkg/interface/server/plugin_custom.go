package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

type CustomSkillReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repo_url"`
	Entrypoint  string `json:"entrypoint"`
}

func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	var req CustomSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := "csk_" + hex.EncodeToString(b)

	_, err := s.db.ExecContext(r.Context(),
		"INSERT INTO skills(id, name, description, repo_url, entrypoint, publisher, trust_tier, enabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		id, req.Name, req.Description, req.RepoURL, req.Entrypoint, "user", 1, 1, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

type CustomPluginReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ManifestURL string `json:"manifest_url"`
}

func (s *Server) handleCreatePlugin(w http.ResponseWriter, r *http.Request) {
	var req CustomPluginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := "cpl_" + hex.EncodeToString(b)

	_, err := s.db.ExecContext(r.Context(),
		"INSERT INTO plugins(id, name, description, manifest_url, publisher, trust_tier, enabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		id, req.Name, req.Description, req.ManifestURL, "user", 1, 1, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

type CustomAppReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req CustomAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := "cap_" + hex.EncodeToString(b)

	_, err := s.db.ExecContext(r.Context(),
		"INSERT INTO apps(id, name, description, url, publisher, trust_tier, enabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		id, req.Name, req.Description, req.URL, "user", 1, 1, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

type CustomMCPReq struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
}

func (s *Server) handleCreateMCP(w http.ResponseWriter, r *http.Request) {
	var req CustomMCPReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	id := "cmcp_" + hex.EncodeToString(b)

	argsBytes, _ := json.Marshal(req.Args)
	envBytes, _ := json.Marshal(req.Env)

	_, err := s.db.ExecContext(r.Context(),
		"INSERT INTO mcp_servers(id, name, transport, command, args, env, url, enabled, timeout, trust_tier, catalog_id, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
		id, req.Name, req.Transport, req.Command, string(argsBytes), string(envBytes), req.URL, 1, 30, 1, "", now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 启动 MCP Server
	if s.mcpMgr != nil {
		go s.startMCPServer(MCPServerConfig{
			ID:        id,
			Name:      req.Name,
			Transport: req.Transport,
			Command:   req.Command,
			Args:      req.Args,
			Env:       req.Env,
			URL:       req.URL,
			Timeout:   30,
			TrustTier: 1,
			Enabled:   true,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}
