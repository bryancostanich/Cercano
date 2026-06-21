package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/conversation"
)

// Smallest viable Agent harness — we exercise the persistence path through
// the exported wrappers (ListConversations / ResumeConversation / Delete /
// Rename) plus the package-private storeConversationTurn.

func newTestAgentWithPersistent(t *testing.T) (*Agent, conversation.Store) {
	t.Helper()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	a := &Agent{persistent: store}
	t.Cleanup(func() { _ = store.Close() })
	return a, store
}

func TestAgent_storeConversationTurn_WritesUserAndAssistant(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()

	a.storeConversationTurn(ctx, "conv-1", "what's the capital of France?", &Response{
		Output:       "Paris.",
		InputTokens:  12,
		OutputTokens: 4,
	})

	turns, err := store.GetTurns(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[0].Content != "what's the capital of France?" {
		t.Errorf("user turn wrong: %+v", turns[0])
	}
	if turns[0].TokensIn != 12 {
		t.Errorf("user tokens_in: want 12 got %d", turns[0].TokensIn)
	}
	if turns[1].Role != "assistant" || turns[1].Content != "Paris." {
		t.Errorf("assistant turn wrong: %+v", turns[1])
	}
	if turns[1].TokensOut != 4 {
		t.Errorf("assistant tokens_out: want 4 got %d", turns[1].TokensOut)
	}
}

func TestAgent_storeConversationTurn_NoConvID_NoOp(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()
	a.storeConversationTurn(ctx, "", "hello", &Response{Output: "world"})

	infos, _ := store.List(ctx, "", 0)
	if len(infos) != 0 {
		t.Errorf("empty convID should not persist anything, got %d conversations", len(infos))
	}
}

func TestAgent_storeConversationTurn_NoPersistentStore_NoPanic(t *testing.T) {
	// No persistent store + no in-memory store. Should not panic; just no-op.
	a := &Agent{}
	a.storeConversationTurn(context.Background(), "conv-1", "hello", &Response{Output: "world"})
}

func TestAgent_RenameConversation_DelegatesToStore(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()

	_ = store.EnsureConversation(ctx, "conv-1", "", "")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "conv-1", Role: "user", Content: "hello"})

	if err := a.RenameConversation(ctx, "conv-1", "my title"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	infos, _ := store.List(ctx, "", 0)
	if len(infos) != 1 || infos[0].Title != "my title" {
		t.Errorf("rename didn't land: %+v", infos)
	}
}

func TestAgent_RenameConversation_NoPersistent_NoOp(t *testing.T) {
	a := &Agent{}
	// Should silently no-op when no persistent store is attached.
	if err := a.RenameConversation(context.Background(), "anything", "new title"); err != nil {
		t.Errorf("expected no-op, got %v", err)
	}
}

func TestAgent_ListConversations_PassesThroughFilter(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()

	_ = store.EnsureConversation(ctx, "a", "/proj1", "")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "a", Role: "user", Content: "x"})
	_ = store.EnsureConversation(ctx, "b", "/proj2", "")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "b", Role: "user", Content: "y"})

	infos, err := a.ListConversations(ctx, "/proj1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != "a" {
		t.Errorf("filter not applied: %+v", infos)
	}
}

func TestAgent_DeleteConversation_RemovesAll(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "a", "", "")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "a", Role: "user", Content: "x"})

	if err := a.DeleteConversation(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	infos, _ := store.List(ctx, "", 0)
	if len(infos) != 0 {
		t.Errorf("delete didn't remove conv: %+v", infos)
	}
}

func TestAgent_ResumeConversation_ReturnsTurns(t *testing.T) {
	a, store := newTestAgentWithPersistent(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "a", "", "")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "a", Role: "user", Content: "hello"})
	_ = store.Append(ctx, conversation.Turn{ConversationID: "a", Role: "assistant", Content: "hi"})

	turns, err := a.ResumeConversation(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Errorf("ordering wrong: %+v", turns)
	}
}
