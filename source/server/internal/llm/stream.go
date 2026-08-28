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
	// EventNotice is a non-fatal, user-facing status line injected in-band by
	// the resilience engine ("anthropic quota reached — switching to openai").
	// It is display-only: consumers surface it to the user and MUST NOT
	// persist it as message content.
	EventNotice StreamEventType = "notice"
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
	Err     error

	// Set on EventNotice: the user-facing status line. A dedicated field —
	// not TextDelta — so no consumer can mistake it for assistant content.
	Notice string
}

type StreamReader interface {
	Next() (StreamEvent, bool, error)
	Close() error
}
