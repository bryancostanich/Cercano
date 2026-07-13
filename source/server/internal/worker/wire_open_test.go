package worker

import (
	"encoding/json"
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestMarshalChatRequestRoundTrip(t *testing.T) {
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
		Temperature: 0.7,
	}
	p, err := MarshalChatRequest(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalChatRequest(p)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != req.Model || got.System != req.System || got.MaxTokens != req.MaxTokens || got.Temperature != req.Temperature {
		t.Errorf("scalar mismatch: %+v", got)
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
