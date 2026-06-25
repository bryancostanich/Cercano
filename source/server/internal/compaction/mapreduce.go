package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// MapReduceCompactor summarizes each older segment from raw (no compounding),
// then reduces the segment summaries — mechanically (MergeSummaries) or via a
// second model pass (ModelReduce).
type MapReduceCompactor struct {
	ModelReduce bool
}

func (c MapReduceCompactor) Name() string {
	if c.ModelReduce {
		return "map-reduce/model"
	}
	return "map-reduce/mechanical"
}

func (c MapReduceCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
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
		if c.ModelReduce && len(parts) > 1 {
			// Reduce pass: hand the rendered segment summaries back to the model.
			var input []llm.Message
			for _, p := range parts {
				input = append(input, renderSummaryMessages(p)...)
			}
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		} else {
			sum = MergeSummaries(parts)
		}
	}
	return Result{SendView: AssembleSendView(sum, recent), Summaries: []StructuredSummary{sum}}, nil
}
