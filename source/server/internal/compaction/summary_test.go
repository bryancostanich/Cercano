package compaction

import (
	"strings"
	"testing"
)

func TestMergeSummaries_DedupesAndOverrides(t *testing.T) {
	a := StructuredSummary{
		Goal:      "ship compaction",
		Decisions: []string{"use structured summaries"},
		Files:     map[string]string{"a.go": "stubbed out"},
		State:     "early",
	}
	b := StructuredSummary{
		Decisions: []string{"use structured summaries", "elide superseded reads"}, // first is a dup
		Files:     map[string]string{"a.go": "finalized", "b.go": "added"},        // a.go overrides
		State:     "mid",
	}
	m := MergeSummaries([]StructuredSummary{a, b})
	if m.Goal != "ship compaction" {
		t.Errorf("Goal = %q", m.Goal)
	}
	if len(m.Decisions) != 2 {
		t.Errorf("Decisions should dedupe to 2, got %v", m.Decisions)
	}
	if m.Files["a.go"] != "finalized" {
		t.Errorf("later Files value should win, got %q", m.Files["a.go"])
	}
	if m.State != "mid" {
		t.Errorf("State should be last non-empty, got %q", m.State)
	}
}

func TestRenderBlock_ContainsSections(t *testing.T) {
	s := StructuredSummary{Goal: "G", Decisions: []string{"D1"}, State: "S"}
	blk := s.RenderBlock()
	if !strings.Contains(blk.Text, "G") || !strings.Contains(blk.Text, "D1") || !strings.Contains(blk.Text, "S") {
		t.Errorf("rendered block missing content:\n%s", blk.Text)
	}
}
