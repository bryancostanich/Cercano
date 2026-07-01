package compaction

import "testing"

func TestReduce_SinglePassthrough(t *testing.T) {
	out := Reduce([]StructuredSummary{{Goal: "only"}})
	if out.Goal != "only" {
		t.Errorf("single part should pass through, got %q", out.Goal)
	}
}

// Reduce must be a deterministic union — never an LLM call — even for multi-
// part inputs. The prior model-reduce branch fabricated content; this test
// pins the invariant that the merge is mechanical.
func TestReduce_MultiIsDeterministicUnion(t *testing.T) {
	parts := []StructuredSummary{
		{Goal: "first", Decisions: []string{"A"}, State: "s1", Files: map[string]string{"a.go": "v1"}},
		{Goal: "second", Decisions: []string{"B", "A"}, State: "s2", Files: map[string]string{"a.go": "v2", "b.go": "new"}},
	}
	out := Reduce(parts)
	if out.Goal != "first" {
		t.Errorf("Goal should be first non-empty, got %q", out.Goal)
	}
	if len(out.Decisions) != 2 || out.Decisions[0] != "A" || out.Decisions[1] != "B" {
		t.Errorf("Decisions should union with dedup, order preserved: %v", out.Decisions)
	}
	if out.State != "s2" {
		t.Errorf("State should be last non-empty, got %q", out.State)
	}
	if out.Files["a.go"] != "v2" || out.Files["b.go"] != "new" {
		t.Errorf("Files should union with recent overriding older: %v", out.Files)
	}
}
