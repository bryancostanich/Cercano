package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestSummarizeBudgetedLocal_ChunksAndMergesWithoutCloud(t *testing.T) {
	msgs := []llm.Message{}
	for i := 0; i < 4; i++ {
		msgs = append(msgs, budgetTextMsg(llm.RoleUser, fmt.Sprintf("msg-%d %s", i, strings.Repeat("x", 1800))))
	}
	calls := 0
	got, stats, err := SummarizeBudgetedLocal(context.Background(), msgs, 2500, 256, func(ctx context.Context, prompt string, maxTokens int) (StructuredSummary, error) {
		calls++
		if maxTokens != 256 {
			t.Fatalf("maxTokens = %d, want 256", maxTokens)
		}
		if budget := EstimateSummaryBudget(prompt, maxTokens, 2500); !budget.Fits {
			t.Fatalf("provider was called with over-budget prompt: %+v", budget)
		}
		return StructuredSummary{Goal: "goal", Decisions: []string{fmt.Sprintf("chunk-%d", calls)}, Files: map[string]string{fmt.Sprintf("f%d", calls): "seen"}, State: fmt.Sprintf("state-%d", calls)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || stats.Chunks != calls || !stats.Merged {
		t.Fatalf("expected multiple chunk calls and deterministic merge, calls=%d stats=%+v", calls, stats)
	}
	if len(got.Decisions) != calls || got.State != fmt.Sprintf("state-%d", calls) {
		t.Fatalf("merged summary did not preserve chunk outputs: %+v", got)
	}
}

func TestSummarizeBudgetedLocal_MinimalOverflowDefersBeforeProvider(t *testing.T) {
	called := false
	_, _, err := SummarizeBudgetedLocal(context.Background(), []llm.Message{budgetTextMsg(llm.RoleUser, strings.Repeat("x", 20000))}, 2000, 256, func(ctx context.Context, prompt string, maxTokens int) (StructuredSummary, error) {
		called = true
		return StructuredSummary{}, nil
	})
	if called {
		t.Fatal("minimal overflow must defer before calling provider")
	}
	if _, ok := err.(*DeferralError); !ok {
		t.Fatalf("expected DeferralError, got %T %v", err, err)
	}
}
