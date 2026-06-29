package llm

import "encoding/json"

type StreamEventType string

const (
	EventMessageStart      StreamEventType = "message_start"
	EventTextDelta         StreamEventType = "text_delta"
	EventToolUseStart      StreamEventType = "tool_use_start"
	EventToolUseInputDelta StreamEventType = "tool_use_input_delta"
	EventToolUseStop       StreamEventType = "tool_use_stop"
	EventReasoning         StreamEventType = "reasoning"
	EventMessageStop       StreamEventType = "message_stop"
	EventError             StreamEventType = "error"
)

type StreamEvent struct {
	Type StreamEventType

	TextDelta string

	ToolUseID    string
	ToolName     string
	ToolInputRaw json.RawMessage

	// Set on EventReasoning: the opaque encrypted reasoning item to carry forward.
	ReasoningID   string
	ReasoningData string

	StopReason string

	// Provider-reported token usage. InputTokens is set on EventMessageStart
	// (full prompt: system + tools + history); OutputTokens on EventMessageStop.
	InputTokens  int
	OutputTokens int

	ErrText string
}

type StreamReader interface {
	Next() (StreamEvent, bool, error)
	Close() error
}
