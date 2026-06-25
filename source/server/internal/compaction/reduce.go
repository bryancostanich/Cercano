package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// Reduce reconciles segment summaries into one. With more than one part it runs
// a model reduce pass over the rendered parts (C's reduce step); with one (or
// zero) it falls back to the deterministic MergeSummaries.
func Reduce(ctx context.Context, parts []StructuredSummary, summarize SummarizeFunc) (StructuredSummary, error) {
	if len(parts) > 1 {
		var input []llm.Message
		for _, p := range parts {
			input = append(input, renderSummaryMessages(p)...)
		}
		return summarize(ctx, input)
	}
	return MergeSummaries(parts), nil
}
