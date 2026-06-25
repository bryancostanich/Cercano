package compactor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func TestAdvance_SameSecondBoundaryDoesNotDropTurn(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	body := strings.Repeat("lorem ipsum dolor sit amet ", 100)
	// 12 turns; the last eligible (t9) shares second 1000 with the first verbatim
	// turn (t10) — the exact straddle that would drop t10 without the trim.
	ats := []int64{100, 101, 102, 103, 104, 105, 106, 107, 108, 1000, 1000, 1001}
	var turns []conversation.Turn
	for i, at := range ats {
		c := body
		switch i {
		case 9:
			c = "MARK9 " + body
		case 10:
			c = "MARK10 " + body
		}
		turns = append(turns, conversation.Turn{
			ID: fmt.Sprintf("t%d", i), Role: "user", Content: c, CreatedAt: time.Unix(at, 0),
		})
	}
	rec := &recSummarize{}
	st, changed, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected compaction to run")
	}
	if st.FrozenThrough >= 1000 {
		t.Errorf("FrozenThrough=%d must be strictly below the shared boundary second 1000", st.FrozenThrough)
	}
	view, err := BuildSendView(turns, st)
	if err != nil {
		t.Fatal(err)
	}
	out := flat(view)
	if !strings.Contains(out, "MARK10") {
		t.Error("the same-second verbatim turn (t10) was dropped from the send view")
	}
	if !strings.Contains(out, "MARK9") {
		t.Error("t9 (held back to avoid the collision) should be live, not dropped")
	}
}

// recSummarize records how many times it's called and returns a marked summary.
type recSummarize struct{ n int }

func (r *recSummarize) fn(_ context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
	id := r.n
	r.n++
	return compaction.StructuredSummary{Goal: fmt.Sprintf("SEG%d", id)}, nil
}

// bigTurns builds n user turns each ~tokensEach tokens, created at 100+i.
func bigTurns(n, tokensEach int) []conversation.Turn {
	body := ""
	for len(body)/4 < tokensEach {
		body += "lorem ipsum dolor sit amet "
	}
	var ts []conversation.Turn
	for i := 0; i < n; i++ {
		ts = append(ts, conversation.Turn{
			ID: fmt.Sprintf("t%d", i), Role: "user", Content: body,
			CreatedAt: time.Unix(int64(100+i), 0),
		})
	}
	return ts
}

func TestAdvance_ActivationGateSkipsSmall(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 100000, SegmentTokens: 8000, VerbatimRecent: 6}
	rec := &recSummarize{}
	turns := bigTurns(4, 500) // ~2k tokens total, below the 100k floor
	_, changed, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if changed || rec.n != 0 {
		t.Errorf("below activation floor: expected no work, changed=%v calls=%d", changed, rec.n)
	}
}

func TestAdvance_FreezesSegmentsAndReuses(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	rec := &recSummarize{}
	// 12 turns × ~1000 tok = ~12k total; eligible = all but last 2 = 10 turns ~10k
	// → at 4k segments, ~3 new segments.
	turns := bigTurns(12, 1000)

	st, changed, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected compaction to run")
	}
	if rec.n < 2 {
		t.Fatalf("expected multiple segments mapped, got %d", rec.n)
	}
	if st.ConsolidatedJSON == "" {
		t.Error("expected a consolidated summary")
	}
	if st.FrozenThrough == 0 {
		t.Error("expected the frozen boundary to advance")
	}
	// Boundary must leave the last VerbatimRecent turns live.
	lastFrozenIdx := len(turns) - cfg.VerbatimRecent - 1
	if st.FrozenThrough != turns[lastFrozenIdx].CreatedAt.Unix() {
		t.Errorf("FrozenThrough=%d, want %d (last eligible turn)", st.FrozenThrough, turns[lastFrozenIdx].CreatedAt.Unix())
	}
	var segs []compaction.StructuredSummary
	_ = json.Unmarshal([]byte(st.SegmentSummariesJSON), &segs)
	firstRunSegs := len(segs)
	callsAfterFirst := rec.n

	// Second pass with NO new turns: nothing eligible past the boundary → no work,
	// and crucially the frozen segments are NOT re-summarized.
	st2, changed2, err := Advance(context.Background(), turns, st, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("second pass with no new turns should be a no-op")
	}
	if rec.n != callsAfterFirst {
		t.Errorf("frozen segments must not be re-summarized: calls went %d → %d", callsAfterFirst, rec.n)
	}
	var segs2 []compaction.StructuredSummary
	_ = json.Unmarshal([]byte(st2.SegmentSummariesJSON), &segs2)
	if len(segs2) != firstRunSegs {
		t.Errorf("segment count changed on no-op pass: %d → %d", firstRunSegs, len(segs2))
	}
}
