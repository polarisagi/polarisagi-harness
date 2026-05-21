package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/mrlaoliai/polaris-harness/pkg/substrate/inference"
)

// LoadProvidersFromDB 从 providers + provider_models 两表 JOIN，
// 每个启用的 (provider, model) 组合注册一个带角色的 Adapter 到 ProviderRegistry。
func LoadProvidersFromDB(ctx context.Context, db *sql.DB, reg *inference.ProviderRegistry, httpClient *http.Client) error {
	rows, err := db.QueryContext(ctx, `
		SELECT p.id, p.type, p.base_url, p.api_key, p.project_id, p.location,
		       m.id, m.model_id, m.role
		  FROM providers p
		  JOIN provider_models m ON m.provider_id = p.id
		 WHERE p.enabled=1 AND m.enabled=1
		 ORDER BY p.created_at, m.created_at`)
	if err != nil {
		return err
	}
	defer rows.Close()

	reg.UnregisterAll()

	for rows.Next() {
		var pID, typ, baseURL, apiKey, projectID, location string
		var mID, modelID, role string
		if err := rows.Scan(&pID, &typ, &baseURL, &apiKey, &projectID, &location,
			&mID, &modelID, &role); err != nil {
			continue
		}

		keyCopy := apiKey
		credFn := func() string { return keyCopy }
		// 注册名使用 model 记录 ID 前缀，保证同厂商多模型不冲突
		name := fmt.Sprintf("%s/%s", typ, mID[:8])

		switch typ {
		case "openai_compat":
			reg.RegisterWithRole(name, role, inference.NewOpenAIAdapter(baseURL, modelID, credFn, httpClient))
		case "anthropic":
			reg.RegisterWithRole(name, role, inference.NewAnthropicAdapter(modelID, credFn, httpClient))
		case "google_agent_platform":
			reg.RegisterWithRole(name, role, inference.NewGoogleAgentPlatformAdapter(modelID, projectID, location, credFn, httpClient))
		case "ollama":
			if baseURL == "" {
				baseURL = "http://localhost:11434"
			}
			reg.RegisterWithRole(name, role, inference.NewOpenAIAdapter(baseURL+"/v1", modelID, credFn, httpClient))
		}
	}
	return rows.Err()
}
