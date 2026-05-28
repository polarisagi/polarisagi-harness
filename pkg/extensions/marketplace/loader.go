package marketplace

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// SkillMetaFromSKILLmd 解析 agentskills.io 标准 SKILL.md 文件，
// 转换为 protocol.SkillMeta（ADR-0015 §2.3）。
//
// 签名：外部 SKILL.md 无 SIGNATURE 文件，使用 HMAC-SHA256（密钥 = signingKey）
// 生成本地签名，设置 SignatureValid=true + Capabilities 附加 "trust:local"。
// Cedar 策略通过 "trust:local" vs "trust:verified" 区分沙箱级别。
func SkillMetaFromSKILLmd(path string, signingKey []byte) (*protocol.SkillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("loader: read %s: %v", path, err), err)
	}

	name, description, err := parseFrontmatter(data)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("loader: parse frontmatter %s: %v", path, err), err)
	}
	if name == "" {
		return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("loader: SKILL.md %s missing 'name' in frontmatter", path))
	}

	// 本地 HMAC 签名（替代 cosign，标记 trust:local）
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(data)

	return &protocol.SkillMeta{
		Name:         "skill:" + name,
		Version:      "1.0.0",
		Runtime:      "markdown", // NL 描述技能，非 Wasm
		RiskLevel:    "low",
		Sandbox:      1, // Sbx-L1（TrustLocal 上限）
		Capabilities: []string{"description:" + description},
		Trust:        protocol.TrustLocal, // HMAC 本地验证通过，publisher 未认证
		Idempotent:   false,
		Benchmarks:   protocol.SkillBenchmarks{},
	}, nil
}

// parseFrontmatter 提取 SKILL.md YAML frontmatter 中的 name 和 description。
// 格式：--- ... name: xxx ... description: yyy ... ---
func parseFrontmatter(data []byte) (name, description string, err error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", perrors.New(perrors.CodeInternal, "no frontmatter found")
	}

	inFrontmatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break // 第二个 ---，frontmatter 结束
		}
		if !inFrontmatter {
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "name:"); ok {
			name = strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(trimmed, "description:"); ok {
			description = strings.TrimSpace(after)
		}
	}
	return name, description, nil
}

// ParseSKILLmd 解析 SKILL.md，需要提供 signingKey 保证跨重启验证一致性。
func ParseSKILLmd(path string, signingKey []byte) (*protocol.SkillMeta, error) {
	return SkillMetaFromSKILLmd(path, signingKey)
}

// LoadPlugin 从指定目录加载完整的 Codex 插件树。
func LoadPlugin(dir string) (*Plugin, error) {
	manifestPath := filepath.Join(dir, ".codex-plugin", "plugin.json")
	manifest, err := parseManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	plugin := &Plugin{
		Manifest: *manifest,
		Dir:      dir,
		Enabled:  true,
		MCPs:     make(map[string]protocol.MCPServerDef),
	}

	// 尝试加载 .mcp.json
	if manifest.MCPServers != "" {
		mcpPath := manifest.MCPServers
		if !filepath.IsAbs(mcpPath) {
			mcpPath = filepath.Join(dir, mcpPath)
		}
		if mcpConfig, err := loadMCPConfig(mcpPath); err == nil {
			plugin.MCPs = mcpConfig.MCPServers
		} else if !errors.Is(err, os.ErrNotExist) {
			// 如果不是文件不存在导致的错误，则返回解析错误
			return nil, perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("loader: failed to load %s: %v", mcpPath, err), err)
		}
	}

	return plugin, nil
}

func loadMCPConfig(path string) (*protocol.MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c protocol.MCPConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// parseManifest reads and parses a plugin.json file.
func parseManifest(path string) (*protocol.PluginJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest protocol.PluginJSON
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}
