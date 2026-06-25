package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// RollingCompactor folds the older history into a running summary, one segment
// at a time, carrying the prior summary forward. Sequential; exhibits
// compounding loss — the baseline the map-reduce contenders must beat.
type RollingCompactor struct{}

func (RollingCompactor) Name() string { return "rolling" }

func (RollingCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)
	older, recent := splitRecent(elided, b.VerbatimRecent)

	var sum StructuredSummary
	if len(older) > 0 {
		tok := contextmeter.Default()
		for _, seg := range SegmentByTokens(older, tok, segTokens(b)) {
			input := append(renderSummaryMessages(sum), seg.Messages...)
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		}
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}

// segTokens returns the per-segment token budget, defaulting when unset.
func segTokens(b Budget) int {
	if b.SegmentTokens > 0 {
		return b.SegmentTokens
	}
	return 32000
}
