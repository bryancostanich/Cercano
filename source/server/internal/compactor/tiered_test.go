package compactor

import (
	"reflect"
	"testing"

	"cercano/source/server/internal/compaction"
)

// seg builds a StructuredSummary tagged so tier behavior is legible in failures.
func seg(tag string) compaction.StructuredSummary {
	return compaction.StructuredSummary{
		Goal:        "goal-" + tag,
		Decisions:   []string{"dec-" + tag},
		Proposals:   []string{"prop-" + tag},
		OpenThreads: []string{"open-" + tag},
		Files:       map[string]string{"f-" + tag: "state-" + tag},
		State:       "state-" + tag,
	}
}

// recent<=0 disables tiering entirely — the input is returned unchanged.
func TestTiered_DisabledIsNoOp(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b"), seg("c")}
	got := applyTieredRetention(in, 0)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("recent=0 must be a no-op")
	}
}

// When there are no more segments than the recent window, nothing is old enough
// to compress: all kept verbatim.
func TestTiered_AllRecentKeptVerbatim(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b")}
	got := applyTieredRetention(in, 3)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("with len<=recent everything should be verbatim")
	}
}

// Middle tier (older-but-not-ancient) drops Proposals+OpenThreads, keeps the
// rest. With recent=2 and 3 segments the ancient tier can't open (needs
// len>2*recent → >4), so seg[0] is middle, seg[1]+seg[2] recent.
func TestTiered_MiddleDropsTransient(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b"), seg("c")}
	got := applyTieredRetention(in, 2)

	// Recent tail untouched.
	if !reflect.DeepEqual(got[1], seg("b")) || !reflect.DeepEqual(got[2], seg("c")) {
		t.Fatalf("recent segments must be verbatim, got %+v %+v", got[1], got[2])
	}
	// Middle segment: Proposals + OpenThreads gone, Decisions/Files/Goal/State kept.
	if len(got[0].Proposals) != 0 {
		t.Errorf("middle seg[0] should have no Proposals, got %v", got[0].Proposals)
	}
	if len(got[0].OpenThreads) != 0 {
		t.Errorf("middle seg[0] should have no OpenThreads, got %v", got[0].OpenThreads)
	}
	if len(got[0].Decisions) == 0 {
		t.Errorf("middle seg[0] should keep Decisions")
	}
	if got[0].Goal == "" || got[0].State == "" || len(got[0].Files) == 0 {
		t.Errorf("middle seg[0] must keep Goal/State/Files")
	}
}

// Ancient tier keeps only Goal/State/Files. With recent=1 and 4 segments,
// ancientEnd = len-2*recent = 2, so seg[0],seg[1] are ancient, seg[2] middle,
// seg[3] recent.
func TestTiered_AncientKeepsOnlyDurable(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b"), seg("c"), seg("d")}
	got := applyTieredRetention(in, 1)

	for _, i := range []int{0, 1} {
		if len(got[i].Decisions) != 0 || len(got[i].Proposals) != 0 || len(got[i].OpenThreads) != 0 {
			t.Errorf("ancient seg[%d] should keep only Goal/State/Files, got %+v", i, got[i])
		}
		if got[i].Goal == "" || got[i].State == "" || len(got[i].Files) == 0 {
			t.Errorf("ancient seg[%d] must retain Goal/State/Files", i)
		}
	}
	// seg[2] middle: Decisions kept, transient gone.
	if len(got[2].Decisions) == 0 || len(got[2].Proposals) != 0 {
		t.Errorf("seg[2] should be middle tier, got %+v", got[2])
	}
	// seg[3] recent: verbatim.
	if !reflect.DeepEqual(got[3], seg("d")) {
		t.Errorf("seg[3] should be verbatim recent, got %+v", got[3])
	}
}

// Tiering must not mutate the caller's input — especially the Files maps, which
// are shared references without a defensive copy.
func TestTiered_DoesNotMutateInput(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b"), seg("c"), seg("d")}
	// Snapshot seg[0]'s originals.
	origProps := append([]string(nil), in[0].Proposals...)
	origFilesLen := len(in[0].Files)

	_ = applyTieredRetention(in, 1)

	if !reflect.DeepEqual(in[0].Proposals, origProps) {
		t.Errorf("input Proposals mutated: %v", in[0].Proposals)
	}
	if len(in[0].Files) != origFilesLen {
		t.Errorf("input Files map mutated: len now %d", len(in[0].Files))
	}
}

// Goal (first-non-empty), State (last-non-empty) and Files (union) must survive
// a full Reduce over tiered parts even when every tier fired — the invariant the
// design guarantees.
func TestTiered_ReduceKeepsGoalStateFiles(t *testing.T) {
	in := []compaction.StructuredSummary{seg("a"), seg("b"), seg("c"), seg("d")}
	tiered := applyTieredRetention(in, 1)
	red := compaction.Reduce(tiered)

	if red.Goal != "goal-a" {
		t.Errorf("Goal should be first-non-empty 'goal-a', got %q", red.Goal)
	}
	if red.State != "state-d" {
		t.Errorf("State should be last-non-empty 'state-d', got %q", red.State)
	}
	// Every segment's file survives the union — including ancient ones.
	for _, tag := range []string{"a", "b", "c", "d"} {
		if _, ok := red.Files["f-"+tag]; !ok {
			t.Errorf("file f-%s from a tiered segment was lost in Reduce", tag)
		}
	}
}
