package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/adk/session"

	"cercano/source/server/internal/engine"
)

func TestStore_AppendAndLoad(t *testing.T) {
	svc := session.InMemoryService()
	s := NewStore(svc, 50)

	history := []engine.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello!"},
	}
	if err := s.Save(context.Background(), "conv1", history); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background(), "conv1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[1].Content != "hello!" {
		t.Errorf("got %+v", got[1])
	}
}

func TestStore_EmptyIDReturnsEmpty(t *testing.T) {
	s := NewStore(session.InMemoryService(), 50)
	got, err := s.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for empty conversationID, got %+v", got)
	}
}

func TestStore_RoundTripsToolTurn(t *testing.T) {
	s := NewStore(session.InMemoryService(), 50)
	history := []engine.ChatMessage{
		{Role: "user", Content: "read /x"},
		{Role: "assistant", ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "read_file", Arguments: json.RawMessage(`{"path":"/x"}`)}},
		}},
		{Role: "tool", ToolCallID: "tc_1", Name: "read_file", Content: "<contents>"},
		{Role: "assistant", Content: "the file says <contents>"},
	}
	if err := s.Save(context.Background(), "conv2", history); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background(), "conv2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4", len(got))
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("tool call not preserved: %+v", got[1])
	}
	if got[2].ToolCallID != "tc_1" {
		t.Errorf("tool result not preserved: %+v", got[2])
	}
}
