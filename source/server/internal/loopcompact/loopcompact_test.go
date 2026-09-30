package loopcompact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func textMsg(role llm.Role, body string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: body}}}
}

// bigHistory builds a history well past the activation floor, alternating
// user/assistant turns of substantial size.
func bigHistory(pairs int, filler int) []llm.Message {
	body := strings.Repeat("lorem ipsum dolor sit amet consectetur ", filler)
	var out []llm.Message
	for i := 0; i < pairs; i++ {
		out = append(out, textMsg(llm.RoleUser, fmt.Sprintf("request %d %s", i, body)))
		out = append(out, textMsg(llm.RoleAssistant, fmt.Sprintf("answer %d %s", i, body)))
	}
	return out
}

func countingSummarizer(calls *int) Summarize {
	return func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		*calls++
		return compaction.StructuredSummary{
			Goal:      fmt.Sprintf("summary of %d messages", len(msgs)),
			State:     "compacted",
			Decisions: []string{"kept the essentials"},
		}, nil
	}
}

func testCompactor(t *testing.T, s Summarize) *Compactor {
	t.Helper()
	c := New(Options{Config: compactor.DefaultConfig(), Summarize: s, Tokenizer: contextmeter.Default()})
	if c == nil {
		t.Fatal("New returned nil with a summarizer present")
	}
	return c
}

// Without a summarizer there is nothing to compact with: New must return nil
// so callers can wire unconditionally and simply get no compaction.
func TestNewWithoutSummarizerIsNil(t *testing.T) {
	if New(Options{Config: compactor.DefaultConfig()}) != nil {
		t.Fatal("expected nil compactor without summarizer")
	}
}

// Zero-value config must fall back to production defaults rather than
// producing a compactor with a zero activation floor (which would compact
// every trivial dispatch) or zero segments.
func TestZeroConfigFallsBackToDefaults(t *testing.T) {
	calls := 0
	c := New(Options{Summarize: countingSummarizer(&calls)})
	if c == nil {
		t.Fatal("nil compactor")
	}
	def := compactor.DefaultConfig()
	if c.cfg.ActivationFloorTokens != def.ActivationFloorTokens ||
		c.cfg.SegmentTokens != def.SegmentTokens ||
		c.cfg.VerbatimRecent != def.VerbatimRecent {
		t.Fatalf("defaults not applied: %+v", c.cfg)
	}
	if c.timeout != DefaultTimeout || c.tok == nil {
		t.Fatalf("timeout/tokenizer defaults not applied: %v", c.timeout)
	}
}

// The common case for short dispatches: below the activation floor nothing
// happens, nothing is billed, and the history is returned byte-identical.
func TestBelowActivationFloorIsNoOp(t *testing.T) {
	calls := 0
	c := testCompactor(t, countingSummarizer(&calls))
	hist := []llm.Message{textMsg(llm.RoleUser, "tiny"), textMsg(llm.RoleAssistant, "also tiny")}
	out, spent, err := c.CompactLoopHistory(context.Background(), hist)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || spent != 0 {
		t.Fatalf("summarized below floor: calls=%d spent=%d", calls, spent)
	}
	if len(out) != len(hist) || out[0].Blocks[0].Text != "tiny" {
		t.Fatalf("history altered: %+v", out)
	}
	// Empty history must be safe too.
	if out, _, err := c.CompactLoopHistory(context.Background(), nil); err != nil || len(out) != 0 {
		t.Fatalf("nil history mishandled: %v %v", out, err)
	}
}

// Past the floor the real algorithm must actually shrink the sent history and
// report the summarizer spend so the dispatch budget stays honest.
func TestAboveFloorCompactsAndReportsSpend(t *testing.T) {
	calls := 0
	c := testCompactor(t, countingSummarizer(&calls))
	tok := contextmeter.Default()
	hist := bigHistory(60, 60)
	before := compaction.TotalTokens(tok, hist)
	if before < compactor.DefaultConfig().ActivationFloorTokens {
		t.Fatalf("fixture too small to trigger compaction: %d tokens", before)
	}
	out, spent, err := c.CompactLoopHistory(context.Background(), hist)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if calls == 0 {
		t.Fatal("summarizer never called above the activation floor")
	}
	after := compaction.TotalTokens(tok, out)
	if after >= before {
		t.Fatalf("history not reduced: before=%d after=%d", before, after)
	}
	if spent <= 0 {
		t.Fatal("summarizer spend not reported")
	}
	t.Logf("compacted %d -> %d tokens (%d summarizer calls, %d tokens spent)", before, after, calls, spent)

	// State reuse: a second pass over a slightly grown history must not
	// re-summarize the already-frozen segments from scratch.
	firstCalls := calls
	grown := append(append([]llm.Message{}, hist...), textMsg(llm.RoleUser, "one more"))
	if _, _, err = c.CompactLoopHistory(context.Background(), grown); err != nil {
		t.Fatal(err)
	}
	if calls > firstCalls*2 {
		t.Fatalf("frozen state not reused: %d calls after %d", calls, firstCalls)
	}
}

// A failing summarizer must never break the dispatch: the error is advisory,
// spend is still reported, and the caller keeps its history.
func TestSummarizerFailureIsAdvisory(t *testing.T) {
	c := testCompactor(t, func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, errors.New("summarizer down")
	})
	hist := bigHistory(60, 60)
	out, _, err := c.CompactLoopHistory(context.Background(), hist)
	if err == nil {
		t.Fatal("expected an advisory error")
	}
	if len(out) != len(hist) {
		t.Fatalf("history lost on summarizer failure: %d vs %d", len(out), len(hist))
	}
}

// Synthetic timestamps must be strictly increasing so Advance can place a
// frozen boundary; real wall-clock stamps would cluster tool bursts into one
// second and stall compaction.
func TestSyntheticTimestampsAreSeparable(t *testing.T) {
	c := testCompactor(t, countingSummarizer(new(int)))
	turns := c.toTurns(bigHistory(3, 1))
	for i := 1; i < len(turns); i++ {
		if !turns[i].CreatedAt.After(turns[i-1].CreatedAt) {
			t.Fatalf("turn %d not after %d: %v vs %v", i, i-1, turns[i].CreatedAt, turns[i-1].CreatedAt)
		}
	}
	// Blocks must survive the round trip, since Advance reads BlocksJSON.
	if turns[0].BlocksJSON == "" || !strings.Contains(turns[0].BlocksJSON, "request 0") {
		t.Fatalf("blocks not carried: %q", turns[0].BlocksJSON)
	}
	if turns[0].Content == "" {
		t.Fatal("text-only turn lost its Content fallback")
	}
}

// Dispatches feed the reduced view back, unlike the store-backed full history.
func TestRepeatedPassOnReducedHistory(t *testing.T) {
	calls := 0
	c := testCompactor(t, countingSummarizer(&calls))
	hist := bigHistory(60, 60)
	out, _, err := c.CompactLoopHistory(t.Context(), hist)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalls := calls
	grown := append(out, bigHistory(60, 60)...)
	next, _, err := c.CompactLoopHistory(t.Context(), grown)
	if err != nil {
		t.Fatal(err)
	}
	if calls == beforeCalls || compaction.TotalTokens(c.tok, next) >= compaction.TotalTokens(c.tok, grown) {
		t.Fatal("second pass did not reduce newly accumulated history")
	}
}

func TestRepeatedPassDoesNotSkipUnsummarizedMessages(t *testing.T) {
	seen := map[string]bool{}
	c := testCompactor(t, func(_ context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		for _, m := range msgs {
			for _, b := range m.Blocks {
				seen[b.Text] = true
			}
		}
		return compaction.StructuredSummary{Goal: "preserve evidence"}, nil
	})
	hist := bigHistory(60, 60)
	out, _, err := c.CompactLoopHistory(t.Context(), hist)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, bigHistory(60, 61)...)
	next, _, err := c.CompactLoopHistory(t.Context(), out)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range next {
		for _, b := range m.Blocks {
			seen[b.Text] = true
		}
	}
	for i, m := range append(hist, bigHistory(60, 61)...) {
		for _, b := range m.Blocks {
			if !seen[b.Text] {
				t.Fatalf("message %d neither summarized nor retained", i)
			}
		}
	}
}
