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
// summary. Tests inspect texts to assert the summarizer is only ever called on
// raw segments — never on an already-consolidated summary (the shrink path is
// deterministic, not a re-summarize).
type recCall struct {
	texts []string
	ret   compaction.StructuredSummary
}

func (r *recCall) fn(_ context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
	r.texts = append(r.texts, flat(msgs))
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

// When the merged ledger exceeds the compacted budget, the pass shrinks it
// DETERMINISTICALLY — dropping whole entries in recency order — instead of
// re-summarizing. It must never paraphrase (no summarizer call for the shrink),
// the consolidated view must fit under budget, and the high-signal Goal must
// survive while excess Decisions are pruned.
func TestAdvancePrunesDeterministicallyWhenSummariesExceedBudget(t *testing.T) {
	tok := contextmeter.Default()
	budget := 800
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2, CompactedBudgetTokens: budget}
	fat := fatPart()
	seededJSON, _ := json.Marshal([]compaction.StructuredSummary{fat})
	if compaction.TotalTokens(tok, compaction.AssembleSendView(compaction.Reduce([]compaction.StructuredSummary{fat}), nil)) <= budget {
		t.Fatal("test setup: seeded parts should exceed the budget")
	}
	state := conversation.Compaction{SegmentSummariesJSON: string(seededJSON)}

	// The summarizer here summarizes the freshly-frozen live turns (a normal
	// segment pass); it must NOT be invoked to shrink the consolidated ledger.
	rc := &recCall{ret: compaction.StructuredSummary{Goal: "seg", State: "s"}}
	turns := bigTurns(12, 3000)
	st, changed, _, err := Advance(context.Background(), turns, state, rc.fn, cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a pass")
	}
	// No summarizer call ever received an already-consolidated summary: the
	// shrink is deterministic, not a re-summarize.
	for _, txt := range rc.texts {
		if strings.Contains(txt, "[conversation summary]") {
			t.Fatalf("shrink must be deterministic — summarizer was called on a consolidated summary:\n%s", txt)
		}
	}
	// The consolidated view now fits under the budget.
	var consolidated compaction.StructuredSummary
	if err := json.Unmarshal([]byte(st.ConsolidatedJSON), &consolidated); err != nil {
		t.Fatal(err)
	}
	if got := compaction.TotalTokens(tok, compaction.AssembleSendView(consolidated, nil)); got > budget {
		t.Fatalf("consolidated view %d tokens still over budget %d", got, budget)
	}
	// Goal survives (verbatim, not paraphrased) and decisions were pruned, not
	// fabricated: every surviving decision is one of the originals.
	if consolidated.Goal != "FATGOAL" {
		t.Fatalf("Goal should survive pruning verbatim, got %q", consolidated.Goal)
	}
	if len(consolidated.Decisions) >= len(fat.Decisions) {
		t.Fatalf("expected decisions to be pruned: had %d, still %d", len(fat.Decisions), len(consolidated.Decisions))
	}
	orig := map[string]bool{}
	for _, d := range fat.Decisions {
		orig[d] = true
	}
	for _, d := range consolidated.Decisions {
		if !orig[d] {
			t.Fatalf("pruning fabricated a decision not in the input: %q", d)
		}
	}
}

// pruneToFit preserves Goal and State even under an aggressively small budget
// rather than gutting the summary to hit a byte target.
func TestPruneToFitKeepsGoalAndState(t *testing.T) {
	tok := contextmeter.Default()
	s := fatPart()
	s.State = "STATELINE"
	out := pruneToFit(s, 1, tok) // impossibly small budget
	if out.Goal != "FATGOAL" || out.State != "STATELINE" {
		t.Fatalf("Goal/State must survive even under a tiny budget, got Goal=%q State=%q", out.Goal, out.State)
	}
}

// budgetTokens falls back to the segment-relative bound when no explicit
// window-relative budget is configured, so zero-value Configs keep working.
func TestBudgetTokensFallback(t *testing.T) {
	if got := budgetTokens(Config{CompactedBudgetTokens: 42000}); got != 42000 {
		t.Fatalf("explicit budget should win, got %d", got)
	}
	if got := budgetTokens(Config{SegmentTokens: 8000}); got != legacyBoundSegments*8000 {
		t.Fatalf("fallback should be %d, got %d", legacyBoundSegments*8000, got)
	}
	if got := budgetTokens(Config{}); got != legacyBoundSegments*DefaultConfig().SegmentTokens {
		t.Fatalf("zero Config should use default segment tokens, got %d", got)
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

func TestAdvance_KeepsPartialProgressWhenSegmentFails(t *testing.T) {
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 500, VerbatimRecent: 2}
	turns := bigTurns(12, 3000)

	// Summarizer succeeds twice then fails — models a pass whose deadline
	// expires partway through its segments.
	calls, failAfter := 0, 2
	summarize := func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		calls++
		if calls > failAfter {
			return compaction.StructuredSummary{}, context.DeadlineExceeded
		}
		return compaction.StructuredSummary{Goal: "g"}, nil
	}

	st, changed, more, err := Advance(context.Background(), turns, conversation.Compaction{}, summarize, cfg, tok)
	if err != nil {
		t.Fatalf("partial progress must not surface the segment error: %v", err)
	}
	if !changed || !more {
		t.Fatalf("expected persisted partial progress with backlog: changed=%v more=%v", changed, more)
	}
	if st.FrozenThrough <= 0 {
		t.Fatal("FrozenThrough must advance for the completed segments")
	}
	var parts []compaction.StructuredSummary
	if err := json.Unmarshal([]byte(st.SegmentSummariesJSON), &parts); err != nil || len(parts) != failAfter {
		t.Fatalf("want %d kept segment summaries, got %d (err=%v)", failAfter, len(parts), err)
	}

	// Zero completed segments: nothing to keep — the error must surface so
	// real failures (model down) stay visible.
	calls, failAfter = 0, 0
	_, changed, more, err = Advance(context.Background(), turns, conversation.Compaction{}, summarize, cfg, tok)
	if err == nil || changed || more {
		t.Fatalf("zero completed segments must surface the error: err=%v changed=%v more=%v", err, changed, more)
	}
}

func TestAdvance_EmptySummaryFailsInsteadOfFreezing(t *testing.T) {
	// A summarizer that "succeeds" with an empty summary (broken local model,
	// zero-token cloud completion, unparseable output) must fail the pass.
	// Accepting it would advance FrozenThrough and hide the segment's content
	// behind nothing — silent, unbounded context loss (the empty-parts
	// incident of 2026-07-15: 63 consecutive segments frozen behind empty
	// summaries).
	tok := contextmeter.Default()
	cfg := Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	turns := bigTurns(12, 1000)

	empty := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, nil
	}
	st, changed, more, err := Advance(context.Background(), turns, conversation.Compaction{}, empty, cfg, tok)
	if err == nil {
		t.Fatal("an empty summary for a non-trivial segment must surface as an error")
	}
	if changed || more {
		t.Fatalf("no state may change on an empty summary: changed=%v more=%v", changed, more)
	}
	if st.FrozenThrough != 0 || st.SegmentSummariesJSON != "" {
		t.Fatalf("state must be untouched: %+v", st)
	}

	// Partial-progress parity with segment errors: segments that summarized
	// non-empty before the empty one are kept, and the boundary stays in
	// lockstep with the kept summaries.
	n := 0
	emptyAfterOne := func(_ context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
		n++
		if n > 1 {
			return compaction.StructuredSummary{}, nil
		}
		return compaction.StructuredSummary{Goal: "SEG0"}, nil
	}
	st, changed, more, err = Advance(context.Background(), turns, conversation.Compaction{}, emptyAfterOne, cfg, tok)
	if err != nil {
		t.Fatalf("partial progress must not surface the empty-summary error: %v", err)
	}
	if !changed || !more {
		t.Fatalf("expected persisted partial progress with backlog: changed=%v more=%v", changed, more)
	}
	var parts []compaction.StructuredSummary
	if err := json.Unmarshal([]byte(st.SegmentSummariesJSON), &parts); err != nil || len(parts) != 1 {
		t.Fatalf("want 1 kept segment summary, got %d (err=%v)", len(parts), err)
	}
}
