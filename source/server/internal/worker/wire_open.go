package worker

// Codecs for the open-inference proxy: convert between the llm inference types
// and the proto envelope (OpenInferenceRequest / OpenInferenceEvent) used to
// proxy an open-model call from the worker to the host. Messages reuse the
// existing MarshalMessage/UnmarshalMessage (JSON blocks), so round-trip
// fidelity matches the persistence layer.

import (
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
	proto "cercano/source/server/pkg/proto"
)

// MarshalChatRequest converts an llm.ChatRequest to its proto wire form.
func MarshalChatRequest(req llm.ChatRequest) (*proto.LLMChatRequest, error) {
	msgs := make([]*proto.LLMMessage, 0, len(req.Messages))
	for i, m := range req.Messages {
		pm, err := MarshalMessage(m)
		if err != nil {
			return nil, fmt.Errorf("worker/wire: marshal chat message %d: %w", i, err)
		}
		msgs = append(msgs, pm)
	}
	tools := make([]*proto.LLMTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, MarshalTool(t))
	}
	return &proto.LLMChatRequest{
		Model:          req.Model,
		System:         req.System,
		Messages:       msgs,
		Tools:          tools,
		ToolChoiceType: string(req.ToolChoice.Type),
		ToolChoiceName: req.ToolChoice.Name,
		MaxTokens:      int32(req.MaxTokens),
		Temperature:    req.Temperature,
	}, nil
}

// UnmarshalChatRequest converts a proto LLMChatRequest back to llm.ChatRequest.
func UnmarshalChatRequest(p *proto.LLMChatRequest) (llm.ChatRequest, error) {
	if p == nil {
		return llm.ChatRequest{}, fmt.Errorf("worker/wire: nil LLMChatRequest")
	}
	msgs := make([]llm.Message, 0, len(p.GetMessages()))
	for i, pm := range p.GetMessages() {
		m, err := UnmarshalMessage(pm)
		if err != nil {
			return llm.ChatRequest{}, fmt.Errorf("worker/wire: unmarshal chat message %d: %w", i, err)
		}
		msgs = append(msgs, m)
	}
	tools := make([]llm.Tool, 0, len(p.GetTools()))
	for _, pt := range p.GetTools() {
		tools = append(tools, UnmarshalTool(pt))
	}
	return llm.ChatRequest{
		Model:    p.GetModel(),
		System:   p.GetSystem(),
		Messages: msgs,
		Tools:    tools,
		ToolChoice: llm.ToolChoice{
			Type: llm.ToolChoiceType(p.GetToolChoiceType()),
			Name: p.GetToolChoiceName(),
		},
		MaxTokens:   int(p.GetMaxTokens()),
		Temperature: p.Temperature, // optional double → pointer round-trips presence
	}, nil
}

// MarshalTool converts an llm.Tool to proto. Only the model-facing fields cross
// the wire; llm.Tool.Permission is an agent-side concern.
func MarshalTool(t llm.Tool) *proto.LLMTool {
	return &proto.LLMTool{
		Name:        t.Name,
		Description: t.Description,
		Schema:      []byte(t.Schema),
	}
}

// UnmarshalTool converts a proto LLMTool back to llm.Tool.
func UnmarshalTool(p *proto.LLMTool) llm.Tool {
	if p == nil {
		return llm.Tool{}
	}
	var schema json.RawMessage
	if s := p.GetSchema(); len(s) > 0 {
		schema = json.RawMessage(s)
	}
	return llm.Tool{
		Name:        p.GetName(),
		Description: p.GetDescription(),
		Schema:      schema,
	}
}

// MarshalStreamEvent converts an llm.StreamEvent to its proto wire form.
func MarshalStreamEvent(e llm.StreamEvent) *proto.LLMStreamEvent {
	return &proto.LLMStreamEvent{
		Type:          string(e.Type),
		TextDelta:     e.TextDelta,
		ToolUseId:     e.ToolUseID,
		ToolName:      e.ToolName,
		ToolInputRaw:  []byte(e.ToolInputRaw),
		ReasoningId:   e.ReasoningID,
		ReasoningData: e.ReasoningData,
		StopReason:    e.StopReason,
		InputTokens:   int64(e.InputTokens),
		OutputTokens:  int64(e.OutputTokens),
		ErrText:       e.ErrText,
	}
}

// UnmarshalStreamEvent converts a proto LLMStreamEvent back to llm.StreamEvent.
func UnmarshalStreamEvent(p *proto.LLMStreamEvent) llm.StreamEvent {
	if p == nil {
		return llm.StreamEvent{}
	}
	var raw json.RawMessage
	if r := p.GetToolInputRaw(); len(r) > 0 {
		raw = json.RawMessage(r)
	}
	return llm.StreamEvent{
		Type:          llm.StreamEventType(p.GetType()),
		TextDelta:     p.GetTextDelta(),
		ToolUseID:     p.GetToolUseId(),
		ToolName:      p.GetToolName(),
		ToolInputRaw:  raw,
		ReasoningID:   p.GetReasoningId(),
		ReasoningData: p.GetReasoningData(),
		StopReason:    p.GetStopReason(),
		InputTokens:   int(p.GetInputTokens()),
		OutputTokens:  int(p.GetOutputTokens()),
		ErrText:       p.GetErrText(),
	}
}
