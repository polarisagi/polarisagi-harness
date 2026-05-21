package kernel

import (
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate"
)

// PromptBuilder 是系统内唯一合法的 LLM Prompt 组装构造器。
// 它通过 Go 语言类型系统强制实现指令数据隔离（M11 §3 规定）。
type PromptBuilder struct {
	messages []protocol.Message
}

// NewPromptBuilder 创建一个新的 Prompt 构造器。
func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{
		messages: make([]protocol.Message, 0),
	}
}

// WriteInstruction 将已经证实为安全的指令写入 System 角色。
// 由于参数被强制要求为 substrate.SafeString，只有 TaintNone 或被彻底清洗过的内容才能进入此处。
func (b *PromptBuilder) WriteInstruction(safe substrate.SafeString) {
	b.messages = append(b.messages, protocol.Message{
		Role:    "system",
		Content: safe.Content(),
	})
}

// WriteUserData 将不受信的外部输入写入 User 角色，并强制进行 Spotlighting 围栏保护。
// 这可以防止 LLM 将恶意用户文本解析为隐藏的控制指令（Prompt Injection）。
func (b *PromptBuilder) WriteUserData(ts substrate.TaintedString) {
	b.messages = append(b.messages, protocol.Message{
		Role:    "user",
		Content: substrate.Spotlighting(ts),
	})
}

// WriteUserInstruction 允许将 SafeString 写入 User 角色。
// 用于某些特定场景下需要由 User 发起但内容确认为系统硬编码的安全指令。
func (b *PromptBuilder) WriteUserInstruction(safe substrate.SafeString) {
	b.messages = append(b.messages, protocol.Message{
		Role:    "user",
		Content: safe.Content(),
	})
}

// Build 输出最终组装完毕可用于 InferRequest 的消息序列。
func (b *PromptBuilder) Build() []protocol.Message {
	return b.messages
}

// WriteComputerUsePolicy 写入电脑操控权限的系统指令。
func (b *PromptBuilder) WriteComputerUsePolicy(mode string, anyAppEnabled, chromeEnabled bool) {
	if mode == "" {
		mode = "auto_review"
	}

	policy := `Computer Use Confirmations Policy:
You have access to computer control tools. Here are the rules for using them based on the current user configuration:
- Mode: ` + mode + `
- anyAppEnabled: `
	if anyAppEnabled {
		policy += "true"
	} else {
		policy += "false"
	}
	policy += "\n- chromeEnabled: "
	if chromeEnabled {
		policy += "true"
	} else {
		policy += "false"
	}

	policy += "\n\n"
	if mode == "default" {
		policy += "You MUST ask for user confirmation before performing any action that interacts with the computer or browser."
	} else if mode == "auto_review" {
		policy += "You may perform safe actions (read, scroll, search) without asking. You MUST ask for user confirmation before performing any dangerous action (write, delete, purchase, login)."
	} else if mode == "full_access" {
		policy += "You have full access to the computer and browser. You do not need to ask for user confirmation before performing any action."
	}

	if !anyAppEnabled {
		policy += "\nYou are NOT allowed to interact with any application other than the explicitly enabled ones."
	}
	if chromeEnabled {
		policy += "\nYou are allowed to control Google Chrome via the browser_use tool."
	}

	b.messages = append(b.messages, protocol.Message{
		Role:    "system",
		Content: policy,
	})
}
