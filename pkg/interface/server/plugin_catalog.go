package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"time"
)

// CatalogEntry 插件目录条目（ADR-0016：增加 Publisher/TrustTier/Type 字段）。
type CatalogEntry struct {
	// ID 全局唯一 slug，格式："{publisher}/{name}" 或 "mcp/{name}"
	ID string `json:"id"`
	// Publisher 来源组织（openai / anthropic / google / modelcontextprotocol / github / microsoft / figma / community）
	Publisher string `json:"publisher"`
	// Type 条目类型："mcp" | "skill" | "plugin"（bundle）
	Type string `json:"type"`
	// TrustTier 信任级别（ADR-0016 §2.1）。由 Polaris 白名单决定，非 author 自定义。
	// 0=Untrusted, 1=Local, 2=Community, 3=Official, 4=System
	TrustTier int `json:"trust_tier"`

	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport,omitempty"` // "stdio" | "sse" | "streamable_http"
	Command     string            `json:"command,omitempty"`   // stdio 命令，{DATA_DIR} 为占位符
	Args        []string          `json:"args,omitempty"`      // 默认参数，{DATA_DIR} 为占位符
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"` // SSE / Streamable HTTP 端点
	Tags        []string          `json:"tags"`
	Homepage    string            `json:"homepage,omitempty"`
	Timeout     int               `json:"timeout"` // 推荐超时秒数
	// 运行时叠加：是否已安装（mcp_servers 表中存在同 catalog_id）
	Installed bool `json:"installed"`
}

// builtinCatalog 内置推荐插件目录。
// 来源：MCP 官方 servers（TrustOfficial=3）+ 三大平台官方工具（TrustOfficial=3）。
// TrustTier 由 Polaris 白名单决定（ADR-0016 §2.1），非 author 自定义。
var builtinCatalog = []CatalogEntry{
	// ── ModelContextProtocol 官方（TrustTier=3）────────────────────────────
	{
		ID:          "mcp/filesystem",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Filesystem",
		Description: "读写本地文件系统：支持读文件、写文件、目录列举、文件搜索和 diff。适合让 AI 直接操作工作目录。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", "{DATA_DIR}"},
		Tags:        []string{"files", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
		Timeout:     30,
	},
	{
		ID:          "mcp/fetch",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Fetch",
		Description: "抓取公开网页内容并提取纯文本，支持 HTML → Markdown 转换。让 AI 实时获取网络资料。",
		Transport:   "stdio",
		Command:     "uvx",
		Args:        []string{"mcp-server-fetch"},
		Tags:        []string{"web", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
		Timeout:     30,
	},
	{
		ID:          "mcp/brave-search",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Brave Search",
		Description: "通过 Brave Search API 执行网页和新闻搜索，返回结构化结果。需要 Brave API Key。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-brave-search"},
		Env:         map[string]string{"BRAVE_API_KEY": ""},
		Tags:        []string{"search", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search",
		Timeout:     30,
	},
	{
		ID:          "mcp/github",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "GitHub",
		Description: "操作 GitHub 仓库：读写文件、搜索代码、管理 Issue/PR、fork 仓库。需要 GitHub Token。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-github"},
		Env:         map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": ""},
		Tags:        []string{"dev", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/github",
		Timeout:     30,
	},
	{
		ID:          "mcp/sqlite",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "SQLite",
		Description: "对本地 SQLite 数据库执行 SQL 查询（读写），并提供表结构洞察功能。",
		Transport:   "stdio",
		Command:     "uvx",
		Args:        []string{"mcp-server-sqlite", "--db-path", "{DATA_DIR}/polaris.db"},
		Tags:        []string{"database", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sqlite",
		Timeout:     30,
	},
	{
		ID:          "mcp/git",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Git",
		Description: "Git 仓库操作：读取历史提交、diff、blame、branch 管理、暂存与提交。",
		Transport:   "stdio",
		Command:     "uvx",
		Args:        []string{"mcp-server-git", "--repository", "{DATA_DIR}"},
		Tags:        []string{"dev", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/git",
		Timeout:     30,
	},
	{
		ID:          "mcp/memory",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Memory",
		Description: "基于知识图谱的持久化记忆系统，支持跨会话存储实体关系。让 AI 记住用户偏好和项目上下文。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-memory"},
		Tags:        []string{"memory", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/memory",
		Timeout:     30,
	},
	{
		ID:          "mcp/sequential-thinking",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Sequential Thinking",
		Description: "为复杂问题提供结构化的逐步推理框架，支持反思和修正，避免推理跳跃。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
		Tags:        []string{"reasoning", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking",
		Timeout:     30,
	},
	{
		ID:          "mcp/puppeteer",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Puppeteer",
		Description: "浏览器自动化：截图、填表、点击交互、抓取动态 JS 渲染页面。无头浏览器驱动。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-puppeteer"},
		Tags:        []string{"browser", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/puppeteer",
		Timeout:     60,
	},
	{
		ID:          "mcp/slack",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Slack",
		Description: "读取 Slack 频道历史、发送消息、列出用户和频道。需要 Slack Bot Token。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-slack"},
		Env:         map[string]string{"SLACK_BOT_TOKEN": "", "SLACK_TEAM_ID": ""},
		Tags:        []string{"communication", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/slack",
		Timeout:     30,
	},
	{
		ID:          "mcp/time",
		Publisher:   "modelcontextprotocol",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Time",
		Description: "时区转换工具，获取任意时区当前时间并在时区之间转换。轻量级无依赖。",
		Transport:   "stdio",
		Command:     "uvx",
		Args:        []string{"mcp-server-time"},
		Tags:        []string{"utility", "official"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/time",
		Timeout:     10,
	},

	// (Marketplaces moved to builtinMarketplaces)

	// ── Concrete Skills (Mock Data for Skills Tab) ────────────────────────
	{
		ID:          "skill/web-search",
		Publisher:   "community",
		Type:        "skill",
		TrustTier:   2,
		Name:        "Web Search",
		Description: "通用搜索引擎技能，可以联网搜索最新的网页资料和新闻，返回 Markdown 格式的结果摘要。",
		URL:         "https://github.com/community/web-search-skill",
		Tags:        []string{"search", "web"},
		Timeout:     30,
	},
	{
		ID:          "skill/data-analyzer",
		Publisher:   "community",
		Type:        "skill",
		TrustTier:   2,
		Name:        "Data Analyzer",
		Description: "对 CSV、JSON 或数据库结果进行深度清洗、分析并生成图表代码的内置数据分析技能。",
		URL:         "https://github.com/community/data-analyzer",
		Tags:        []string{"data", "analysis"},
		Timeout:     60,
	},
	{
		ID:          "skill/github-pr-reviewer",
		Publisher:   "community",
		Type:        "skill",
		TrustTier:   2,
		Name:        "GitHub PR Reviewer",
		Description: "自动化读取 PR 差异并进行代码审查，自动生成审查意见与修改建议。",
		URL:         "https://github.com/community/gh-pr-reviewer",
		Tags:        []string{"github", "review", "dev"},
		Timeout:     120,
	},

	// ── Concrete Plugins (Mock Data for Plugins Tab) ──────────────────────
	{
		ID:          "plugin/jira-integration",
		Publisher:   "community",
		Type:        "plugin",
		TrustTier:   2,
		Name:        "Jira Integration",
		Description: "完整的 Jira 工作流插件。支持创建 Issue、查询 Sprint 进度、自动添加评论和流转工单状态。",
		URL:         "https://github.com/community/jira-plugin",
		Tags:        []string{"project-management", "jira"},
		Timeout:     30,
	},
	{
		ID:          "plugin/aws-manager",
		Publisher:   "community",
		Type:        "plugin",
		TrustTier:   2,
		Name:        "AWS Manager",
		Description: "AWS 资源管理插件（整合了多个相关 Skills）。支持查看 EC2 实例状态、S3 桶操作以及简单的 IAM 权限分析。",
		URL:         "https://github.com/community/aws-manager-plugin",
		Tags:        []string{"cloud", "aws", "devops"},
		Timeout:     60,
	},

	// ── GitHub 官方（TrustTier=3）─────────────────────────────────────────
	{
		ID:          "github/mcp-server",
		Publisher:   "github",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "GitHub MCP Server",
		Description: "GitHub 官方 MCP Server，原生集成 GitHub API：仓库管理、代码搜索、Issue/PR 操作、Actions 触发、Copilot 工作流。需要 GITHUB_TOKEN。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"-y", "@github/mcp-server"},
		Env:         map[string]string{"GITHUB_TOKEN": ""},
		Tags:        []string{"dev", "github", "official"},
		Homepage:    "https://github.com/github/mcp-server",
		Timeout:     30,
	},

	// ── Microsoft 官方（TrustTier=3）──────────────────────────────────────
	{
		ID:          "microsoft/playwright-mcp",
		Publisher:   "microsoft",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Playwright MCP",
		Description: "Microsoft 官方 Playwright MCP Server，提供结构化浏览器自动化：页面导航、元素交互、表单填写、截图、PDF 导出。基于 accessibility tree，LLM 友好。",
		Transport:   "stdio",
		Command:     "npx",
		Args:        []string{"@playwright/mcp@latest"},
		Tags:        []string{"browser", "automation", "official"},
		Homepage:    "https://github.com/microsoft/playwright-mcp",
		Timeout:     60,
	},

	// ── Figma 官方（TrustTier=3）──────────────────────────────────────────
	{
		ID:          "figma/mcp-server",
		Publisher:   "figma",
		Type:        "mcp",
		TrustTier:   3,
		Name:        "Figma MCP Server",
		Description: "Figma 官方 MCP Server，让 AI 读取设计文件结构、组件层级、样式变量，实现设计稿到代码的精准转换。需要 FIGMA_API_KEY。",
		Transport:   "streamable_http",
		URL:         "https://mcp.figma.com/mcp",
		Env:         map[string]string{"FIGMA_API_KEY": ""},
		Tags:        []string{"design", "ui", "official"},
		Homepage:    "https://www.figma.com/developers/mcp",
		Timeout:     30,
	},
}

// handleListPluginCatalog 返回内置推荐插件目录列表，并叠加已安装状态。
// GET /v1/plugins/catalog
func (s *Server) getInstalledCatalogIDs(ctx context.Context) map[string]bool {
	installed := map[string]bool{}
	queries := []string{
		`SELECT catalog_id FROM mcp_servers WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM skill_sources WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM skills WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM plugins WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
		`SELECT catalog_id FROM apps WHERE catalog_id != '' AND catalog_id IS NOT NULL`,
	}
	for _, query := range queries {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			continue
		}
		for rows.Next() {
			var cid string
			if rows.Scan(&cid) == nil {
				installed[cid] = true
			}
		}
		rows.Close()
	}
	return installed
}

func (s *Server) appendCustomCatalogs(ctx context.Context, result []CatalogEntry, installed map[string]bool) []CatalogEntry {
	// Fetch Custom MCP
	if rows, err := s.db.QueryContext(ctx, `SELECT id, name, transport, command, url FROM mcp_servers WHERE catalog_id = ''`); err == nil {
		for rows.Next() {
			var m CatalogEntry
			m.Type, m.Publisher, m.Installed = "mcp", "user", true
			if err := rows.Scan(&m.ID, &m.Name, &m.Transport, &m.Command, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows.Close()
	}

	// Fetch Custom Skills
	if rows2, err := s.db.QueryContext(ctx, `SELECT id, name, description, repo_url FROM skills`); err == nil {
		for rows2.Next() {
			var m CatalogEntry
			m.Type, m.Publisher, m.Installed = "skill", "user", true
			if err := rows2.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows2.Close()
	}

	// Fetch Custom Plugins
	if rows3, err := s.db.QueryContext(ctx, `SELECT id, name, description, manifest_url FROM plugins`); err == nil {
		for rows3.Next() {
			var m CatalogEntry
			m.Type, m.Publisher, m.Installed = "plugin", "user", true
			if err := rows3.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows3.Close()
	}

	// Fetch Custom Apps
	if rows4, err := s.db.QueryContext(ctx, `SELECT id, name, description, url FROM apps`); err == nil {
		for rows4.Next() {
			var m CatalogEntry
			m.Type, m.Publisher, m.Installed = "app", "user", true
			if err := rows4.Scan(&m.ID, &m.Name, &m.Description, &m.URL); err == nil {
				result = append(result, m)
			}
		}
		rows4.Close()
	}
	return result
}

func (s *Server) appendCachedCatalogs(ctx context.Context, result []CatalogEntry, installed map[string]bool) []CatalogEntry {
	// Fetch Marketplaces Cached Catalog
	if rows5, err := s.db.QueryContext(ctx, `SELECT payload FROM catalog_cache`); err == nil {
		for rows5.Next() {
			var payload string
			if err := rows5.Scan(&payload); err == nil {
				var entry CatalogEntry
				if err := json.Unmarshal([]byte(payload), &entry); err == nil {
					entry.Installed = installed[entry.ID]
					result = append(result, entry)
				}
			}
		}
		rows5.Close()
	}
	return result
}

// handleListPluginCatalog 返回内置推荐插件目录列表，并叠加已安装状态。
// GET /v1/plugins/catalog
func (s *Server) handleListPluginCatalog(w http.ResponseWriter, r *http.Request) {
	installed := s.getInstalledCatalogIDs(r.Context())

	result := make([]CatalogEntry, 0, len(builtinCatalog))
	for _, e := range builtinCatalog {
		e.Installed = installed[e.ID]
		result = append(result, e)
	}

	result = s.appendCustomCatalogs(r.Context(), result, installed)
	result = s.appendCachedCatalogs(r.Context(), result, installed)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"catalog": result,
		"total":   len(result),
	})
}

// pluginInstallRequest 一键安装请求体。
type pluginInstallRequest struct {
	CatalogID string `json:"catalog_id"`
	// 可选覆盖：若不传则使用 catalog 默认值
	Name    string            `json:"name,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

// handleInstallPlugin 一键安装推荐插件。
// Type=mcp → 写入 mcp_servers 并异步连接；Type=skill/plugin → 写入 skill_sources。
// POST /v1/plugins/install
func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req pluginInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.CatalogID == "" {
		http.Error(w, "catalog_id is required", http.StatusBadRequest)
		return
	}

	// 查找 catalog 条目
	var entry *CatalogEntry
	for i := range builtinCatalog {
		if builtinCatalog[i].ID == req.CatalogID {
			e := builtinCatalog[i]
			entry = &e
			break
		}
	}
	if entry == nil {
		http.Error(w, "catalog entry not found: "+req.CatalogID, http.StatusNotFound)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	switch entry.Type {
	case "skill", "plugin":
		s.installSkillSource(w, r, entry, req, b, now)
	default: // "mcp" 及空值
		s.installMCPServer(w, r, entry, req, b, now)
	}
}

// installSkillSource 将 skill/plugin 类型 catalog 条目写入 skill_sources 表。
func (s *Server) installSkillSource(w http.ResponseWriter, r *http.Request,
	entry *CatalogEntry, req pluginInstallRequest, b []byte, now string) {

	// 防重复安装
	var existCount int
	s.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM skill_sources WHERE catalog_id=?`, req.CatalogID).Scan(&existCount) //nolint:errcheck
	if existCount > 0 {
		http.Error(w, "already installed", http.StatusConflict)
		return
	}

	srcID := "src_" + hex.EncodeToString(b)
	name := entry.Name
	if req.Name != "" {
		name = req.Name
	}
	repoURL := entry.URL
	if req.URL != "" {
		repoURL = req.URL
	}

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO skill_sources(id, name, type, publisher, trust_tier, repo_url, catalog_id, enabled, created_at, updated_at)
         VALUES(?,?,?,?,?,?,?,1,?,?)`,
		srcID, name, entry.Type, entry.Publisher, entry.TrustTier, repoURL, req.CatalogID, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":         srcID,
		"type":       entry.Type,
		"name":       name,
		"publisher":  entry.Publisher,
		"trust_tier": entry.TrustTier,
		"repo_url":   repoURL,
		"catalog_id": req.CatalogID,
		"created_at": now,
	})
}

// installMCPServer 将 mcp 类型 catalog 条目写入 mcp_servers 表并异步连接。
func (s *Server) installMCPServer(w http.ResponseWriter, r *http.Request,
	entry *CatalogEntry, req pluginInstallRequest, b []byte, now string) {

	// 防重复安装
	var existCount int
	s.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM mcp_servers WHERE catalog_id=?`, req.CatalogID).Scan(&existCount) //nolint:errcheck
	if existCount > 0 {
		http.Error(w, "plugin already installed", http.StatusConflict)
		return
	}

	// 合并请求覆盖值（TrustTier 从 catalog 继承，不允许请求覆盖）
	c := MCPServerConfig{
		Transport: entry.Transport,
		Command:   entry.Command,
		Args:      entry.Args,
		Env:       entry.Env,
		URL:       entry.URL,
		Timeout:   entry.Timeout,
		TrustTier: entry.TrustTier,
		Enabled:   true,
	}
	if req.Name != "" {
		c.Name = req.Name
	} else {
		c.Name = entry.Name
	}
	if len(req.Args) > 0 {
		c.Args = req.Args
	}
	if len(req.Env) > 0 {
		merged := make(map[string]string, len(c.Env)+len(req.Env))
		maps.Copy(merged, c.Env)
		maps.Copy(merged, req.Env)
		c.Env = merged
	}
	if req.URL != "" {
		c.URL = req.URL
	}
	if req.Timeout > 0 {
		c.Timeout = req.Timeout
	}
	c.ID = "mcp_" + hex.EncodeToString(b)

	argsBytes, _ := json.Marshal(c.Args)
	envBytes, _ := json.Marshal(c.Env)

	_, err := s.db.ExecContext(r.Context(),
		`INSERT INTO mcp_servers(id, name, transport, command, args, env, url, enabled, timeout, trust_tier, catalog_id, created_at, updated_at)
         VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Transport, c.Command, string(argsBytes), string(envBytes),
		c.URL, 1, c.Timeout, c.TrustTier, req.CatalogID, now, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.mcpMgr != nil {
		go s.startMCPServer(c)
	}

	c.CreatedAt, c.UpdatedAt = now, now
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"server":     c,
		"catalog_id": req.CatalogID,
	})
}

// handleUninstallPlugin 卸载插件（通过 catalog_id 定位，自动识别 mcp_servers / skill_sources）。
// DELETE /v1/plugins/{catalogID}
func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	catalogID := r.PathValue("catalogID")
	removed := false

	// 尝试从 mcp_servers 删除（Type=mcp）
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id FROM mcp_servers WHERE catalog_id=?`, catalogID)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if s.mcpMgr != nil {
				s.mcpMgr.Remove(id)
			}
			s.db.ExecContext(r.Context(), `DELETE FROM mcp_servers WHERE id=?`, id) //nolint:errcheck
			removed = true
		}
	}

	// 尝试从 skill_sources 删除（Type=skill/plugin）
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM skill_sources WHERE catalog_id=?`, catalogID)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			removed = true
		}
	}

	if !removed {
		http.Error(w, "plugin not installed", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "uninstalled"}) //nolint:errcheck
}

type Marketplace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Publisher   string `json:"publisher"`
	RepoURL     string `json:"repo_url"`
	Description string `json:"description"`
	IsBuiltin   int    `json:"is_builtin"`
	TrustTier   int    `json:"trust_tier"`
	Enabled     int    `json:"enabled"`
	CreatedAt   string `json:"created_at"`
}

var builtinMarketplaces = []Marketplace{
	{
		ID:          "openai/skills",
		Publisher:   "openai",
		Type:        "skill",
		TrustTier:   3,
		Name:        "OpenAI Skills Catalog",
		Description: "OpenAI 官方 Skills 目录（agentskills.io 开放标准）。包含 11 个 curated 技能：cloudflare-deploy、develop-web-game、doc、gh-fix-ci、imagegen、jupyter-notebook、linear、netlify-deploy、notion-knowledge-capture、pdf、spreadsheet。跨平台兼容 Polaris。",
		RepoURL:     "https://github.com/openai/skills",
		IsBuiltin:   1,
		Enabled:     1,
		CreatedAt:   "1970-01-01T00:00:00Z",
	},
	{
		ID:          "anthropic/skills",
		Publisher:   "anthropic",
		Type:        "skill",
		TrustTier:   3,
		Name:        "Anthropic Claude Skills",
		Description: "Anthropic 官方 17 个 Claude Skills（开源，Apache 2.0）。文档类：pdf/docx/xlsx/pptx；设计类：brand-guidelines/canvas-design/theme-factory；工程类：claude-api-helper/mcp-builder/webapp-testing；创意类：algorithmic-art/internal-comms/slack-gifs。",
		RepoURL:     "https://github.com/anthropics/skills",
		IsBuiltin:   1,
		Enabled:     1,
		CreatedAt:   "1970-01-01T00:00:00Z",
	},
	{
		ID:          "anthropic/claude-plugins-official",
		Publisher:   "anthropic",
		Type:        "plugin",
		TrustTier:   3,
		Name:        "Anthropic Official Plugin Directory",
		Description: "Anthropic 官方 Claude Code Plugin 目录（55+ 精选插件）。目录结构：/plugins（Anthropic 自研）+ /external_plugins（审核第三方：Supabase/Firebase/Discord/Telegram 等）。每个 Plugin 捆绑 Skills + MCP Server + Hooks。",
		RepoURL:     "https://github.com/anthropics/claude-plugins-official",
		IsBuiltin:   1,
		Enabled:     1,
		CreatedAt:   "1970-01-01T00:00:00Z",
	},
	{
		ID:          "google/skills",
		Publisher:   "google",
		Type:        "skill",
		TrustTier:   3,
		Name:        "Google Agent Skills",
		Description: "Google 官方 Cloud + Gemini Agent Skills 库（Google Cloud Next 2026 发布，Apache 2.0）。Cloud 技能：BigQuery、Cloud Run、Cloud SQL、AlloyDB、GKE、Firebase、Gemini API；架构技能：Security/Reliability/Cost-Optimization；Recipe：onboarding/authentication/network-observability。",
		RepoURL:     "https://github.com/google/skills",
		IsBuiltin:   1,
		Enabled:     1,
		CreatedAt:   "1970-01-01T00:00:00Z",
	},
}

// handleListMarketplaces 列表
func (s *Server) handleListMarketplaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), "SELECT id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at FROM plugin_marketplaces")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var mps []Marketplace
	for rows.Next() {
		var m Marketplace
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Publisher, &m.RepoURL, &m.Description, &m.IsBuiltin, &m.TrustTier, &m.Enabled, &m.CreatedAt); err == nil {
			mps = append(mps, m)
		}
	}
	// Append builtins
	mps = append(builtinMarketplaces, mps...)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"marketplaces": mps, "total": len(mps)})
}

// handleAddMarketplace 添加自定义市场
func (s *Server) handleAddMarketplace(w http.ResponseWriter, r *http.Request) {
	var req Marketplace
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	req.ID = "mp_" + hex.EncodeToString(b)
	req.IsBuiltin = 0
	req.TrustTier = 2 // Community
	req.Enabled = 1
	req.CreatedAt = now

	_, err := s.db.ExecContext(r.Context(),
		"INSERT INTO plugin_marketplaces(id, name, type, publisher, repo_url, description, is_builtin, trust_tier, enabled, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		req.ID, req.Name, req.Type, req.Publisher, req.RepoURL, req.Description, req.IsBuiltin, req.TrustTier, req.Enabled, req.CreatedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(req)
}

// handleDeleteMarketplace 删除市场
func (s *Server) handleDeleteMarketplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 只能删除非内置市场
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM plugin_marketplaces WHERE id=? AND is_builtin=0", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "marketplace not found or is builtin", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
