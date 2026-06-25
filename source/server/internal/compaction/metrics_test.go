package compaction

import (
	"context"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func nopSummarize(_ context.Context, _ []llm.Message) (StructuredSummary, error) {
	return StructuredSummary{}, nil
}

func TestScore_ElisionBaselineOverCorpus(t *testing.T) {
	tok := contextmeter.Default()
	b := Budget{TargetTokens: 4000, VerbatimRecent: 4, SegmentTokens: 2000}
	for _, f := range Corpus() {
		m, err := Score(context.Background(), ElisionCompactor{}, f, nopSummarize, tok, b)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if !m.PairingValid {
			t.Errorf("%s: send-view not pairing-valid", f.Name)
		}
		if m.ModelCalls != 0 {
			t.Errorf("%s: elision baseline made %d model calls", f.Name, m.ModelCalls)
		}
		// Elision never touches prose, so every anchor must survive on the
		// fixtures whose anchors are not inside a superseded result.
		if f.Name != "repeated-reads" && m.AnchorsKept != m.AnchorsTotal {
			t.Errorf("%s: anchors %d/%d kept", f.Name, m.AnchorsKept, m.AnchorsTotal)
		}
	}
}

func TestScore_RepeatedReadsReclaimsTokens(t *testing.T) {
	tok := contextmeter.Default()
	m, err := Score(context.Background(), ElisionCompactor{},
		repeatedReadsFixture(), nopSummarize, tok, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if m.DedupCollapsed < 4 {
		t.Errorf("expected >=4 superseded reads stubbed, got %d", m.DedupCollapsed)
	}
	if m.Reduction <= 0 {
		t.Errorf("expected positive token reduction, got %f", m.Reduction)
	}
}
