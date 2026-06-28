package llm

import "encoding/json"

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockImage      BlockType = "image"
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

	MediaType string `json:"media_type,omitempty"` // image: "image/png" etc (required for base64)
	ImageData string `json:"image_data,omitempty"` // image: base64 bytes
	ImageURL  string `json:"image_url,omitempty"`  // image: http(s) URL

	ProviderExtras map[string]any `json:"-"`
}

type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"content"`
}
