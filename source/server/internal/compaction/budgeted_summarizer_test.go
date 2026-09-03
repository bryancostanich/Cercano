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

func TestSummarizeBudgetedLocal_SplitsOversizedTextBeforeProvider(t *testing.T) {
	calls := 0
	_, stats, err := SummarizeBudgetedLocal(context.Background(), []llm.Message{budgetTextMsg(llm.RoleUser, strings.Repeat("x", 20000))}, 2000, 256, func(ctx context.Context, prompt string, maxTokens int) (StructuredSummary, error) {
		calls++
		if budget := EstimateSummaryBudget(prompt, maxTokens, 2000); !budget.Fits {
			t.Fatalf("provider was called with over-budget split prompt: %+v", budget)
		}
		return StructuredSummary{Decisions: []string{fmt.Sprintf("part-%d", calls)}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || stats.Chunks != calls || !stats.Merged {
		t.Fatalf("expected oversized text to split and merge, calls=%d stats=%+v", calls, stats)
	}
}

func TestSummarizeBudgetedLocal_PreElidesHugeToolResults(t *testing.T) {
	huge := strings.Repeat("tool-output ", 2000)
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: huge}}}}

	calls := 0
	_, stats, err := SummarizeBudgetedLocal(context.Background(), msgs, 2000, 256, func(ctx context.Context, prompt string, maxTokens int) (StructuredSummary, error) {
		calls++
		if strings.Contains(prompt, huge) {
			t.Fatal("local summary prompt retained the oversized tool result")
		}
		if !strings.Contains(prompt, "[elided: tool result") {
			t.Fatalf("local summary prompt did not include an elision marker: %q", prompt)
		}
		if budget := EstimateSummaryBudget(prompt, maxTokens, 2000); !budget.Fits {
			t.Fatalf("provider was called with over-budget prompt after elision: %+v", budget)
		}
		return StructuredSummary{State: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if stats.ToolResultsElided != 1 {
		t.Fatalf("ToolResultsElided = %d, want 1", stats.ToolResultsElided)
	}
}
