package agent

import (
	"context"

	"cercano/source/server/internal/llm"
)

// LoopCompactor compacts a delegated tool loop's in-memory history between
// iterations. Sub-agent dispatches are ephemeral and never read their history
// back from the store, so the main loop's asynchronous, store-backed generator
// (debounced, minutes-long) cannot help them: a dispatch routinely finishes
// before a background pass would even start. This seam runs the SAME
// production compaction algorithm synchronously, inside the loop.
//
// It is a callback rather than a direct call because internal/compactor
// imports internal/agent (for BuildLLMHistory); the concrete implementation is
// wired where both packages are already visible.
//
// Contract for implementations:
//   - Return the history to send from now on. Returning the input unchanged is
//     always valid and is the correct response below the activation floor.
//   - The result MUST be pairing-valid (every tool_use has its tool_result):
//     use llm.RepairPairing. A broken pair corrupts the rest of the dispatch.
//   - Errors are advisory. The loop keeps the uncompacted history and
//     continues: compaction is an optimization, never a reason to fail work
//     that is already paid for.
//   - Summarizer calls made here bill real tokens; implementations that spend
//     must report it so the dispatch budget stays honest (see SpentTokens).
type LoopCompactor interface {
	// CompactLoopHistory returns the (possibly compacted) history plus the
	// tokens the compaction itself billed, if any.
	CompactLoopHistory(ctx context.Context, history []llm.Message) (out []llm.Message, spent int, err error)
}

// LoopCompactorFunc adapts a function to LoopCompactor.
type LoopCompactorFunc func(ctx context.Context, history []llm.Message) ([]llm.Message, int, error)

func (f LoopCompactorFunc) CompactLoopHistory(ctx context.Context, history []llm.Message) ([]llm.Message, int, error) {
	return f(ctx, history)
}

// LoopCompactionScope carries the dispatch correlation an inline compaction
// pass should report with its telemetry: which conversation's sub-agent, at
// which tool-loop iteration, the pass ran. Values only — no content.
type LoopCompactionScope struct {
	ConversationID string
	Iteration      int
}

type loopCompactionScopeKey struct{}

// WithLoopCompactionScope stamps dispatch correlation onto a context so a
// LoopCompactor implementation can attribute its telemetry without the seam
// itself carrying logging concerns.
func WithLoopCompactionScope(ctx context.Context, s LoopCompactionScope) context.Context {
	return context.WithValue(ctx, loopCompactionScopeKey{}, s)
}

// LoopCompactionScopeFrom reads the stamped correlation; ok is false when the
// caller (tests, direct seam use) did not stamp one.
func LoopCompactionScopeFrom(ctx context.Context) (LoopCompactionScope, bool) {
	s, ok := ctx.Value(loopCompactionScopeKey{}).(LoopCompactionScope)
	return s, ok
}

// compactLoopHistory applies the compactor defensively: any error, nil result,
// or empty result leaves the history untouched. Compaction must never be able
// to destroy a dispatch's working context.
func compactLoopHistory(ctx context.Context, c LoopCompactor, history []llm.Message, budget *TokenBudget) []llm.Message {
	if c == nil || len(history) == 0 {
		return history
	}
	out, spent, err := c.CompactLoopHistory(ctx, history)
	// Spend counts even on failure: a summarizer call that errored after
	// billing still cost money, and hiding it would understate the budget.
	if spent > 0 && budget != nil {
		budget.Add(spent, 0)
	}
	if err != nil || len(out) == 0 {
		return history
	}
	return llm.RepairPairing(out)
}
