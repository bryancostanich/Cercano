// Package compaction reduces a conversation's message history into a smaller,
// pairing-valid send-view, while keeping the original messages untouched. It is
// pure over its inputs: no DB, no goroutines, no live state. Candidate
// algorithms implement Compactor and are scored by the metrics harness.
package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

// StructuredSummary is the fixed-section reduction of a span of history.
// Structured (not prose) so merges are deterministic and degradation is bounded.
type StructuredSummary struct {
	Goal        string            // the session's objective
	Decisions   []string          // key decisions made (confirmed / applied)
	Proposals   []string          // designs, plans, or approaches proposed but not yet accepted or rejected
	Files       map[string]string // path -> latest known state/summary
	OpenThreads []string          // unresolved questions / next steps
	State       string            // one-line current state
}

// Segment is a contiguous, token-budgeted slice of the history.
type Segment struct {
	Messages []llm.Message
	Tokens   int
}

// Budget bounds a compaction.
type Budget struct {
	TargetTokens   int // desired max tokens for the assembled send-view
	VerbatimRecent int // number of trailing messages kept verbatim
	SegmentTokens  int // token budget per segment for summarization
}

// Result is what a Compactor produces.
type Result struct {
	SendView  []llm.Message       // assembled, pairing-valid array to send
	Summaries []StructuredSummary // summaries produced (for persistence/inspection)
}

// SummarizeFunc produces a StructuredSummary from a chunk of messages. Real
// implementations wrap the local model; the harness uses a deterministic fake.
type SummarizeFunc func(ctx context.Context, messages []llm.Message) (StructuredSummary, error)

// Compactor reduces raw messages into a send-view + summaries. Pure w.r.t. live
// state.
type Compactor interface {
	Name() string
	Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error)
}
