package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// parseSkillMD 从 SKILL.md 中解析 Frontmatter 元数据。
// 这么做是为了提取技能名称、描述和标签，避免将其作为一个整体的大仓库对待。
func parseSkillMD(content string) (string, string, []string, string) {
	lines := strings.Split(content, "\n")
	firstDash := -1
	secondDash := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if firstDash == -1 {
				firstDash = i
			} else {
				secondDash = i
				break
			}
		}
	}

	if firstDash != -1 && secondDash != -1 && secondDash > firstDash+1 {
		yamlContent := strings.Join(lines[firstDash+1:secondDash], "\n")
		var fm struct {
			Name        string   `yaml:"name"`
			Description string   `yaml:"description"`
			Tags        []string `yaml:"tags"`
			ExecMode    string   `yaml:"exec_mode"`
		}
		if err := yaml.Unmarshal([]byte(yamlContent), &fm); err == nil {
			execMode := fm.ExecMode
			if execMode == "" {
				execMode = "tool"
			}
			return fm.Name, fm.Description, fm.Tags, execMode
		}
	}
	return "", "", nil, "tool"
}

// formatName 将连字符分隔的目录名格式化为人类可读的名称。
// 这是为了在 Frontmatter 中没有指定 name 字段时提供一个优雅的后备方案。
func formatName(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// parseSkillEntry 解析技能市场的 SKILL.md 文件并返回 protocol.RegistryEntry。
func parseSkillEntry(path string, mpDir string, mp protocol.Marketplace) (*protocol.RegistryEntry, error) {
	relDir, err := filepath.Rel(mpDir, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	relPath := filepath.ToSlash(relDir)

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	name, desc, tags, _ := parseSkillMD(string(contentBytes))
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
		name = formatName(name)
	}
	if desc == "" {
		desc = "Auto-detected skill in " + relPath
	}

	url := mp.RepoURL
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, "/") + "/tree/main/" + relPath
	}

	return &protocol.RegistryEntry{
		ID:          mp.ID + "/" + relPath,
		Publisher:   mp.Publisher,
		Type:        "skill",
		TrustTier:   mp.TrustTier,
		Name:        name,
		Description: desc,
		URL:         url,
		Tags:        tags,
		Timeout:     60,
	}, nil
}

// parsePluginEntry 解析插件市场的 plugin.json 文件并返回 protocol.RegistryEntry。
func parsePluginEntry(path string, mpDir string, mp protocol.Marketplace) (*protocol.RegistryEntry, error) {
	relDir, err := filepath.Rel(mpDir, filepath.Dir(path))
	if err != nil {
		return nil, err
	}

	// 如果 plugin.json 在 .claude-plugin 目录下，其上级目录才是插件主目录
	if filepath.Base(relDir) == ".claude-plugin" {
		relDir = filepath.Dir(relDir)
	}

	relPath := filepath.ToSlash(relDir)

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pJSON struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Keywords    []string `json:"keywords"`
	}
	var name, desc string
	var tags []string
	if err := json.Unmarshal(contentBytes, &pJSON); err == nil {
		name = pJSON.Name
		desc = pJSON.Description
		tags = pJSON.Keywords
	}

	if name == "" {
		name = filepath.Base(relDir)
		name = formatName(name)
	}
	if desc == "" {
		desc = "Auto-detected plugin in " + relPath
	}

	url := mp.RepoURL
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, "/") + "/tree/main/" + relPath
	}

	return &protocol.RegistryEntry{
		ID:          mp.ID + "/" + relPath,
		Publisher:   mp.Publisher,
		Type:        "plugin",
		TrustTier:   mp.TrustTier,
		Name:        name,
		Description: desc,
		URL:         url,
		Tags:        tags,
		Timeout:     60,
	}, nil
}

// parseMCPEntry 解析市场的 mcp.json 文件并返回 protocol.RegistryEntry。
func parseMCPEntry(path string, mpDir string, mp protocol.Marketplace) (*protocol.RegistryEntry, error) {
	relDir, err := filepath.Rel(mpDir, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	relPath := filepath.ToSlash(relDir)

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var mJSON struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Transport   string            `json:"transport"`
		Command     string            `json:"command"`
		Args        []string          `json:"args"`
		Env         map[string]string `json:"env"`
		Keywords    []string          `json:"keywords"`
	}
	if err := json.Unmarshal(contentBytes, &mJSON); err != nil {
		return nil, err
	}

	name := mJSON.Name
	if name == "" {
		name = filepath.Base(relDir)
		name = formatName(name)
	}

	url := mp.RepoURL
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, "/") + "/tree/main/" + relPath
	}

	return &protocol.RegistryEntry{
		ID:          mp.ID + "/" + relPath,
		Publisher:   mp.Publisher,
		Type:        "mcp",
		TrustTier:   mp.TrustTier,
		Name:        name,
		Description: mJSON.Description,
		URL:         url,
		Tags:        mJSON.Keywords,
		Timeout:     60,
		Transport:   mJSON.Transport,
		Command:     mJSON.Command,
		Args:        mJSON.Args,
		Env:         mJSON.Env,
	}, nil
}

// discoverMarketplaceEntries 递归遍历市场目录，自动发现所有的插件和技能。
// 这解决了插件/技能页面只列出整个市场仓库的系统漏洞。
func discoverMarketplaceEntries(mpDir string, mp protocol.Marketplace) ([]protocol.RegistryEntry, error) { //nolint:gocyclo
	var entries []protocol.RegistryEntry

	err := filepath.Walk(mpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// 寻找所有 SKILL.md
		if info.Name() == "SKILL.md" {
			entry, err := parseSkillEntry(path, mpDir, mp)
			if err != nil {
				return err
			}
			if entry != nil {
				entries = append(entries, *entry)
			}
		}

		// 寻找所有 plugin.json
		if info.Name() == "plugin.json" {
			entry, err := parsePluginEntry(path, mpDir, mp)
			if err != nil {
				return err
			}
			if entry != nil {
				entries = append(entries, *entry)
			}
		}

		// 寻找所有 mcp.json
		if info.Name() == "mcp.json" {
			entry, err := parseMCPEntry(path, mpDir, mp)
			if err != nil {
				return err
			}
			if entry != nil {
				entries = append(entries, *entry)
			}
		}

		// OpenAI ai-plugin.json
		if info.Name() == "ai-plugin.json" {
			if entry, err := parseAIPluginEntry(path, mpDir, mp); err == nil && entry != nil {
				entries = append(entries, *entry)
			}
		}

		// Anthropic plugin.toml（根目录或 .claude-plugin/ 子目录）
		if info.Name() == "plugin.toml" {
			if entry, err := parsePluginTOMLEntry(path, mpDir, mp); err == nil && entry != nil {
				entries = append(entries, *entry)
			}
		}

		// Google Agent Skills skills.yaml / agent-manifest.yaml
		if info.Name() == "skills.yaml" || info.Name() == "agent-manifest.yaml" {
			newEntries, err := parseGoogleSkillsEntry(path, mpDir, mp)
			if err == nil {
				entries = append(entries, newEntries...)
			}
		}

		return nil
	})

	return entries, err
}

// pullOrClone 尝试执行 git pull 或 clone，返回是否有实质更新。
func pullOrClone(repoURL, mpDir, gitDir string) bool {
	if _, err := os.Stat(gitDir); err == nil {
		cmd := exec.Command("git", "-C", mpDir, "pull")
		output, err := cmd.CombinedOutput()
		if err == nil {
			return !strings.Contains(string(output), "Already up to date.")
		}
		os.RemoveAll(mpDir)
	} else {
		os.RemoveAll(mpDir)
	}

	if _, err := os.Stat(mpDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth", "1", repoURL, mpDir)
		if err := cmd.Run(); err != nil {
			return false
		}
		return true
	}
	return false
}

// syncMarketplace 同步单个市场
func (s *Server) syncMarketplace(ctx context.Context, mp protocol.Marketplace, tmpDir string) int {
	if mp.RepoURL == "" {
		return 0
	}

	safeID := strings.ReplaceAll(mp.ID, "/", "_")
	mpDir := filepath.Join(tmpDir, safeID)
	gitDir := filepath.Join(mpDir, ".git")

	if !pullOrClone(mp.RepoURL, mpDir, gitDir) {
		return 0 // 如果没有更新或拉取失败，直接跳过解析和写库，节省大量时间
	}

	b, err := os.ReadFile(filepath.Join(mpDir, "catalog.json"))
	if err != nil {
		entries, scanErr := discoverMarketplaceEntries(mpDir, mp)
		if scanErr == nil && len(entries) > 0 {
			b, _ = json.Marshal(entries)
		} else {
			return 0
		}
	}

	var entries []protocol.RegistryEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return 0
	}

	syncedCount := 0
	// 对当前有更新的市场单独开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err == nil {
		_, _ = tx.ExecContext(ctx, "DELETE FROM extension_catalog WHERE marketplace_id = ?", mp.ID)
		for _, e := range entries {
			e.Publisher = mp.Publisher
			e.TrustTier = mp.TrustTier
			payload, _ := json.Marshal(e)

			_, _ = tx.ExecContext(ctx,
				`INSERT INTO extension_catalog(id, marketplace_id, type, name, description, publisher, trust_tier, url, payload) 
				VALUES(?,?,?,?,?,?,?,?,?)`,
				e.ID, mp.ID, e.Type, e.Name, e.Description, mp.Publisher, mp.TrustTier, e.URL, string(payload))
			syncedCount++
		}
		_ = tx.Commit()
	}

	return syncedCount
}

// SyncAllMarketplaces 后台静默同步所有可用市场并更新缓存
func (s *Server) SyncAllMarketplaces(ctx context.Context) (int, error) {
	var mps []protocol.Marketplace
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at FROM plugin_marketplaces WHERE enabled=1")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var m protocol.Marketplace
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Publisher, &m.RepoURL, &m.Description, &m.IsBuiltin, &m.TrustTier, &m.Enabled, &m.CreatedAt); err == nil {
			mps = append(mps, m)
		}
	}
	rows.Close()

	home, _ := os.UserHomeDir()
	tmpDir := filepath.Join(home, ".polaris-harness", "tmp", "marketplaces")
	_ = os.MkdirAll(tmpDir, 0755)

	// 首先清理已经从活跃列表中移除的孤儿市场缓存
	activeIDs := make([]any, 0, len(mps))
	queryMarks := ""
	for i, mp := range mps {
		activeIDs = append(activeIDs, mp.ID)
		if i > 0 {
			queryMarks += ","
		}
		queryMarks += "?"
	}
	if len(activeIDs) > 0 {
		delOrphanQuery := "DELETE FROM extension_catalog WHERE marketplace_id != 'builtin' AND marketplace_id NOT IN (" + queryMarks + ")"
		_, _ = s.db.ExecContext(ctx, delOrphanQuery, activeIDs...)
	} else {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM extension_catalog WHERE marketplace_id != 'builtin'")
	}

	syncedCount := 0
	for _, mp := range mps {
		syncedCount += s.syncMarketplace(ctx, mp, tmpDir)
	}

	return syncedCount, nil
}

// parseAIPluginEntry 解析 OpenAI ai-plugin.json 格式。
// api.type=="mcp" 时映射为 mcp 条目；其余映射为 app（URL 直连）。
func parseAIPluginEntry(path, mpDir string, mp protocol.Marketplace) (*protocol.RegistryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p protocol.AIPluginJSON
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	name := p.NameForHuman
	if name == "" {
		name = p.NameForModel
	}
	if name == "" {
		return nil, nil
	}
	desc := p.DescriptionForHuman
	if desc == "" {
		desc = p.DescriptionForModel
	}

	relDir, _ := filepath.Rel(mpDir, filepath.Dir(path))
	relPath := filepath.ToSlash(relDir)

	extType := "app"
	command := ""
	if strings.EqualFold(p.API.Type, "mcp") {
		extType = "mcp"
		command = p.API.URL
	}

	url := p.API.URL
	if strings.Contains(mp.RepoURL, "github.com") && url == "" {
		url = strings.TrimSuffix(mp.RepoURL, "/") + "/tree/main/" + relPath
	}

	return &protocol.RegistryEntry{
		ID:          mp.ID + "/" + relPath,
		Publisher:   mp.Publisher,
		Type:        extType,
		TrustTier:   mp.TrustTier,
		Name:        name,
		Description: desc,
		URL:         url,
		Homepage:    p.LegalInfoURL,
		Command:     command,
		Timeout:     60,
	}, nil
}

// parsePluginTOMLEntry 解析 Anthropic plugin.toml（根目录或 .claude-plugin/ 下）。
func parsePluginTOMLEntry(path, mpDir string, mp protocol.Marketplace) (*protocol.RegistryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p protocol.AnthropicPluginTOML
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Plugin.Name == "" && p.MCP.Command == "" {
		return nil, nil
	}

	relDir, _ := filepath.Rel(mpDir, filepath.Dir(path))
	// plugin.toml 在 .claude-plugin/ 子目录时，ID 取其上级目录
	if filepath.Base(relDir) == ".claude-plugin" {
		relDir = filepath.Dir(relDir)
	}
	relPath := filepath.ToSlash(relDir)

	extType := "mcp"
	if p.MCP.Command == "" {
		extType = "plugin"
	}

	url := mp.RepoURL
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, "/") + "/tree/main/" + relPath
	}

	return &protocol.RegistryEntry{
		ID:          mp.ID + "/" + relPath,
		Publisher:   mp.Publisher,
		Type:        extType,
		TrustTier:   mp.TrustTier,
		Name:        p.Plugin.Name,
		Description: p.Plugin.Description,
		URL:         url,
		Command:     p.MCP.Command,
		Args:        p.MCP.Args,
		Env:         p.MCP.Env,
		Timeout:     60,
	}, nil
}

// parseGoogleSkillsEntry 解析 Google skills.yaml / agent-manifest.yaml 格式。
// 顶层有 command 时映射为 mcp；否则映射为 skill。多 skills 列表逐条展开。
func parseGoogleSkillsEntry(path, mpDir string, mp protocol.Marketplace) ([]protocol.RegistryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g protocol.GoogleSkillsYAML
	if err := yaml.Unmarshal(data, &g); err != nil {
		return nil, err
	}

	relDir, _ := filepath.Rel(mpDir, filepath.Dir(path))
	relPath := filepath.ToSlash(relDir)
	baseID := mp.ID + "/" + relPath

	baseURL := mp.RepoURL
	if strings.Contains(baseURL, "github.com") {
		baseURL = strings.TrimSuffix(baseURL, "/") + "/tree/main/" + relPath
	}

	// 单条目
	if g.Name != "" && len(g.Skills) == 0 {
		extType := "skill"
		if g.Command != "" {
			extType = "mcp"
		}
		return []protocol.RegistryEntry{{
			ID:          baseID,
			Publisher:   mp.Publisher,
			Type:        extType,
			TrustTier:   mp.TrustTier,
			Name:        g.Name,
			Description: g.Description,
			URL:         baseURL,
			Command:     g.Command,
			Args:        g.Args,
			Timeout:     60,
		}}, nil
	}

	// 多技能列表
	entries := make([]protocol.RegistryEntry, 0, len(g.Skills))
	for i, s := range g.Skills {
		if s.Name == "" {
			continue
		}
		extType := "skill"
		if s.Command != "" {
			extType = "mcp"
		}
		entries = append(entries, protocol.RegistryEntry{
			ID:          fmt.Sprintf("%s/skill_%d", baseID, i),
			Publisher:   mp.Publisher,
			Type:        extType,
			TrustTier:   mp.TrustTier,
			Name:        s.Name,
			Description: s.Description,
			URL:         baseURL,
			Command:     s.Command,
			Args:        s.Args,
			Timeout:     60,
		})
	}
	return entries, nil
}

// handleSyncMarketplaces 刷新/同步市场
func (s *Server) handleSyncMarketplaces(w http.ResponseWriter, r *http.Request) {
	syncedCount, err := s.SyncAllMarketplaces(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "synced", "synced_count": syncedCount})
}
