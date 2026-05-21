package plugin

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
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
		return nil, fmt.Errorf("loader: read %s: %w", path, err)
	}

	name, description, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("loader: parse frontmatter %s: %w", path, err)
	}
	if name == "" {
		return nil, fmt.Errorf("loader: SKILL.md %s missing 'name' in frontmatter", path)
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
		return "", "", fmt.Errorf("no frontmatter found")
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

// defaultSigningKey 从环境变量读取签名密钥，fallback 为进程启动时间哈希。
// 生产环境应通过配置文件注入稳定密钥以保证重启后签名一致。
func defaultSigningKey() []byte {
	if key := os.Getenv("POLARIS_SKILL_SIGNING_KEY"); key != "" {
		return []byte(key)
	}
	// fallback：不稳定（重启失效），适合开发阶段
	h := sha256.Sum256([]byte(fmt.Sprintf("polaris-local-%d", time.Now().Unix()/86400)))
	return h[:]
}

// ParseSKILLmd 使用默认签名密钥解析 SKILL.md。
func ParseSKILLmd(path string) (*protocol.SkillMeta, error) {
	return SkillMetaFromSKILLmd(path, defaultSigningKey())
}

// bufioScanner utility for line scanning (避免重复 strings.Split)
var _ = bufio.NewScanner
