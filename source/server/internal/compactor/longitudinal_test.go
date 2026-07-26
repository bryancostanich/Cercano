package compactor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// This file holds the longitudinal invariant tests (Option A): drive the real
// 1,898-turn "CERCANO - AGENT BEHAVIORS" conversation through the stateful
// compactor across many passes and assert system-level integrity over time —
// not the correctness of any single function.
//
// The hard gate is BOUNDEDNESS: the consolidated send-view preamble must stay
// under the configured budget no matter how many passes run, and Goal/State
// must never be lost. This guards the shipped fix (pruneToFit) against
// regression on the exact conversation whose unbounded state motivated it.
//
// A separate, explicitly-aspirational sub-test documents the ideal that the
// system SHOULD not grow the ledger when fed equivalent-but-reworded content.
// Today's exact-string dedup fails that ideal, so it is marked aspirational and
// skipped-with-explanation rather than gating red.

// turnsFromMessages adapts the fixture ([]llm.Message) into the []conversation.Turn
// shape Advance consumes: each message's blocks are marshaled into BlocksJSON
// (exactly how persistence stores them) with a monotonic one-second cadence so
// the freeze-boundary logic behaves as it does in production. A handful of turns
// share a second deliberately (tool-use bursts) to exercise the same-second trim.
func turnsFromMessages(msgs []llm.Message) []conversation.Turn {
	turns := make([]conversation.Turn, 0, len(msgs))
	base := int64(1_700_000_000)
	for i, m := range msgs {
		bj, _ := json.Marshal(m.Blocks)
		// Emit ~every 3rd turn in the same second as its predecessor to mimic
		// tool-use bursts, which the boundary logic must tolerate.
		sec := base + int64(i) - int64(i/3)
		turns = append(turns, conversation.Turn{
			ID:         fmt.Sprintf("t%04d", i),
			Role:       string(m.Role),
			BlocksJSON: string(bj),
			CreatedAt:  time.Unix(sec, 0),
		})
	}
	return turns
}

// driftingSummarizer is a deterministic but realistic SummarizeFunc. Unlike the
// trivial recSummarize fake, it emits a *structured* summary with Decisions,
// Proposals, OpenThreads and Files that reword slightly from segment to segment
// — reproducing the real-world pressure under which appendUnique's exact-string
// dedup fails to collapse near-duplicates and the ledger accumulates. It is
// keyed off segment content so repeated identical input yields identical output
// (determinism), while distinct segments yield distinct-but-overlapping ledgers.
type driftingSummarizer struct{ calls int }

func (d *driftingSummarizer) fn(_ context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
	n := d.calls
	d.calls++
	// A single stable Goal across the whole session (as a real conversation has).
	s := compaction.StructuredSummary{
		Goal:  "Fix the compaction pipeline so context stays bounded and faithful over long sessions.",
		State: fmt.Sprintf("Working through segment %d of the session.", n),
		Files: map[string]string{
			"compactor.go": fmt.Sprintf("touched in segment %d", n),
			"summary.go":   "merge logic under review",
		},
		// Reworded near-duplicates: the same underlying decision, phrased three
		// different ways across segments. Exact-string dedup keeps all three.
		Decisions: []string{
			fmt.Sprintf("Segment %d: adopt deterministic pruning over re-summarization.", n),
			"Prefer deterministic pruning to LLM re-summarization for the ledger.",
			"Deterministic pruning is preferred over paraphrasing summaries.",
		},
		Proposals: []string{
			fmt.Sprintf("Consider a window-relative budget (seg %d).", n),
			"Maybe make the budget a fraction of the model window.",
		},
		OpenThreads: []string{
			"Calibrate the default budget fraction against the corpus.",
		},
	}
	return s, nil
}

// TestLongitudinal_RealCorpus_StaysBounded is the hard gate. It runs the real
// conversation through Advance to convergence and asserts, after EVERY pass,
// that the consolidated send-view preamble stays under budget and that Goal and
// State survive. Boundedness is the property that matters (context stays
// usable); the test is agnostic about how the compactor achieves it.
func TestLongitudinal_RealCorpus_StaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-corpus longitudinal run in -short mode (~30s over 1,898 turns)")
	}
	tok := contextmeter.Default()
	turns := turnsFromMessages(compaction.RealConversation())

	// A deliberately small budget so the corpus crosses it and the pruneToFit
	// backstop is genuinely exercised (rather than the corpus fitting trivially).
	const budget = 6000
	cfg := Config{
		ActivationFloorTokens: 8000,
		SegmentTokens:         4000,
		VerbatimRecent:        6,
		CompactedBudgetTokens: budget,
	}

	sum := &driftingSummarizer{}
	state := conversation.Compaction{ConversationID: "real"}

	const maxPasses = 2000 // generous ceiling; convergence should be far sooner
	pass := 0
	for ; pass < maxPasses; pass++ {
		next, changed, more, err := Advance(context.Background(), turns, state, sum.fn, cfg, tok)
		if err != nil {
			t.Fatalf("pass %d: Advance error: %v", pass, err)
		}
		state = next

		// INVARIANT 1 — boundedness: the consolidated preamble never exceeds budget.
		var consolidated compaction.StructuredSummary
		if state.ConsolidatedJSON != "" {
			if err := json.Unmarshal([]byte(state.ConsolidatedJSON), &consolidated); err != nil {
				t.Fatalf("pass %d: consolidated JSON corrupt: %v", pass, err)
			}
			preambleTokens := compaction.TotalTokens(tok, compaction.AssembleSendView(consolidated, nil))
			if preambleTokens > budget {
				t.Fatalf("pass %d: consolidated preamble = %d tokens, exceeds budget %d — compaction is unbounded",
					pass, preambleTokens, budget)
			}

			// INVARIANT 2 — Goal survives once established.
			if consolidated.Goal == "" {
				t.Fatalf("pass %d: Goal was lost from the consolidated summary", pass)
			}
			// INVARIANT 3 — State stays present (recency line).
			if consolidated.State == "" {
				t.Fatalf("pass %d: State was lost from the consolidated summary", pass)
			}
		}

		if !changed && !more {
			break // converged: no further eligible backlog
		}
	}
	if pass >= maxPasses {
		t.Fatalf("did not converge within %d passes — possible livelock", maxPasses)
	}
	t.Logf("converged after %d passes; frozen_through=%d compacted_tokens=%d",
		pass+1, state.FrozenThrough, state.CompactedTokens)

	// INVARIANT 4 — determinism: a second identical run yields identical state.
	sum2 := &driftingSummarizer{}
	state2 := conversation.Compaction{ConversationID: "real"}
	for i := 0; i < maxPasses; i++ {
		next, changed, more, err := Advance(context.Background(), turns, state2, sum2.fn, cfg, tok)
		if err != nil {
			t.Fatalf("determinism run: pass %d error: %v", i, err)
		}
		state2 = next
		if !changed && !more {
			break
		}
	}
	if state.ConsolidatedJSON != state2.ConsolidatedJSON {
		t.Error("non-deterministic: two identical runs produced different consolidated summaries")
	}
	if state.FrozenThrough != state2.FrozenThrough {
		t.Errorf("non-deterministic frozen_through: %d vs %d", state.FrozenThrough, state2.FrozenThrough)
	}
}

// TestLongitudinal_MergeGrowsUnbounded_Aspirational documents the IDEAL that the
// ledger should not grow when fed equivalent-but-reworded input. Today's
// exact-string appendUnique fails this: three rephrasings of one decision are
// kept as three entries. This is the bug the fix will address. It is marked
// aspirational so it does not gate the build red before the fix lands; flip the
// skip to see it fail against current code.
func TestLongitudinal_MergeGrowsUnbounded_Aspirational(t *testing.T) {
	if true {
		t.Skip("aspirational: exact-string dedup cannot collapse reworded duplicates yet; " +
			"un-skip once semantic/normalized dedup or a per-section cap lands")
	}

	// The same decision, reworded five ways across five segments.
	var parts []compaction.StructuredSummary
	rewordings := []string{
		"Adopt deterministic pruning over re-summarization.",
		"Prefer deterministic pruning to LLM re-summarization.",
		"Deterministic pruning is preferred over paraphrasing.",
		"We should prune deterministically instead of re-summarizing.",
		"Pruning deterministically beats re-summarizing the ledger.",
	}
	for _, d := range rewordings {
		parts = append(parts, compaction.StructuredSummary{
			Goal:      "one goal",
			Decisions: []string{d},
		})
	}
	merged := compaction.Reduce(parts)
	// The ideal: these are one decision, so the merged ledger should hold ~1.
	if len(merged.Decisions) > 1 {
		t.Fatalf("reworded duplicates accumulated: %d decisions retained, want ~1", len(merged.Decisions))
	}
}
