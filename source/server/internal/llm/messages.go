package llm

import "encoding/json"

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockImage      BlockType = "image"
	BlockReasoning  BlockType = "reasoning"
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
	// StartLine is client display metadata on tool_result blocks: the 1-based
	// line where a file edit/write began, recorded at execute time so diffs
	// rendered from the persisted args can be numbered after resume. Never
	// sent to providers — adapters translate field-by-field and skip it.
	// 0 = not applicable.
	StartLine int `json:"start_line,omitempty"`

	MediaType string `json:"media_type,omitempty"` // image: "image/png" etc (required for base64)
	ImageData string `json:"image_data,omitempty"` // image: base64 bytes
	ImageURL  string `json:"image_url,omitempty"`  // image: http(s) URL

	// reasoning: opaque encrypted reasoning carried across turns (Responses API).
	// We never read ReasoningData — it is stored and sent back verbatim.
	ReasoningID   string `json:"reasoning_id,omitempty"`
	ReasoningData string `json:"reasoning_data,omitempty"`

	ProviderExtras map[string]any `json:"-"`
}

type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"content"`
}

// MalformedToolInputKey is the single key of the envelope that wraps
// structurally invalid streamed tool input. Providers occasionally deliver
// tool_use input that is not valid JSON (truncated mid-stream, or a proxy
// re-serialization bug). Kept raw in ToolInput, those bytes poison everything
// downstream: the turn cannot persist (RawMessage refuses to marshal) and
// replaying the block corrupts every later request body. The stream collector
// wraps such input as {"_malformed_tool_input": "<raw text>"} so history
// stays marshalable and the raw attempt survives for the model to see.
const MalformedToolInputKey = "_malformed_tool_input"

// MalformedToolInput reports whether input is the malformed-input envelope,
// returning the original raw text when it is.
func MalformedToolInput(input json.RawMessage) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	var env map[string]string
	if json.Unmarshal(input, &env) != nil {
		return "", false
	}
	raw, ok := env[MalformedToolInputKey]
	return raw, ok && len(env) == 1
}
