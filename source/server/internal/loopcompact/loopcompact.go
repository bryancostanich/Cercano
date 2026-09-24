// Package loopcompact runs the production compaction algorithm synchronously
// inside an ephemeral tool loop (sub-agent dispatches).
//
// Main turns compact asynchronously: turns live in the conversation store and
// compactiongen.Generator runs compactor.Advance in the background, debounced
// by ~10s and bounded by a multi-minute timeout. Sub-agent dispatches cannot
// use that path — their history is in memory only, is never read back, and a
// dispatch frequently finishes before a debounced background pass would begin.
//
// This package therefore reuses the SAME algorithm (compactor.Advance), the
// SAME configuration (compactor.Config from cfg.Compaction) and the SAME
// summarizer seam, and only changes WHEN it runs: inline, between iterations,
// once the history crosses the activation floor.
package loopcompact

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// Summarize is the summarizer seam, matching compaction.SummarizeFunc. Both
// main and dispatch compaction use the Compaction task route.
type Summarize = compaction.SummarizeFunc

// Compactor implements agent.LoopCompactor over compactor.Advance.
//
// It is stateful across iterations of ONE dispatch: frozen segment summaries
// are reused rather than recomputed, exactly as the stored Compaction row does
// for main turns. Not safe for concurrent use by multiple dispatches; each
// dispatch constructs its own.
type Compactor struct {
	guard            *compaction.SummaryGuard
	cfg              compactor.Config
	summarize        Summarize
	tok              contextmeter.Tokenizer
	timeout          time.Duration
	contextBudgetPct float64
	fallbackBudget   int
	// onPass receives metadata-only telemetry for every attempt; nil disables.
	onPass func(PassEvent)

	// state carries frozen boundaries and summaries between passes. It is the
	// in-memory analogue of the persisted conversation.Compaction row.
	state     conversation.Compaction
	lastUsage compaction.SummaryUsage
}

// Options configures a loop compactor. Zero values fall back to production
// defaults so callers cannot accidentally construct a disabled compactor.
type Options struct {
	Config    compactor.Config
	Summarize Summarize
	Tokenizer contextmeter.Tokenizer
	// ContextBudgetPct enables sizing from the executing model in the loop scope.
	ContextBudgetPct float64
	// Timeout optionally tightens the shared compaction execution budget.
	Timeout time.Duration
	// OnPass receives metadata-only telemetry for every compaction attempt
	// (below-floor no-ops included). Nil disables telemetry.
	OnPass func(PassEvent)
}

// DefaultTimeout bounds one inline pass.
const DefaultTimeout = compaction.ExecutionTimeout

// New builds a loop compactor. It returns nil when no summarizer is available,
// so callers can wire unconditionally and simply get no compaction when the
// environment cannot summarize (worker without a local runtime, tests, etc.).
func New(opts Options) *Compactor {
	if opts.Summarize == nil {
		return nil
	}
	cfg := opts.Config
	if cfg.ActivationFloorTokens <= 0 || cfg.SegmentTokens <= 0 || cfg.VerbatimRecent <= 0 {
		def := compactor.DefaultConfig()
		if cfg.ActivationFloorTokens <= 0 {
			cfg.ActivationFloorTokens = def.ActivationFloorTokens
		}
		if cfg.SegmentTokens <= 0 {
			cfg.SegmentTokens = def.SegmentTokens
		}
		if cfg.VerbatimRecent <= 0 {
			cfg.VerbatimRecent = def.VerbatimRecent
		}
	}
	tok := opts.Tokenizer
	if tok == nil {
		tok = contextmeter.Default()
	}
	timeout := opts.Timeout
	if timeout <= 0 || timeout > DefaultTimeout {
		timeout = DefaultTimeout
	}
	return &Compactor{guard: compaction.NewSummaryGuard(2), cfg: cfg, summarize: opts.Summarize, tok: tok, timeout: timeout, onPass: opts.OnPass, contextBudgetPct: opts.ContextBudgetPct, fallbackBudget: cfg.CompactedBudgetTokens}
}

// CompactLoopHistory implements agent.LoopCompactor.
//
// Returns the history unchanged whenever Advance declines (below the
// activation floor — the common case for short dispatches), and reports the
// summarizer token volume, reported where available and otherwise estimated.
func (c *Compactor) CompactLoopHistory(ctx context.Context, history []llm.Message) ([]llm.Message, int, error) {
	if c == nil || len(history) == 0 {
		return history, 0, nil
	}
	c.lastUsage = compaction.SummaryUsage{}
	// A dispatch's context is not the summarizer's context, nor the local chat
	// model's. The loop refreshes this scope when its serving route changes.
	if c.contextBudgetPct > 0 {
		c.cfg.CompactedBudgetTokens = c.fallbackBudget
	}
	if scope, ok := agent.LoopCompactionScopeFrom(ctx); ok && scope.ContextWindowKnown && scope.ContextWindow > 0 && c.contextBudgetPct > 0 {
		c.cfg.CompactedBudgetTokens = summaryBudget(scope.ContextWindow, c.contextBudgetPct)
	}
	// Metadata-only telemetry: counts, thresholds and outcome codes for every
	// attempt, correlated to the dispatch via the scope the tool loop stamped.
	started := time.Now()
	ev := PassEvent{
		Enabled:               true,
		ActivationFloorTokens: c.cfg.ActivationFloorTokens,
		SegmentTokens:         c.cfg.SegmentTokens,
		VerbatimRecent:        c.cfg.VerbatimRecent,
		CompactedBudgetTokens: c.cfg.CompactedBudgetTokens,
		HistoryMessagesBefore: len(history),
		EstimatedTokensBefore: compaction.TotalTokens(c.tok, history),
		ToolResultCharsBefore: toolResultChars(history),
		ToolResultsBefore:     toolResultCount(history),
	}
	ev.ConversationID, ev.Iteration = scopeFrom(ctx)

	out, spent := history, 0
	defer func() {
		ev.HistoryMessagesAfter = len(out)
		ev.EstimatedTokensAfter = compaction.TotalTokens(c.tok, out)
		ev.ToolResultCharsAfter = toolResultChars(out)
		ev.ToolResultsAfter = toolResultCount(out)
		ev.SpentTokensEstimated = c.lastUsage.Estimated()
		ev.SpentTokensReported = c.lastUsage.Reported()
		ev.Duration = time.Since(started)
		c.emitPass(ev)
	}()

	// Cheap pre-gate: Advance applies the same floor, but this avoids building
	// synthetic turns on every iteration of a small dispatch.
	if ev.EstimatedTokensBefore < c.cfg.ActivationFloorTokens {
		ev.Outcome = OutcomeBelowFloor
		ev.Reason = reasonBelowFloor
		return history, 0, nil
	}
	turns := c.toTurns(history)

	// Production records every provider call (including chunks) in this meter.
	// Legacy/test seams without usage retain an explicitly labeled estimate.
	ctx, meter := compaction.WithUsageMeter(ctx)
	metered := func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		ev.SummarizerCalls++
		before := meter.Snapshot()
		summary, err := c.guard.Summarize(ctx, msgs, c.summarize)
		after := meter.Snapshot()
		if after.Observations == before.Observations {
			compaction.RecordSummaryUsage(ctx, compaction.SummaryUsage{EstimatedInput: compaction.TotalTokens(c.tok, msgs), Calls: 1})
			after = meter.Snapshot()
		}
		c.lastUsage = after
		spent = after.Total()
		return summary, err
	}

	passCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	state, changed, _, err := compactor.Advance(passCtx, turns, c.state, metered, c.cfg, c.tok)
	if err != nil {
		// NONFATAL by contract: the history is preserved (below) and the
		// dispatch continues; the failure is observable only via the event.
		ev.Outcome = OutcomeFailed
		ev.Reason = classifyFailure(err)
		return history, spent, fmt.Errorf("loop compaction pass: %w", err)
	}
	if !changed {
		ev.Outcome = OutcomeUnchanged
		ev.Reason = reasonNoProgress
		return history, spent, nil
	}
	view, err := compactor.BuildSendView(turns, state)
	if err != nil {
		ev.Outcome = OutcomeFailed
		ev.Reason = classifyFailure(err)
		return history, spent, fmt.Errorf("loop compaction send view: %w", err)
	}
	if len(view) == 0 {
		// Never hand back an empty view; the caller would discard it anyway.
		ev.Outcome = OutcomeUnchanged
		ev.Reason = reasonEmptyView
		return history, spent, nil
	}
	c.state = state
	out = view
	ev.Outcome = OutcomeCompacted
	return view, spent, nil
}

// toTurns adapts in-memory messages to the conversation.Turn shape Advance
// expects. Timestamps are SYNTHETIC and monotonic (one second apart), not wall
// clock: Advance uses CreatedAt only to order turns and to place the frozen
// boundary, and it refuses to freeze turns sharing the boundary second. Real
// wall-clock stamps would cluster many turns into one second (tool bursts
// persist fast) and stall the boundary; a synthetic clock keeps every turn
// separable. Reduced views are rebased at the existing frozen boundary.
func (c *Compactor) toTurns(msgs []llm.Message) []conversation.Turn {
	turns := make([]conversation.Turn, 0, len(msgs))
	// Zero is the initial frozen boundary: the first real message must be
	// strictly after it. When the loop feeds back a reduced view, its summary
	// represents the frozen prefix. Anchor that preamble at the boundary and
	// renumber its live tail AFTER it, rather than reusing old array indices
	// that would cause unsummarized messages to fall behind FrozenThrough.
	base := time.Unix(1, 0).UTC()
	if len(msgs) > 0 && c.state.ConsolidatedJSON != "" {
		var summary compaction.StructuredSummary
		if json.Unmarshal([]byte(c.state.ConsolidatedJSON), &summary) == nil && !summary.IsEmpty() && reflect.DeepEqual(msgs[0].Blocks, []llm.Block{summary.RenderBlock()}) {
			base = time.Unix(c.state.FrozenThrough, 0).UTC()
		}
	}
	for i, m := range msgs {
		t := conversation.Turn{
			Role:      string(m.Role),
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		// Mirror agent.BuildLLMHistory's inverse: blocks ride BlocksJSON, and
		// plain text also fills Content so a text-only turn survives a
		// marshalling failure.
		if blocks, err := json.Marshal(m.Blocks); err == nil {
			t.BlocksJSON = string(blocks)
		}
		if len(m.Blocks) == 1 && m.Blocks[0].Type == llm.BlockText {
			t.Content = m.Blocks[0].Text
		}
		turns = append(turns, t)
	}
	return turns
}

// Ensure the concrete type satisfies the agent-side seam.
var _ agent.LoopCompactor = (*Compactor)(nil)

// LastCompactionUsage exposes source attribution without changing the legacy
// agent seam's total-token return. Valid only for this instance's latest pass.
func (c *Compactor) LastCompactionUsage() (reported, estimated int) {
	return c.lastUsage.Reported(), c.lastUsage.Estimated()
}
