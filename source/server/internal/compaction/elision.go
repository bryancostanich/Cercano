package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// ElisionCompactor is the deterministic baseline: it makes no model calls. It
// only collapses superseded duplicate tool results, then sends the result
// verbatim. It is the floor of the bake-off — how much can mechanical dedup
// reclaim with zero summarization and zero information loss to prose?
type ElisionCompactor struct{}

func (ElisionCompactor) Name() string { return "elision" }

func (ElisionCompactor) Compact(_ context.Context, raw []llm.Message, _ SummarizeFunc, _ Budget) (Result, error) {
	deduped, _ := ElideSupersededToolResults(raw)
	return Result{SendView: AssembleSendView(StructuredSummary{}, deduped)}, nil
}
