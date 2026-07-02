package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// MapReduceCompactor summarizes each older segment from raw (no compounding),
// then reduces the segment summaries via the deterministic MergeSummaries. A
// prior ModelReduce mode was removed after the second model pass was shown to
// fabricate content (see reduce.go).
type MapReduceCompactor struct{}

func (MapReduceCompactor) Name() string { return "map-reduce" }

func (MapReduceCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)
	older, recent := splitRecent(elided, b.VerbatimRecent)

	var sum StructuredSummary
	if len(older) > 0 {
		tok := contextmeter.Default()
		var parts []StructuredSummary
		for _, seg := range SegmentByTokens(older, tok, segTokens(b)) {
			s, err := summarize(ctx, seg.Messages)
			if err != nil {
				return Result{}, err
			}
			parts = append(parts, s)
		}
		sum = MergeSummaries(parts)
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}
