package compaction

import (
	"context"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// Metrics is one compactor's score on one fixture.
type Metrics struct {
	RawTokens      int
	SentTokens     int
	Reduction      float64 // 1 - SentTokens/RawTokens (0 when RawTokens == 0)
	AnchorsKept    int
	AnchorsTotal   int
	DedupCollapsed int  // count of elision stub markers in the send-view
	PairingValid   bool
	ModelCalls     int
}

const elisionStubMarker = "[elided: superseded result"

// Score runs c over fixture f and measures the result. It wraps summarize to
// count model calls, so the caller's SummarizeFunc need not track them.
func Score(ctx context.Context, c Compactor, f Fixture, summarize SummarizeFunc,
	tok contextmeter.Tokenizer, b Budget) (Metrics, error) {

	calls := 0
	counted := func(ctx context.Context, msgs []llm.Message) (StructuredSummary, error) {
		calls++
		return summarize(ctx, msgs)
	}

	res, err := c.Compact(ctx, f.Messages, counted, b)
	if err != nil {
		return Metrics{}, err
	}

	raw := TotalTokens(tok, f.Messages)
	sent := TotalTokens(tok, res.SendView)
	flat := flattenText(res.SendView)

	kept := 0
	for _, anchor := range f.MustKeep {
		if strings.Contains(flat, anchor) {
			kept++
		}
	}

	m := Metrics{
		RawTokens:      raw,
		SentTokens:     sent,
		AnchorsKept:    kept,
		AnchorsTotal:   len(f.MustKeep),
		DedupCollapsed: strings.Count(flat, elisionStubMarker),
		PairingValid:   llm.IsValidPairing(res.SendView),
		ModelCalls:     calls,
	}
	if raw > 0 {
		m.Reduction = 1 - float64(sent)/float64(raw)
	}
	return m, nil
}

func flattenText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
