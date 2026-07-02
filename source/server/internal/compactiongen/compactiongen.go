// Package compactiongen debounces per-conversation context compaction off the
// request path (mirrors the recap generator). It calls compactor.Advance and
// persists the derived state; failures are swallowed so a turn is never blocked.
package compactiongen

import (
	"context"
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

const runTimeout = 2 * time.Minute

// Generator debounces compaction per conversation.
type Generator struct {
	store     Store
	summarize compaction.SummarizeFunc
	cfg       compactor.Config
	tok       contextmeter.Tokenizer
	debounce  time.Duration

	mu       sync.Mutex
	enabled  bool // guarded by mu — the runtime kill switch
	timers   map[string]*time.Timer
	inflight map[string]bool
}

func New(store Store, summarize compaction.SummarizeFunc, cfg compactor.Config, tok contextmeter.Tokenizer, debounce time.Duration) *Generator {
	return &Generator{
		store: store, summarize: summarize, cfg: cfg, tok: tok, debounce: debounce,
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

func (g *Generator) runCompaction(ctx context.Context, conversationID string) error {
	g.mu.Lock()
	g.inflight[conversationID] = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.inflight, conversationID)
		g.mu.Unlock()
	}()

	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil || len(turns) == 0 {
		return err
	}
	state, err := g.store.GetCompaction(ctx, conversationID)
	if err != nil {
		return err
	}
	state.ConversationID = conversationID
	newState, changed, err := compactor.Advance(ctx, turns, state, g.summarize, g.cfg, g.tok)
	if err != nil || !changed {
		return err
	}
	return g.store.SaveCompaction(ctx, newState)
}

// IsCompacting reports whether a compaction pass is currently running for the
// conversation.
func (g *Generator) IsCompacting(conversationID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight[conversationID]
}
