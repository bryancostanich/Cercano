package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/session"
)

func TestCompactResponse_ChatNoFileChanges(t *testing.T) {
	resp := &Response{
		Output: "This is a chat response with no file changes.",
	}
	result := CompactResponse(resp)
	if result != "This is a chat response with no file changes." {
		t.Errorf("expected full text preserved, got %q", result)
	}
}

func TestCompactResponse_ChatTruncatesLongOutput(t *testing.T) {
	longText := strings.Repeat("a", 2500)
	resp := &Response{
		Output: longText,
	}
	result := CompactResponse(resp)
	if len(result) != MaxChatResponseLen+3 { // 500 + "..."
		t.Errorf("expected length %d, got %d", MaxChatResponseLen+3, len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("expected truncated result to end with '...', got %q", result[len(result)-10:])
	}
}

func TestCompactResponse_CodingWithFileChanges(t *testing.T) {
	resp := &Response{
		Output: "Here is the generated code for calculator.go...",
		FileChanges: []FileChange{
			{Path: "calculator.go", Action: "UPDATE"},
			{Path: "calculator_test.go", Action: "CREATE"},
		},
	}
	result := CompactResponse(resp)
	expected := "[Code generated: UPDATE calculator.go, CREATE calculator_test.go]"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestConversationStore_AppendAndLoadHistory(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)
	ctx := context.Background()

	err := cs.AppendTurn(ctx, "conv-123", "tell me about this class", "It has Add and Subtract methods.")
	if err != nil {
		t.Fatalf("AppendTurn failed: %v", err)
	}

	history, err := cs.LoadHistory(ctx, "conv-123")
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	if !strings.Contains(history, "tell me about this class") {
		t.Errorf("history should contain user message, got %q", history)
	}
	if !strings.Contains(history, "It has Add and Subtract methods.") {
		t.Errorf("history should contain assistant response, got %q", history)
	}
	if !strings.Contains(history, "--- Conversation History ---") {
		t.Errorf("history should have header, got %q", history)
	}
	if !strings.Contains(history, "--- End History ---") {
		t.Errorf("history should have footer, got %q", history)
	}
}

func TestConversationStore_DepthLimit(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 2) // Only keep last 2 turns
	ctx := context.Background()

	// Append 3 turns
	cs.AppendTurn(ctx, "conv-depth", "first question", "first answer")
	cs.AppendTurn(ctx, "conv-depth", "second question", "second answer")
	cs.AppendTurn(ctx, "conv-depth", "third question", "third answer")

	history, err := cs.LoadHistory(ctx, "conv-depth")
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}

	// Should contain the last 2 turns but not the first
	if strings.Contains(history, "first question") {
		t.Errorf("history should NOT contain first question (depth limit), got %q", history)
	}
	if !strings.Contains(history, "second question") {
		t.Errorf("history should contain second question, got %q", history)
	}
	if !strings.Contains(history, "third question") {
		t.Errorf("history should contain third question, got %q", history)
	}
}

func TestConversationStore_EmptyConversationID(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)
	ctx := context.Background()

	history, err := cs.LoadHistory(ctx, "")
	if err != nil {
		t.Fatalf("LoadHistory with empty ID should not error, got %v", err)
	}
	if history != "" {
		t.Errorf("expected empty string for empty conversation ID, got %q", history)
	}
}

func TestConversationStore_MultipleConversations(t *testing.T) {
	svc := session.InMemoryService()
	cs := NewConversationStore(svc, 3)
	ctx := context.Background()

	cs.AppendTurn(ctx, "conv-A", "question A", "answer A")
	cs.AppendTurn(ctx, "conv-B", "question B", "answer B")

	historyA, _ := cs.LoadHistory(ctx, "conv-A")
	historyB, _ := cs.LoadHistory(ctx, "conv-B")

	if !strings.Contains(historyA, "question A") {
		t.Errorf("conv-A history should contain question A")
	}
	if strings.Contains(historyA, "question B") {
		t.Errorf("conv-A history should NOT contain question B")
	}

	if !strings.Contains(historyB, "question B") {
		t.Errorf("conv-B history should contain question B")
	}
	if strings.Contains(historyB, "answer A") {
		t.Errorf("conv-B history should NOT contain answer A")
	}
}
