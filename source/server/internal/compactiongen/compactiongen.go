// Package compactiongen debounces per-conversation context compaction off the
// request path (mirrors the recap generator). It calls compactor.Advance and
// persists the derived state; failures log to stderr so a turn is never blocked.
package compactiongen

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
)

// Store is the subset of conversation.Store the generator needs.
type Store interface {
	GetTurns(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	GetCompaction(ctx context.Context, conversationID string) (conversation.Compaction, error)
	SaveCompaction(ctx context.Context, c conversation.Compaction) error
}

// runTimeout bounds one scheduled compaction pass. It must comfortably fit
// maxSegmentsPerPass summarizer calls at local-model speed (~40s each at 8k
// segment tokens) plus an occasional re-consolidation call — the previous
// 2-minute budget could expire mid-pass on every attempt. Advance now keeps
// partial progress on deadline expiry, so this is headroom, not a cliff.
const runTimeout = 6 * time.Minute

// Generator debounces compaction per conversation.
type Generator struct {
	store     Store
	summarize compaction.SummarizeFunc
	cfg       compactor.Config
	tok       contextmeter.Tokenizer
	debounce  time.Duration
	logf      func(string, ...any) // injectable for tests; defaults to stderr

	mu       sync.Mutex
	enabled  bool // guarded by mu — the runtime kill switch
	timers   map[string]*time.Timer
	inflight map[string]bool
}

func New(store Store, summarize compaction.SummarizeFunc, cfg compactor.Config, tok contextmeter.Tokenizer, debounce time.Duration) *Generator {
	return &Generator{
		store: store, summarize: summarize, cfg: cfg, tok: tok, debounce: debounce,
		logf:     func(f string, a ...any) { fmt.Fprintf(os.Stderr, f, a...) },
		timers:   make(map[string]*time.Timer),
		inflight: make(map[string]bool),
	}
}

// SetEnabled atomically flips the runtime kill switch. When disabled, Schedule
// noops and no compaction pass will start. In-flight passes complete
// normally. Called at startup with cfg.Compaction.Enabled and again from the
// server's UpdateConfig handler when the toggle changes at runtime.
func (g *Generator) SetEnabled(v bool) {
	g.mu.Lock()
	g.enabled = v
	g.mu.Unlock()
}

// Schedule requests a debounced compaction pass; rapid calls coalesce.
// Noops when the kill switch is off.
func (g *Generator) Schedule(conversationID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return
	}
	if t, ok := g.timers[conversationID]; ok {
		t.Reset(g.debounce)
		return
	}
	g.timers[conversationID] = time.AfterFunc(g.debounce, func() {
		g.mu.Lock()
		delete(g.timers, conversationID)
		g.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		_ = g.runCompaction(ctx, conversationID)
	})
}

// CompactNow runs a compaction pass synchronously (used by the request-path
// hard-limit override).
func (g *Generator) CompactNow(ctx context.Context, conversationID string) error {
	return g.runCompaction(ctx, conversationID)
}

// claim marks a conversation as having a pass in flight. It returns false when
// another pass (scheduled or an explicit Regenerate) already holds the claim,
// so two passes never interleave Advance/SaveCompaction on one conversation.
func (g *Generator) claim(conversationID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inflight[conversationID] {
		return false
	}
	g.inflight[conversationID] = true
	return true
}

func (g *Generator) release(conversationID string) {
	g.mu.Lock()
	delete(g.inflight, conversationID)
	g.mu.Unlock()
}

func (g *Generator) runCompaction(ctx context.Context, conversationID string) error {
	if !g.claim(conversationID) {
		// Another pass holds the conversation; reschedule rather than skip so
		// the debounced backlog isn't silently dropped.
		g.logf("[compaction] pass deferred %s: another pass is in flight\n", conversationID)
		g.Schedule(conversationID)
		return nil
	}
	defer g.release(conversationID)

	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil {
		g.logf("[compaction] pass FAILED %s: %v\n", conversationID, err)
		return err
	}
	if len(turns) == 0 {
		// Nothing to compact and no start line was emitted — stay silent.
		return nil
	}
	state, err := g.store.GetCompaction(ctx, conversationID)
	if err != nil {
		g.logf("[compaction] pass FAILED %s: %v\n", conversationID, err)
		return err
	}
	state.ConversationID = conversationID

	start := time.Now()
	preView, err := compactor.BuildSendView(turns, state)
	if err != nil {
		g.logf("[compaction] view build failed %s (pre-pass): %v\n", conversationID, err)
	}
	pre := compaction.TotalTokens(g.tok, preView)
	g.logf("[compaction] pass start %s: %d tokens\n", conversationID, pre)

	newState, changed, more, err := compactor.Advance(ctx, turns, state, g.summarize, g.cfg, g.tok)
	if err != nil {
		g.logf("[compaction] pass FAILED %s after %s: %v\n", conversationID, time.Since(start).Round(time.Millisecond), err)
		return err
	}
	if !changed {
		g.logf("[compaction] pass no-op %s: nothing to do\n", conversationID)
		return nil
	}
	if err := g.store.SaveCompaction(ctx, newState); err != nil {
		g.logf("[compaction] pass FAILED %s after %s: %v\n", conversationID, time.Since(start).Round(time.Millisecond), err)
		return err
	}
	postView, err := compactor.BuildSendView(turns, newState)
	if err != nil {
		g.logf("[compaction] view build failed %s (post-pass): %v\n", conversationID, err)
	}
	post := compaction.TotalTokens(g.tok, postView)
	g.logf("[compaction] pass ok %s: %d -> %d tokens in %s (more=%v)\n", conversationID, pre, post, time.Since(start).Round(time.Millisecond), more)
	if more {
		// Backlog remains; a bounded pass persisted its progress. Reschedule so
		// the next chunk runs after the debounce — the backlog converges one
		// bounded pass at a time.
		g.Schedule(conversationID)
	}
	return nil
}

// Regenerate runs compaction to completion for one conversation, persisting
// after every pass. With incremental=false the stored derived state is cleared
// (and the clear persisted) first — a full rebuild from the first raw turn.
// With incremental=true existing state is kept and only the backlog from
// FrozenThrough forward is digested (the /compact path). Raw turns are never
// modified — the worst a failed full rebuild can leave behind is a cleared
// state that the next run (or scheduled pass) rebuilds. progress (nil-safe)
// receives one human-readable line per step. Unlike Schedule this ignores the
// kill switch: it only ever runs as an explicit user action.
func (g *Generator) Regenerate(ctx context.Context, conversationID string, incremental bool, progress func(string)) (preTokens, postTokens int, err error) {
	if progress == nil {
		progress = func(string) {}
	}
	if !g.claim(conversationID) {
		return 0, 0, fmt.Errorf("a compaction pass is already running for %s — try again in a moment", conversationID)
	}
	defer g.release(conversationID)

	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil {
		return 0, 0, err
	}
	if len(turns) == 0 {
		return 0, 0, fmt.Errorf("conversation %s has no turns", conversationID)
	}
	state, err := g.store.GetCompaction(ctx, conversationID)
	if err != nil {
		return 0, 0, err
	}
	state.ConversationID = conversationID

	if preView, verr := compactor.BuildSendView(turns, state); verr == nil {
		preTokens = compaction.TotalTokens(g.tok, preView)
	}
	if incremental {
		progress(fmt.Sprintf("compacting backlog over %d raw turns (current view ~%d tokens)", len(turns), preTokens))
	} else {
		progress(fmt.Sprintf("rebuilding from %d raw turns (current view ~%d tokens)", len(turns), preTokens))
		state.FrozenThrough = 0
		state.SegmentSummariesJSON = ""
		state.ConsolidatedJSON = ""
		if err := g.store.SaveCompaction(ctx, state); err != nil {
			return preTokens, 0, fmt.Errorf("clear derived state: %w", err)
		}
	}

	pass := 0
	for {
		pass++
		start := time.Now()
		next, changed, more, err := compactor.Advance(ctx, turns, state, g.summarize, g.cfg, g.tok)
		if err != nil {
			return preTokens, 0, fmt.Errorf("pass %d: %w", pass, err)
		}
		if changed {
			if err := g.store.SaveCompaction(ctx, next); err != nil {
				return preTokens, 0, fmt.Errorf("persist pass %d: %w", pass, err)
			}
			state = next
		}
		postTokens = 0
		if view, verr := compactor.BuildSendView(turns, state); verr == nil {
			postTokens = compaction.TotalTokens(g.tok, view)
		}
		progress(fmt.Sprintf("pass %d: view ~%d tokens (%s)", pass, postTokens, time.Since(start).Round(time.Second)))
		if !more {
			return preTokens, postTokens, nil
		}
		if !changed {
			return preTokens, postTokens, fmt.Errorf("pass %d reported more work but made no progress — aborting rather than spinning", pass)
		}
	}
}

// Clear drops a conversation's derived compaction state entirely — no
// re-summarization — so the next send-view is rebuilt from the full raw turn
// history. This is the recovery path when the compacted layer is bad (e.g. a
// broken summarizer froze segments behind empty summaries): raw turns are the
// durable source of truth and are untouched. Like Regenerate it ignores the
// kill switch (explicit user action) and refuses while a pass is in flight so
// a concurrent Advance can't re-persist the state being cleared. progress
// (nil-safe) receives one human-readable line per step.
func (g *Generator) Clear(ctx context.Context, conversationID string, progress func(string)) (preTokens, postTokens int, err error) {
	if progress == nil {
		progress = func(string) {}
	}
	if !g.claim(conversationID) {
		return 0, 0, fmt.Errorf("a compaction pass is already running for %s — try again in a moment", conversationID)
	}
	defer g.release(conversationID)

	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil {
		return 0, 0, err
	}
	state, err := g.store.GetCompaction(ctx, conversationID)
	if err != nil {
		return 0, 0, err
	}
	if preView, verr := compactor.BuildSendView(turns, state); verr == nil {
		preTokens = compaction.TotalTokens(g.tok, preView)
	}
	progress(fmt.Sprintf("clearing compacted state over %d raw turns (current view ~%d tokens)", len(turns), preTokens))

	cleared := conversation.Compaction{ConversationID: conversationID}
	if err := g.store.SaveCompaction(ctx, cleared); err != nil {
		return preTokens, 0, fmt.Errorf("clear derived state: %w", err)
	}
	if view, verr := compactor.BuildSendView(turns, cleared); verr == nil {
		postTokens = compaction.TotalTokens(g.tok, view)
	}
	return preTokens, postTokens, nil
}

// IsCompacting reports whether a compaction pass is currently running for the
// conversation.
func (g *Generator) IsCompacting(conversationID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight[conversationID]
}
