package agent

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/llm"
)

func compactionHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "one"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "two"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "three"}}},
	}
}

// Compaction is an optimization: every failure mode must leave the loop's
// working history intact rather than degrade or destroy the dispatch.
func TestCompactLoopHistoryIsDefensive(t *testing.T) {
	hist := compactionHistory()
	for _, tc := range []struct {
		name string
		c    LoopCompactor
	}{
		{"nil compactor", nil},
		{"error", LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
			return nil, 0, errors.New("boom")
		})},
		{"empty result", LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
			return []llm.Message{}, 0, nil
		})},
		{"nil result no error", LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
			return nil, 0, nil
		})},
		{"partial result then error", LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
			return []llm.Message{{Role: llm.RoleUser}}, 0, errors.New("late failure")
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := TokenBudget{Limit: 1000}
			out := compactLoopHistory(context.Background(), tc.c, hist, &budget)
			if len(out) != len(hist) {
				t.Fatalf("history not preserved: got %d want %d", len(out), len(hist))
			}
			for i := range out {
				if out[i].Blocks[0].Text != hist[i].Blocks[0].Text {
					t.Fatalf("message %d altered: %+v", i, out[i])
				}
			}
		})
	}
	// Empty input is safe regardless of compactor.
	called := false
	out := compactLoopHistory(context.Background(), LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
		called = true
		return nil, 0, nil
	}), nil, nil)
	if called || len(out) != 0 {
		t.Fatal("empty history should short-circuit")
	}
}

// Summarizer spend must count against the dispatch budget even when the pass
// fails: the tokens were billed either way, and hiding them would let
// compaction quietly erode the budget guarantee.
func TestCompactLoopHistoryChargesSpend(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"failure", errors.New("summarizer down")}} {
		t.Run(tc.name, func(t *testing.T) {
			budget := TokenBudget{Limit: 10_000}
			compactor := LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
				if tc.err != nil {
					return nil, 2_500, tc.err
				}
				return compactionHistory()[:2], 2_500, nil
			})
			compactLoopHistory(context.Background(), compactor, compactionHistory(), &budget)
			if budget.Spent != 2_500 {
				t.Fatalf("spend not charged: %d", budget.Spent)
			}
		})
	}
	// Enough compaction spend must be able to exhaust the budget, so a
	// pathological summarizer cannot grind indefinitely.
	budget := TokenBudget{Limit: 1_000}
	compactLoopHistory(context.Background(), LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
		return compactionHistory(), 5_000, nil
	}), compactionHistory(), &budget)
	if !budget.Exhausted() {
		t.Fatalf("compaction spend cannot exhaust budget: %+v", budget)
	}
}

// A compacted result must be pairing-repaired: an orphaned tool_use without
// its tool_result would corrupt the remainder of the dispatch.
func TestCompactLoopHistoryRepairsPairing(t *testing.T) {
	orphaned := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "go"}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockToolUse, ToolUseID: "call-1", ToolName: "Read", ToolInput: []byte(`{}`)},
		}},
		// tool_result deliberately missing.
	}
	out := compactLoopHistory(context.Background(), LoopCompactorFunc(func(context.Context, []llm.Message) ([]llm.Message, int, error) {
		return orphaned, 0, nil
	}), compactionHistory(), nil)
	for _, m := range out {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				t.Fatalf("unpaired tool_use survived compaction: %+v", out)
			}
		}
	}
}

// End-to-end: a loop configured with a compactor must actually call it
// between iterations, and the compacted history must be what gets sent.
func TestToolLoopInvokesCompactor(t *testing.T) {
	executed := 0
	passes := 0
	var sentSizes []int
	provider := &budgetProbeProvider{bill: 100}
	compactor := LoopCompactorFunc(func(ctx context.Context, hist []llm.Message) ([]llm.Message, int, error) {
		passes++
		sentSizes = append(sentSizes, len(hist))
		// Drop nothing on the first pass; from the second on, collapse the
		// middle so the test can observe a real reduction taking effect.
		if len(hist) > 3 {
			return append(append([]llm.Message{}, hist[0]), hist[len(hist)-2:]...), 50, nil
		}
		return hist, 0, nil
	})
	_, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider:      provider,
		Model:         "m",
		System:        "probe",
		UserInput:     "go",
		Registry:      probeRegistry(t, &executed),
		Permissions:   NewStaticPermissionStore(ModeBypass),
		MaxIterations: 5,
		LoopCompactor: compactor,
	})
	if err != nil {
		var le *llm.Error
		if errors.As(err, &le) && le.Class == llm.ErrTokenBudgetExhausted {
			t.Fatalf("unexpected budget error: %v", err)
		}
	}
	if passes < 2 {
		t.Fatalf("compactor invoked %d times, expected once per iteration", passes)
	}
	// Compaction must have bounded growth: later sends are not monotonically
	// larger than an uncompacted loop would produce.
	if sentSizes[len(sentSizes)-1] > 5 {
		t.Fatalf("history grew despite compaction: %v", sentSizes)
	}
}
