package worker

import (
	"encoding/json"
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestMarshalChatRequestRoundTrip(t *testing.T) {
	temp := 0.7
	req := llm.ChatRequest{
		Model:  "qwen3",
		System: "be helpful",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
			{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}},
		},
		Tools: []llm.Tool{
			{Name: "read", Description: "read a file", Schema: json.RawMessage(`{"type":"object"}`)},
		},
		ToolChoice:  llm.ToolChoice{Type: llm.ToolChoiceTool, Name: "read"},
		MaxTokens:   256,
		Temperature: &temp,
	}
	p, err := MarshalChatRequest(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalChatRequest(p)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != req.Model || got.System != req.System || got.MaxTokens != req.MaxTokens {
		t.Errorf("scalar mismatch: %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != temp {
		t.Errorf("temperature: got %v want %v", got.Temperature, temp)
	}

	// Presence must round-trip: explicit 0 (greedy) stays present, unset
	// stays nil — the wire may not collapse the two.
	zero := 0.0
	if p, err := MarshalChatRequest(llm.ChatRequest{Model: "m", Temperature: &zero}); err != nil {
		t.Fatalf("marshal greedy: %v", err)
	} else if got, err := UnmarshalChatRequest(p); err != nil || got.Temperature == nil || *got.Temperature != 0 {
		t.Errorf("greedy 0 must survive the wire: got %v err %v", got.Temperature, err)
	}
	if p, err := MarshalChatRequest(llm.ChatRequest{Model: "m"}); err != nil {
		t.Fatalf("marshal unset: %v", err)
	} else if got, err := UnmarshalChatRequest(p); err != nil || got.Temperature != nil {
		t.Errorf("unset temperature must stay nil: got %v err %v", got.Temperature, err)
	}
	if got.ToolChoice != req.ToolChoice {
		t.Errorf("tool choice: got %+v want %+v", got.ToolChoice, req.ToolChoice)
	}
	if len(got.Messages) != 2 || got.Messages[0].Blocks[0].Text != "hi" || got.Messages[1].Blocks[0].Text != "hello" {
		t.Errorf("messages: %+v", got.Messages)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "read" || string(got.Tools[0].Schema) != `{"type":"object"}` {
		t.Errorf("tools: %+v", got.Tools)
	}
}

func TestMarshalStreamEventRoundTrip(t *testing.T) {
	e := llm.StreamEvent{
		Type:         llm.EventToolUseStart,
		ToolUseID:    "tu_1",
		ToolName:     "read",
		ToolInputRaw: json.RawMessage(`{"path":"x"}`),
		StopReason:   "tool_use",
		InputTokens:  10,
		OutputTokens: 20,
	}
	got := UnmarshalStreamEvent(MarshalStreamEvent(e))
	if !reflect.DeepEqual(got, e) {
		t.Errorf("stream event round-trip:\n got %+v\nwant %+v", got, e)
	}
}
