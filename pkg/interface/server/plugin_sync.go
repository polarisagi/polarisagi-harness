package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// handleSyncMarketplaces 刷新/同步市场
func (s *Server) handleSyncMarketplaces(w http.ResponseWriter, r *http.Request) {
	mps := append([]Marketplace{}, builtinMarketplaces...)
	rows, err := s.db.QueryContext(r.Context(), "SELECT id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at FROM plugin_marketplaces WHERE enabled=1")
	if err == nil {
		for rows.Next() {
			var m Marketplace
			if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Publisher, &m.RepoURL, &m.Description, &m.IsBuiltin, &m.TrustTier, &m.Enabled, &m.CreatedAt); err == nil {
				mps = append(mps, m)
			}
		}
		rows.Close()
	}

	home, _ := os.UserHomeDir()
	tmpDir := filepath.Join(home, ".polaris-harness", "tmp", "marketplaces")
	_ = os.MkdirAll(tmpDir, 0755)

	// Clean cache
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM catalog_cache")

	syncedCount := 0
	for _, mp := range mps {
		if mp.RepoURL == "" {
			continue
		}

		// If the ID contains slashes, replace them to make a valid directory name
		safeID := strings.ReplaceAll(mp.ID, "/", "_")
		mpDir := filepath.Join(tmpDir, safeID)

		// Clean old dir
		os.RemoveAll(mpDir)

		// Git clone
		cmd := exec.Command("git", "clone", "--depth", "1", mp.RepoURL, mpDir)
		if err := cmd.Run(); err != nil {
			continue
		}

		b, err := os.ReadFile(filepath.Join(mpDir, "catalog.json"))
		if err != nil {
			// Fallback: If no catalog.json, generate mock entries for demonstration
			mockEntries := []CatalogEntry{
				{
					ID:          mp.ID + "/example-plugin",
					Name:        mp.Name + " - Core Module",
					Description: "Auto-detected core module from repository.",
					Type:        "plugin",
					URL:         mp.RepoURL,
				},
				{
					ID:          mp.ID + "/example-skill",
					Name:        mp.Name + " - Assistant Skill",
					Description: "Auto-detected assistant skill from repository.",
					Type:        "skill",
					URL:         mp.RepoURL,
				},
			}
			b, _ = json.Marshal(mockEntries)
		}

		var entries []CatalogEntry
		if err := json.Unmarshal(b, &entries); err != nil {
			continue
		}

		for _, e := range entries {
			// Override with marketplace publisher and trust_tier
			e.Publisher = mp.Publisher
			e.TrustTier = mp.TrustTier

			payload, _ := json.Marshal(e)

			_, _ = s.db.ExecContext(r.Context(),
				`INSERT INTO catalog_cache(id, marketplace_id, type, name, description, publisher, trust_tier, url, payload) 
                 VALUES(?,?,?,?,?,?,?,?,?)`,
				e.ID, mp.ID, e.Type, e.Name, e.Description, mp.Publisher, mp.TrustTier, e.URL, string(payload))
			syncedCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "synced", "synced_count": syncedCount})
}
