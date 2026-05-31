package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/polarisagi/polarisagi-harness/pkg/extensions/mcp"
)

type pluginRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Publisher   string `json:"publisher"`
	Enabled     bool   `json:"enabled"`
	TrustTier   int    `json:"trust_tier"`
	MCPPolicy   string `json:"mcp_policy"`
	InstallPath string `json:"install_path"`
	CatalogID   string `json:"catalog_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type pluginMCPStatus struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

type pluginResponse struct {
	pluginRow
	MCPServers []pluginMCPStatus `json:"mcp_servers"`
}

// handleListPlugins 返回已安装插件列表（来自 plugins 表），含子 MCP 运行时状态。
// GET /v1/plugins
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, name, version, display_name, description, publisher, enabled,
		        trust_tier, mcp_policy, install_path, catalog_id, created_at, updated_at
		 FROM plugins ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 预取 mcpMgr 快照：serverID → info（含 Connected 状态）
	connectedMCPs := make(map[string]mcp.MCPServerInfo)
	if s.mcpMgr != nil {
		for _, srv := range s.mcpMgr.ListServers() {
			connectedMCPs[srv.ID] = srv
		}
	}

	var result []pluginResponse
	for rows.Next() {
		var p pluginRow
		var enabledInt int
		if err := rows.Scan(&p.ID, &p.Name, &p.Version, &p.DisplayName, &p.Description,
			&p.Publisher, &enabledInt, &p.TrustTier, &p.MCPPolicy, &p.InstallPath,
			&p.CatalogID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		p.Enabled = enabledInt == 1

		var mcpPolicy map[string]map[string]any
		_ = json.Unmarshal([]byte(p.MCPPolicy), &mcpPolicy)

		var mcpStatuses []pluginMCPStatus
		for serverName, policyEntry := range mcpPolicy {
			serverID := fmt.Sprintf("plugin_%s_%s", p.ID, serverName)
			policyEnabled := true
			if v, ok := policyEntry["enabled"].(bool); ok {
				policyEnabled = v
			}
			status := pluginMCPStatus{Name: serverName, Enabled: policyEnabled}
			if info, ok := connectedMCPs[serverID]; ok {
				status.Connected = info.Connected
				status.ToolCount = len(info.Tools)
				status.Error = info.Error
			}
			mcpStatuses = append(mcpStatuses, status)
		}
		if mcpStatuses == nil {
			mcpStatuses = []pluginMCPStatus{}
		}

		result = append(result, pluginResponse{pluginRow: p, MCPServers: mcpStatuses})
	}

	if result == nil {
		result = []pluginResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": result, "total": len(result)})
}

// handleUpdatePlugin 更新插件启用状态或 mcp_policy，并实时同步 MCPManager。
// PUT /v1/plugins/{id}
func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")
	if pluginID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	var req struct {
		Enabled   *bool                     `json:"enabled"`
		MCPPolicy map[string]map[string]any `json:"mcp_policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var name, installPath, mcpPolicyJSON string
	var currentEnabled, trustTier int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT name, install_path, enabled, trust_tier, mcp_policy FROM plugins WHERE id=?`, pluginID).
		Scan(&name, &installPath, &currentEnabled, &trustTier, &mcpPolicyJSON)
	if err != nil {
		http.Error(w, "plugin not found", http.StatusNotFound)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	newEnabled := currentEnabled
	if req.Enabled != nil {
		if *req.Enabled {
			newEnabled = 1
		} else {
			newEnabled = 0
		}
	}

	newMCPPolicy := mcpPolicyJSON
	if req.MCPPolicy != nil {
		b, _ := json.Marshal(req.MCPPolicy)
		newMCPPolicy = string(b)
	}

	if _, err = s.db.ExecContext(r.Context(),
		`UPDATE plugins SET enabled=?, mcp_policy=?, updated_at=? WHERE id=?`,
		newEnabled, newMCPPolicy, now, pluginID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 实时同步 MCPManager
	if s.mcpMgr != nil && req.Enabled != nil && currentEnabled != newEnabled {
		var policy map[string]map[string]any
		_ = json.Unmarshal([]byte(newMCPPolicy), &policy)

		if newEnabled == 0 {
			for serverName := range policy {
				s.mcpMgr.Remove(fmt.Sprintf("plugin_%s_%s", pluginID, serverName))
			}
		} else {
			home, _ := os.UserHomeDir()
			dataDir := filepath.Join(home, ".polarisagi/harness")
			go s.mcpMgr.LoadOnePlugin(r.Context(), pluginID, name, installPath, newMCPPolicy, trustTier, dataDir)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "updated", "id": pluginID})
}

// handleTogglePluginMCP 切换插件内单个子 MCP 的启用状态。
// PATCH /v1/plugins/{id}/mcp/{serverName}
func (s *Server) handleTogglePluginMCP(w http.ResponseWriter, r *http.Request) {
	pluginID := r.PathValue("id")
	serverName := r.PathValue("serverName")
	if pluginID == "" || serverName == "" {
		http.Error(w, "id and serverName required", http.StatusBadRequest)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var name, installPath, mcpPolicyJSON string
	var trustTier int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT name, install_path, trust_tier, mcp_policy FROM plugins WHERE id=? AND enabled=1`, pluginID).
		Scan(&name, &installPath, &trustTier, &mcpPolicyJSON)
	if err != nil {
		http.Error(w, "plugin not found or disabled", http.StatusNotFound)
		return
	}

	var policy map[string]map[string]any
	if err := json.Unmarshal([]byte(mcpPolicyJSON), &policy); err != nil {
		policy = make(map[string]map[string]any)
	}
	if policy[serverName] == nil {
		policy[serverName] = make(map[string]any)
	}
	policy[serverName]["enabled"] = req.Enabled

	newPolicyBytes, _ := json.Marshal(policy)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = s.db.ExecContext(r.Context(),
		`UPDATE plugins SET mcp_policy=?, updated_at=? WHERE id=?`,
		string(newPolicyBytes), now, pluginID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	serverID := fmt.Sprintf("plugin_%s_%s", pluginID, serverName)
	if s.mcpMgr != nil {
		if !req.Enabled {
			s.mcpMgr.Remove(serverID)
		} else {
			home, _ := os.UserHomeDir()
			dataDir := filepath.Join(home, ".polarisagi/harness")
			go s.mcpMgr.LoadOnePlugin(r.Context(), pluginID, name, installPath, string(newPolicyBytes), trustTier, dataDir)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "updated",
		"plugin_id": pluginID,
		"server":    serverName,
		"enabled":   req.Enabled,
	})
}
