package compaction

import (
	"context"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// ElisionFirstCompactor is the full deterministic contender: byte-identical
// dedup, then the lossy keep-last-K tool-result window, then (only if a
// target is set) dropping whole messages from the front until the view fits.
// Zero model calls, zero hallucination surface by construction; the open
// question it answers is whether mechanical compression alone reaches a
// usable reduction on real conversations. ElisionCompactor remains the
// lossless dedup-only floor.
type ElisionFirstCompactor struct {
	KeepLast int // tool results kept un-stubbed; 0 means DefaultLossyElisionKeepLast
	KeepHead int // leading messages protected from truncation; 0 means 2
}

func (ElisionFirstCompactor) Name() string { return "elision-first" }

func (c ElisionFirstCompactor) Compact(_ context.Context, raw []llm.Message, _ SummarizeFunc, b Budget) (Result, error) {
	msgs, _ := ElideSupersededToolResults(raw)

	keepLast := c.KeepLast
	if keepLast <= 0 {
		keepLast = DefaultLossyElisionKeepLast
	}
	msgs, _ = KeepLastNToolResults(msgs, keepLast)

	if b.TargetTokens > 0 {
		keepHead := c.KeepHead
		if keepHead <= 0 {
			keepHead = 2
		}
		msgs, _ = TruncateOldestToFit(msgs, contextmeter.Default(), b.TargetTokens, keepHead)
	}
	return Result{SendView: AssembleSendView(StructuredSummary{}, msgs)}, nil
}
