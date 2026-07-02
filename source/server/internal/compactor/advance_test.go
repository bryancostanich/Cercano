package compactor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
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
	st, changed, _, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
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
	_, changed, _, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
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

	st, changed, _, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
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
	st2, changed2, _, err := Advance(context.Background(), turns, st, rec.fn, cfg, tok)
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

// bodyOf returns a lorem body whose char length is at least 4*tokensEach — big
// enough that a single fat turn exceeds a small SegmentTokens, so each turn is
// its own segment and the segment↔turn mapping is exact and deterministic.
func bodyOf(tokensEach int) string {
	body := ""
	for len(body)/4 < tokensEach {
		body += "lorem ipsum dolor sit amet "
	}
	return body
}

func TestAdvanceCapsSegmentsPerPassAndSignalsMore(t *testing.T) {
	tok := contextmeter.Default()
	// Small SegmentTokens + fat turns ⇒ each turn is its own segment, so the
	// eligible span yields many segments (> maxSegmentsPerPass).
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2}
	turns := bigTurns(12, 3000) // 12 fat turns, distinct seconds 100..111
	rec := &recSummarize{}

	st, changed, more, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !more {
		t.Fatalf("expected a capped pass with backlog: changed=%v more=%v", changed, more)
	}
	if rec.n == 0 || rec.n > maxSegmentsPerPass {
		t.Fatalf("summarize calls = %d, want 1..%d", rec.n, maxSegmentsPerPass)
	}
	// FrozenThrough advanced, but not through the whole eligible span.
	eligibleLast := turns[len(turns)-cfg.VerbatimRecent-1].CreatedAt.Unix()
	if st.FrozenThrough <= 0 || st.FrozenThrough >= eligibleLast {
		t.Fatalf("FrozenThrough=%d must advance but stay below the full eligible span %d", st.FrozenThrough, eligibleLast)
	}

	// Repeated passes converge: more eventually false, FrozenThrough monotonic,
	// each pass bounded by maxSegmentsPerPass.
	prevFrozen := st.FrozenThrough
	for i := 0; more; i++ {
		if i > 20 {
			t.Fatal("compaction did not converge within 20 passes")
		}
		before := rec.n
		st, changed, more, err = Advance(context.Background(), turns, st, rec.fn, cfg, tok)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatalf("pass %d: still more backlog but nothing changed", i)
		}
		if rec.n-before > maxSegmentsPerPass {
			t.Fatalf("pass %d summarized %d segments, cap is %d", i, rec.n-before, maxSegmentsPerPass)
		}
		if st.FrozenThrough <= prevFrozen {
			t.Fatalf("pass %d: FrozenThrough did not advance: %d → %d", i, prevFrozen, st.FrozenThrough)
		}
		prevFrozen = st.FrozenThrough
	}
	// Converged: a further pass is a clean no-op.
	_, changed, more, err = Advance(context.Background(), turns, st, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if changed || more {
		t.Fatalf("after convergence expected a no-op: changed=%v more=%v", changed, more)
	}
}

func TestAdvanceCappedBoundaryRespectsSameSecond(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2}
	body := bodyOf(3000) // fat: each turn is its own segment
	// 12 fat turns; t3 and t4 share second 103. With maxSegmentsPerPass=4 the cap
	// would land on t3, but t4 (the first un-covered turn) shares its second, so
	// the boundary must be pulled below 103 → freeze through t2.
	secs := []int64{100, 101, 102, 103, 103, 104, 105, 106, 107, 108, 109, 110}
	var turns []conversation.Turn
	for i, s := range secs {
		turns = append(turns, conversation.Turn{
			ID: fmt.Sprintf("t%d", i), Role: "user", Content: body, CreatedAt: time.Unix(s, 0),
		})
	}
	rec := &recSummarize{}
	st, changed, more, err := Advance(context.Background(), turns, conversation.Compaction{}, rec.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !more {
		t.Fatalf("expected a capped pass with backlog: changed=%v more=%v", changed, more)
	}
	if st.FrozenThrough != 102 {
		t.Fatalf("FrozenThrough=%d, want 102 (pulled below the shared second 103)", st.FrozenThrough)
	}
	if rec.n > maxSegmentsPerPass {
		t.Fatalf("summarize calls = %d exceed cap %d", rec.n, maxSegmentsPerPass)
	}
	// Both same-second turns t3 and t4 must remain live (not dropped).
	var haveT3, haveT4 bool
	for _, tr := range liveTurns(turns, st.FrozenThrough) {
		switch tr.ID {
		case "t3":
			haveT3 = true
		case "t4":
			haveT4 = true
		}
	}
	if !haveT3 || !haveT4 {
		t.Fatalf("same-second turns dropped: t3 live=%v t4 live=%v", haveT3, haveT4)
	}
}

// recCall records the flattened text of each summarize call and returns a fixed
// summary. When errOnConsolidate is set it fails only the re-consolidation call
// (identified by the rendered summary preamble), so new-segment calls still
// succeed.
type recCall struct {
	texts            []string
	ret              compaction.StructuredSummary
	errOnConsolidate bool
}

func (r *recCall) fn(_ context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
	txt := flat(msgs)
	r.texts = append(r.texts, txt)
	if strings.Contains(txt, "[conversation summary]") && r.errOnConsolidate {
		return compaction.StructuredSummary{}, fmt.Errorf("shrink failed")
	}
	return r.ret, nil
}

// fatPart builds a StructuredSummary that renders well past a small bound.
func fatPart() compaction.StructuredSummary {
	s := compaction.StructuredSummary{Goal: "FATGOAL"}
	for i := 0; i < 400; i++ {
		s.Decisions = append(s.Decisions, fmt.Sprintf("decision %04d: a fairly long line of reasoning that adds tokens", i))
	}
	return s
}

func TestAdvanceReconsolidatesWhenSummariesExceedBound(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2}
	fat := fatPart()
	seededJSON, _ := json.Marshal([]compaction.StructuredSummary{fat})
	bound := reconsolidateThresholdSegments * cfg.SegmentTokens
	if compaction.TotalTokens(tok, compaction.AssembleSendView(compaction.Reduce([]compaction.StructuredSummary{fat}), nil)) <= bound {
		t.Fatal("test setup: seeded parts should exceed the bound")
	}
	state := conversation.Compaction{SegmentSummariesJSON: string(seededJSON)}

	rc := &recCall{ret: compaction.StructuredSummary{Goal: "re", State: "small"}}
	turns := bigTurns(12, 3000)
	st, changed, _, err := Advance(context.Background(), turns, state, rc.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a pass")
	}
	// Exactly one part after re-consolidation.
	var parts []compaction.StructuredSummary
	if err := json.Unmarshal([]byte(st.SegmentSummariesJSON), &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("SegmentSummariesJSON holds %d parts, want exactly 1 (re-consolidated)", len(parts))
	}
	// The consolidated view now renders under the bound.
	var consolidated compaction.StructuredSummary
	_ = json.Unmarshal([]byte(st.ConsolidatedJSON), &consolidated)
	if got := compaction.TotalTokens(tok, compaction.AssembleSendView(consolidated, nil)); got > bound {
		t.Fatalf("consolidated view %d tokens still over bound %d", got, bound)
	}
	// The final summarize call was the re-consolidation over the fat content.
	last := rc.texts[len(rc.texts)-1]
	if !strings.Contains(last, "[conversation summary]") || !strings.Contains(last, "FATGOAL") {
		t.Fatalf("last summarize call was not the consolidated content (len=%d)", len(last))
	}
}

func TestAdvanceShrinkFailureSurfaces(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2}
	seededJSON, _ := json.Marshal([]compaction.StructuredSummary{fatPart()})
	state := conversation.Compaction{
		FrozenThrough:        42,
		SegmentSummariesJSON: string(seededJSON),
		ConsolidatedJSON:     "orig",
	}

	rc := &recCall{ret: compaction.StructuredSummary{Goal: "re", State: "small"}, errOnConsolidate: true}
	turns := bigTurns(12, 3000)
	st, changed, more, err := Advance(context.Background(), turns, state, rc.fn, cfg, tok)
	if err == nil {
		t.Fatal("expected the re-consolidation error to surface")
	}
	if changed || more {
		t.Fatalf("on shrink failure expect no change: changed=%v more=%v", changed, more)
	}
	// Original state returned verbatim — a grown state is never persisted.
	if st.FrozenThrough != 42 || st.SegmentSummariesJSON != string(seededJSON) || st.ConsolidatedJSON != "orig" {
		t.Fatalf("shrink failure must return the original state, got %+v", st)
	}
}

// TestEligibleMessagesWithTurnsMirrorsBuildLLMHistory is a drift guard:
// eligibleMessagesWithTurns duplicates agent.BuildLLMHistory + llm.RepairPairing
// so it can carry per-message turn provenance. If either canonical function
// changes and the mirror is not updated, the two silently diverge — the frozen
// boundary would then double-summarize or drop turns. This test pins the mirror
// to the canonical pipeline over adversarial turn shapes.
func TestEligibleMessagesWithTurnsMirrorsBuildLLMHistory(t *testing.T) {
	use := func(id string) string {
		return fmt.Sprintf(`[{"type":"tool_use","id":"%s","name":"read","input":{"path":"x"}}]`, id)
	}
	res := func(id string) string {
		return fmt.Sprintf(`[{"type":"tool_result","tool_use_id":"%s","content":"data"}]`, id)
	}
	at := func(i int) time.Time { return time.Unix(int64(100+i), 0) }

	cases := []struct {
		name  string
		turns []conversation.Turn
	}{
		{"empty turns dropped", []conversation.Turn{
			{ID: "t0", Role: "user", Content: "hello", CreatedAt: at(0)},
			{ID: "t1", Role: "user", Content: "", CreatedAt: at(1)}, // no blocks, no content
			{ID: "t2", Role: "assistant", Content: "world", CreatedAt: at(2)},
		}},
		{"content-only turns", []conversation.Turn{
			{ID: "t0", Role: "user", Content: "a", CreatedAt: at(0)},
			{ID: "t1", Role: "assistant", Content: "b", CreatedAt: at(1)},
			{ID: "t2", Role: "system", Content: "c", CreatedAt: at(2)},
		}},
		{"orphaned tool_use dropped", []conversation.Turn{
			{ID: "t0", Role: "user", Content: "q", CreatedAt: at(0)},
			{ID: "t1", Role: "assistant", BlocksJSON: use("x1"), CreatedAt: at(1)}, // no result follows
			{ID: "t2", Role: "user", Content: "next", CreatedAt: at(2)},
		}},
		{"orphaned tool_result dropped", []conversation.Turn{
			{ID: "t0", Role: "user", BlocksJSON: res("ghost"), CreatedAt: at(0)}, // no prior use
			{ID: "t1", Role: "user", Content: "next", CreatedAt: at(1)},
		}},
		{"cross-turn tool pair kept", []conversation.Turn{
			{ID: "t0", Role: "assistant", BlocksJSON: use("x1"), CreatedAt: at(0)},
			{ID: "t1", Role: "user", BlocksJSON: res("x1"), CreatedAt: at(1)},
			{ID: "t2", Role: "user", Content: "next", CreatedAt: at(2)},
		}},
		{"mixed", []conversation.Turn{
			{ID: "t0", Role: "user", Content: "start", CreatedAt: at(0)},
			{ID: "t1", Role: "user", Content: "", CreatedAt: at(1)},                // empty → dropped
			{ID: "t2", Role: "assistant", BlocksJSON: use("x1"), CreatedAt: at(2)}, // paired
			{ID: "t3", Role: "user", BlocksJSON: res("x1"), CreatedAt: at(3)},
			{ID: "t4", Role: "assistant", BlocksJSON: use("x2"), CreatedAt: at(4)}, // orphaned use → dropped
			{ID: "t5", Role: "user", BlocksJSON: res("ghost"), CreatedAt: at(5)},   // orphaned result → dropped
			{ID: "t6", Role: "assistant", Content: "done", CreatedAt: at(6)},
			{ID: "t7", Role: "user", BlocksJSON: `not json`, Content: "fallback", CreatedAt: at(7)}, // corrupt blocks → Content
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := agent.BuildLLMHistory(tc.turns)
			got, idxs := eligibleMessagesWithTurns(tc.turns)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mirror diverged from BuildLLMHistory:\n got: %+v\nwant: %+v", got, want)
			}
			if len(idxs) != len(got) {
				t.Fatalf("provenance length %d != message count %d", len(idxs), len(got))
			}
			for i, ti := range idxs {
				if ti < 0 || ti >= len(tc.turns) {
					t.Fatalf("idxs[%d]=%d out of range", i, ti)
				}
				if i > 0 && ti <= idxs[i-1] {
					t.Fatalf("provenance not strictly increasing: idxs[%d]=%d after %d", i, ti, idxs[i-1])
				}
			}
		})
	}
}
