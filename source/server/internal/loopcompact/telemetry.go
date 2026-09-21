package loopcompact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/llm"
)

// Pass outcome codes for PassEvent.Outcome. Machine-readable: telemetry
// consumers (and tests) must never have to parse prose.
const (
	// OutcomeBelowFloor: the estimated history stayed under the activation
	// floor — the common case for short dispatches; nothing ran.
	OutcomeBelowFloor = "below_threshold"
	// OutcomeUnchanged: a pass ran but the algorithm declined to freeze
	// anything (nothing past the verbatim window, cadence gate, empty view).
	OutcomeUnchanged = "unchanged"
	// OutcomeCompacted: a pass froze new segments and the returned history is
	// the reduced send-view.
	OutcomeCompacted = "compacted"
	// OutcomeFailed: the pass errored. NONFATAL by contract — the caller
	// keeps the uncompacted history and the dispatch continues; the failure is
	// observable only through this event.
	OutcomeFailed = "failed"
)

// PassEvent is metadata-only telemetry for one inline compaction attempt in one
// dispatch iteration. It carries counts, thresholds and outcome codes — never
// prompt, code, tool-result or summarizer content, and never a raw (potentially
// content-bearing) error message; Reason is a stable classifier code.
type PassEvent struct {
	// Correlation, supplied by the tool loop via agent.WithLoopCompactionScope.
	ConversationID string
	Iteration      int

	// Configured status: the compactor exists, so compaction is enabled; the
	// effective thresholds follow (post-defaulting, i.e. what actually gates).
	Enabled               bool
	ActivationFloorTokens int
	SegmentTokens         int
	VerbatimRecent        int
	CompactedBudgetTokens int

	// History shape before/after the pass. Token counts are ESTIMATES from the
	// context-meter tokenizer, not provider-reported usage.
	HistoryMessagesBefore int
	HistoryMessagesAfter  int
	EstimatedTokensBefore int
	EstimatedTokensAfter  int
	ToolResultCharsBefore int
	ToolResultCharsAfter  int
	ToolResultsBefore     int
	ToolResultsAfter      int

	// Outcome and machine-readable reason code (see classifyFailure).
	Outcome string
	Reason  string

	// SummarizerCalls counts summarizer seam invocations this pass. On
	// unchanged/below-floor passes it is 0.
	SummarizerCalls int
	// SpentTokensEstimated is the estimated input the summarizer consumed — an
	// ESTIMATE, not provider usage; the seam reports no usage numbers.
	SpentTokensEstimated int

	Duration time.Duration
}

// FormatPassEvent renders one metadata-only log line. Keys are stable and
// grep-friendly; values are counts, ids and codes only.
func FormatPassEvent(ev PassEvent) string {
	return fmt.Sprintf("[loop-compaction] pass: conv=%s iter=%d enabled=%t outcome=%s reason=%s "+
		"activation_floor_tokens=%d segment_tokens=%d verbatim_recent=%d compacted_budget_tokens=%d "+
		"history_messages=%d->%d est_tokens=%d->%d tool_result_chars=%d->%d tool_results=%d->%d "+
		"summarizer_calls=%d spent_tokens_est=%d duration_ms=%d",
		ev.ConversationID, ev.Iteration, ev.Enabled, ev.Outcome, ev.Reason,
		ev.ActivationFloorTokens, ev.SegmentTokens, ev.VerbatimRecent, ev.CompactedBudgetTokens,
		ev.HistoryMessagesBefore, ev.HistoryMessagesAfter, ev.EstimatedTokensBefore, ev.EstimatedTokensAfter,
		ev.ToolResultCharsBefore, ev.ToolResultCharsAfter, ev.ToolResultsBefore, ev.ToolResultsAfter,
		ev.SummarizerCalls, ev.SpentTokensEstimated, ev.Duration.Milliseconds())
}

// classifyFailure maps a pass error to a stable, content-free reason code.
// Raw error text is deliberately dropped: provider and runtime errors can
// echo conversation or tool content, which must never reach a log.
func classifyFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var deferral *compaction.DeferralError
	if errors.As(err, &deferral) {
		return "deferred_segment"
	}
	var startup *llm.LocalStartupError
	if errors.As(err, &startup) {
		return "local_runtime_unavailable"
	}
	return "summarizer_error"
}

// toolResultChars sums the character counts of tool_result block bodies — a
// size signal for the bulk of dispatch history, without touching the content.
func toolResultChars(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				n += len(b.Content)
			}
		}
	}
	return n
}

// scopeFrom reads the dispatch/iteration correlation the tool loop stamped on
// the context. Absent scope yields zero values (e.g. direct seam callers).
func scopeFrom(ctx context.Context) (conversationID string, iteration int) {
	if s, ok := agent.LoopCompactionScopeFrom(ctx); ok {
		return s.ConversationID, s.Iteration
	}
	return "", 0
}

// Reason codes shared by the pass emit sites. Stable, machine-readable.
const (
	reasonBelowFloor = "below_activation_floor"
	reasonNoProgress = "no_eligible_progress"
	reasonEmptyView  = "empty_send_view"
)

// emitPass reports the event to the installed sink, if any.
func (c *Compactor) emitPass(ev PassEvent) {
	if c.onPass != nil {
		c.onPass(ev)
	}
}

func toolResultCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				n++
			}
		}
	}
	return n
}
