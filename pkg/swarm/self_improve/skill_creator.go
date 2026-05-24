package self_improve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// LLMClient is a minimal interface for the SkillCreator to generate responses.
type LLMClient interface {
	// Generate uses the system prompt and user intent to generate a structured response.
	Generate(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// SkillCreator defines the auto-generation workflow for skills based on Codex templates.
type SkillCreator struct {
	llm     LLMClient
	baseDir string // e.g. ~/.polaris-harness/plugins/user/
}

// NewSkillCreator initializes a new creator for auto-generating skills.
func NewSkillCreator(llm LLMClient, baseDir string) *SkillCreator {
	return &SkillCreator{
		llm:     llm,
		baseDir: baseDir,
	}
}

// GeneratedSkill represents the structured output expected from the LLM.
type GeneratedSkill struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

const skillCreatorSystemPrompt = `
You are the internal skill-creator agent. Your job is to translate a user's workflow description into a standard SKILL.md format.
A skill MUST have a concise name (kebab-case) and a clear description (what it does and when it should trigger) for progressive disclosure.

Output ONLY valid JSON matching this schema:
{
  "name": "skill-name",
  "description": "Trigger this skill when...",
  "instructions": "The detailed workflow steps..."
}
Do not include any Markdown wrappers like ` + "```json" + ` in the output.
`

// GenerateSkill takes a user's intent, calls the LLM, and creates the physical skill directory and SKILL.md.
func (c *SkillCreator) GenerateSkill(ctx context.Context, intent string) (string, error) {
	if c.llm == nil {
		return "", perrors.New(perrors.CodeInternal, "skill_creator: LLM client is nil")
	}

	response, err := c.llm.Generate(ctx, skillCreatorSystemPrompt, intent)
	if err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to generate skill", err)
	}

	// Simple JSON extraction to handle model quirks
	jsonStr := extractJSON(response)

	var result GeneratedSkill
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to parse generated skill JSON", err)
	}

	if result.Name == "" || result.Description == "" {
		return "", perrors.New(perrors.CodeInternal, "skill_creator: invalid generation, missing name or description")
	}

	// Create physical directory structure
	pluginDir := filepath.Join(c.baseDir, result.Name)
	skillsDir := filepath.Join(pluginDir, "skills", result.Name)

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to create skill directory", err)
	}

	// Write SKILL.md
	skillContent := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", result.Name, result.Description, result.Instructions)
	skillPath := filepath.Join(skillsDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to write SKILL.md", err)
	}

	// Create a default plugin.json
	pluginMetaDir := filepath.Join(pluginDir, ".codex-plugin")
	if err := os.MkdirAll(pluginMetaDir, 0755); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to create .codex-plugin directory", err)
	}

	pluginJSON := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "description": "%s",
  "skills": "./skills/"
}`, result.Name, result.Description)

	pluginJSONPath := filepath.Join(pluginMetaDir, "plugin.json")
	if err := os.WriteFile(pluginJSONPath, []byte(pluginJSON), 0644); err != nil {
		return "", perrors.Wrap(perrors.CodeInternal, "skill_creator: failed to write plugin.json", err)
	}

	return pluginDir, nil
}

func extractJSON(input string) string {
	re := regexp.MustCompile(`(?s)\{.*\}`)
	match := re.FindString(input)
	if match != "" {
		return match
	}
	return input
}
