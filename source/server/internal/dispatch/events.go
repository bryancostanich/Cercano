// Package dispatch implements the cercano_dispatch agentic tool-use loop.
package dispatch

import "encoding/json"

// EventKind identifies the type of event emitted by Loop.Run.
type EventKind int

const (
	EventTextChunk EventKind = iota
	EventToolCall
	EventToolResult
	EventDone
)

func (k EventKind) String() string {
	switch k {
	case EventTextChunk:
		return "text_chunk"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventDone:
		return "done"
	default:
		return "unknown"
	}
}

// MarshalJSON renders EventKind as its string form for downstream consumers.
func (k EventKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// Event is the sum-type emitted by Loop.Run.
// Only the fields relevant to Kind are populated; consumers should switch on Kind.
type Event struct {
	Kind EventKind `json:"kind"`

	// EventTextChunk
	Text string `json:"text,omitempty"`

	// EventToolCall and EventToolResult
	ToolCallID string `json:"tool_call_id,omitempty"`

	// EventToolCall
	ToolName string          `json:"tool_name,omitempty"`
	ToolArgs json.RawMessage `json:"tool_args,omitempty"`

	// EventToolResult
	ToolResult string `json:"tool_result,omitempty"`
	ToolOK     bool   `json:"tool_ok,omitempty"`

	// EventDone
	DoneError string `json:"done_error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
