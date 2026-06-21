package llm

import "encoding/json"

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Block struct {
	Type BlockType `json:"type"`

	Text string `json:"text,omitempty"`

	ToolUseID string          `json:"id,omitempty"`
	ToolName  string          `json:"name,omitempty"`
	ToolInput json.RawMessage `json:"input,omitempty"`

	ToolUseRef string `json:"tool_use_id,omitempty"`
	Content    string `json:"content,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`

	ProviderExtras map[string]any `json:"-"`
}

type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"content"`
}
