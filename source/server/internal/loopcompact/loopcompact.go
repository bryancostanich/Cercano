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
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// Summarize is the summarizer seam, matching compaction.SummarizeFunc. In
// production this is the same local fast_light_text summarizer the main loop
// uses, so compacting a cloud sub-agent's history stays local work.
type Summarize = compaction.SummarizeFunc

// Compactor implements agent.LoopCompactor over compactor.Advance.
//
// It is stateful across iterations of ONE dispatch: frozen segment summaries
// are reused rather than recomputed, exactly as the stored Compaction row does
// for main turns. Not safe for concurrent use by multiple dispatches; each
// dispatch constructs its own.
type Compactor struct {
	cfg       compactor.Config
	summarize Summarize
	tok       contextmeter.Tokenizer
	timeout   time.Duration

	// state carries frozen boundaries and summaries between passes. It is the
	// in-memory analogue of the persisted conversation.Compaction row.
	state conversation.Compaction
	// synthetic monotonic clock for turn timestamps; see toTurns.
	seq int64
}

// Options configures a loop compactor. Zero values fall back to production
// defaults so callers cannot accidentally construct a disabled compactor.
type Options struct {
	Config    compactor.Config
	Summarize Summarize
	Tokenizer contextmeter.Tokenizer
	// Timeout bounds one compaction pass. Unlike the background generator's
	// multi-minute budget, an inline pass blocks the dispatch, so it must be
	// short: a slow summarizer should cost a little latency, never a stall.
	Timeout time.Duration
}

// DefaultTimeout bounds one inline pass.
const DefaultTimeout = 90 * time.Second

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
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Compactor{cfg: cfg, summarize: opts.Summarize, tok: tok, timeout: timeout}
}

// CompactLoopHistory implements agent.LoopCompactor.
//
// Returns the history unchanged whenever Advance declines (below the
// activation floor — the common case for short dispatches), and reports the
// tokens the summarizer billed so the dispatch budget stays honest.
func (c *Compactor) CompactLoopHistory(ctx context.Context, history []llm.Message) ([]llm.Message, int, error) {
	if c == nil || len(history) == 0 {
		return history, 0, nil
	}
	// Cheap pre-gate: Advance applies the same floor, but this avoids building
	// synthetic turns on every iteration of a small dispatch.
	if compaction.TotalTokens(c.tok, history) < c.cfg.ActivationFloorTokens {
		return history, 0, nil
	}
	turns := c.toTurns(history)

	spent := 0
	// Wrap the summarizer to attribute its spend to the dispatch budget.
	metered := func(ctx context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		summary, err := c.summarize(ctx, msgs)
		// Conservative attribution: the summarizer seam reports no usage, so
		// charge the estimated input it consumed. Undercounting spend would
		// let compaction quietly erode the dispatch budget's guarantee.
		spent += compaction.TotalTokens(c.tok, msgs)
		return summary, err
	}

	passCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	state, changed, _, err := compactor.Advance(passCtx, turns, c.state, metered, c.cfg, c.tok)
	if err != nil {
		return history, spent, fmt.Errorf("loop compaction pass: %w", err)
	}
	if !changed {
		return history, spent, nil
	}
	c.state = state

	view, err := compactor.BuildSendView(turns, state)
	if err != nil {
		return history, spent, fmt.Errorf("loop compaction send view: %w", err)
	}
	if len(view) == 0 {
		// Never hand back an empty view; the caller would discard it anyway.
		return history, spent, nil
	}
	return view, spent, nil
}

// toTurns adapts in-memory messages to the conversation.Turn shape Advance
// expects. Timestamps are SYNTHETIC and monotonic (one second apart), not wall
// clock: Advance uses CreatedAt only to order turns and to place the frozen
// boundary, and it refuses to freeze turns sharing the boundary second. Real
// wall-clock stamps would cluster many turns into one second (tool bursts
// persist fast) and stall the boundary; a synthetic clock keeps every turn
// separable. The sequence is stable across passes because index i always maps
// to timestamp i.
func (c *Compactor) toTurns(msgs []llm.Message) []conversation.Turn {
	turns := make([]conversation.Turn, 0, len(msgs))
	base := time.Unix(0, 0).UTC()
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
