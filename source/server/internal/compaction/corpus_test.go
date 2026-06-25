package compaction

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestCorpus_FixturesValid(t *testing.T) {
	c := Corpus()
	if len(c) < 3 {
		t.Fatalf("expected at least 3 fixtures, got %d", len(c))
	}
	for _, f := range c {
		if f.Name == "" || len(f.Messages) == 0 || len(f.MustKeep) == 0 {
			t.Errorf("fixture %q is underspecified", f.Name)
		}
		if !llm.IsValidPairing(f.Messages) {
			t.Errorf("fixture %q is not pairing-valid", f.Name)
		}
	}
}
