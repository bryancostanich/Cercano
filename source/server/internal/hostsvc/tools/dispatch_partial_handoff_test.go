package tools

import (
	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDispatchBudgetErrorRetainsPartialHandoff(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureConversation(t.Context(), "parent", "", "model"); err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil, func() conversation.Store { return store }, nil)
	svc.SetRegistry(historyProbeRegistry())
	installTestFailureLog(t, svc)
	svc.SetLoopCompactorFactory(func() agent.LoopCompactor {
		return agent.LoopCompactorFunc(func(_ context.Context, h []llm.Message) ([]llm.Message, int, error) { return h, 5, nil })
	})
	p := &historyProbeProvider{name: "cloud"}
	// The original task is never compacted. Exhaust on the first execution-history
	// pass, after one completed tool call and before the second model request.
	result, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Task: "Read configuration", ConversationID: "parent", Tools: []string{"Read", "Grep"}, MaxIterations: 5, TokenBudget: 5}, inference.Selection{Provider: p, IsCloud: true}, "model")
	var classified *llm.Error
	if !errors.As(err, &classified) || classified.Class != llm.ErrTokenBudgetExhausted {
		t.Fatalf("lost classification: %v", err)
	}
	if result.SubConversationID == "" || !strings.Contains(err.Error(), result.SubConversationID) {
		t.Fatal("missing durable transcript reference")
	}
	for _, text := range []string{result.Text, err.Error()} {
		if !strings.Contains(text, "Partial work") || !strings.Contains(text, "1 completed") || !strings.Contains(text, "task NOT completed") {
			t.Fatal(text)
		}
	}
	if len(p.requests) != 1 {
		t.Fatal("model called after compaction exhausted budget")
	}
}
