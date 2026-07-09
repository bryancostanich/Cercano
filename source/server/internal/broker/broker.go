// Package broker owns the per-conversation turn-exclusivity registry.
// It enforces that at most one turn is live per conversation: a new turn
// supersedes (cancels) any prior turn on the same conversation and advances the
// per-conversation generation counter so the superseded turn's persistence and
// event emission are fenced out.
//
// broker is proto-free and does not import internal/server.
package broker

import (
	"context"
	"sync"
)

// turnHandle tracks one in-flight turn for a conversation. gen is the
// conversation's turn generation at the moment this turn began; a turn is
// "current" only while active[conv] still points at this handle.
type turnHandle struct {
	gen    uint64
	cancel context.CancelFunc
}

// Broker is the per-conversation turn-exclusivity registry.
// The zero value is not usable; call New.
type Broker struct {
	mu     sync.Mutex
	active map[string]*turnHandle
	gens   map[string]uint64 // per-conversation turn generation (monotonic)
}

// New returns a ready-to-use Broker.
func New() *Broker {
	return &Broker{
		active: make(map[string]*turnHandle),
		gens:   make(map[string]uint64),
	}
}

// BeginTurn registers a new turn for conv, superseding any turn already running
// there (cancels its ctx). Returns a ctx that is canceled when this turn is
// itself superseded or when parent is done, this turn's generation, and a
// release func the caller must defer. The fence helpers (IsCurrent) gate
// persistence/emission on the returned gen.
func (b *Broker) BeginTurn(parent context.Context, conv string) (context.Context, uint64, func()) {
	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	if prev, ok := b.active[conv]; ok {
		prev.cancel() // supersede the turn already running on this conversation
	}
	h := &turnHandle{gen: b.genLocked(conv) + 1, cancel: cancel}
	b.gens[conv] = h.gen
	b.active[conv] = h
	b.mu.Unlock()

	release := func() {
		cancel()
		b.mu.Lock()
		// Only clear the registration if it's still ours — a superseding turn
		// may have replaced it, and must not have its handle removed by us.
		if cur, ok := b.active[conv]; ok && cur == h {
			delete(b.active, conv)
		}
		b.mu.Unlock()
	}
	return ctx, h.gen, release
}

// genLocked returns the current generation for conv. Caller holds b.mu.
func (b *Broker) genLocked(conv string) uint64 {
	return b.gens[conv]
}

// IsCurrent reports whether gen is still the live generation for conv — the
// fence that gates a turn's persistence and event emission. A superseded turn
// fails this and goes quiet.
func (b *Broker) IsCurrent(conv string, gen uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gens[conv] == gen
}

// HasActiveTurn reports whether a turn is currently registered for conv (test
// seam for the release-on-return invariant).
func (b *Broker) HasActiveTurn(conv string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[conv]
	return ok
}
