package worker

// pool.go — per-conversation warm worker pool (Phase 6, Task B1).
//
// Phase 5 spawned a fresh `cercano worker` process PER TURN and killed it on
// return, paying spawn + provider-build cost every message. The pool keeps a
// conversation's worker WARM between turns: a follow-up turn on the same
// conversation reuses the already-running process (and its warm gRPC conn +
// provider connections) instead of spawning.
//
// Lifecycle (design-doc intent: one worker per conversation):
//   - Acquire(convID): reuse a cached, healthy, not-in-use worker for convID,
//     else evict any stale entry + spawn a fresh one, cache it in-use, return.
//   - Release(convID, healthy): healthy → mark not-in-use (keep WARM for the
//     next turn); unhealthy (crash) → kill + remove (next Acquire spawns fresh).
//   - Shutdown(): kill every cached worker + clear (host-shutdown drain, B5).
//
// Concurrency: Phase-4 turn-exclusivity guarantees a conversation is used by at
// most one live turn at a time, so an entry is Acquired/Released by ≤1 turn
// concurrently. The mutex still guards the map + the inUse flag so different
// conversations (and Shutdown) don't race.
//
// The gRPC conn lives on the pooled workerHandle and is REUSED across turns —
// each turn opens a fresh RunTurn STREAM on the same conn. The worker binary's
// grpc.Serve loop (cmd/cercano/main.go:runWorkerMode) handles repeated
// sequential RunTurn streams on one connection; its RunTurn handler returns
// per-turn and can be re-invoked, so the process does NOT exit after one turn.

import (
	"context"
	"sync"
	"syscall"
)

// spawnFunc spawns a worker process and returns a connected handle. It is the
// pool's injectable seam: production uses spawnWorker; tests inject a seam that
// returns bufconn-backed handles and counts spawns.
type spawnFunc func(ctx context.Context, convID string, gen uint64) (*workerHandle, error)

// pooledEntry is one conversation's cached worker.
type pooledEntry struct {
	handle *workerHandle
	inUse  bool
}

// workerPool keeps at most one warm worker per conversation.
type workerPool struct {
	mu     sync.Mutex
	byConv map[string]*pooledEntry
	spawn  spawnFunc
}

// newWorkerPool builds a pool that spawns via the given spawnFunc (defaults to
// spawnWorker when nil).
func newWorkerPool(spawn spawnFunc) *workerPool {
	if spawn == nil {
		spawn = spawnWorker
	}
	return &workerPool{
		byConv: make(map[string]*pooledEntry),
		spawn:  spawn,
	}
}

// Acquire returns a warm worker for convID, reusing a cached healthy one or
// spawning a fresh one. The returned handle is marked in-use; the caller MUST
// call Release(convID, ...) when the turn ends.
func (p *workerPool) Acquire(ctx context.Context, convID string, gen uint64) (*workerHandle, error) {
	p.mu.Lock()
	if e, ok := p.byConv[convID]; ok {
		// A cached entry exists. Reuse it only if it's healthy and not already
		// held by a concurrent turn (shouldn't happen under turn-exclusivity,
		// but guard it anyway).
		if !e.inUse && workerHealthy(e.handle) {
			e.inUse = true
			h := e.handle
			p.mu.Unlock()
			return h, nil // WARM REUSE — no spawn.
		}
		// Stale/unhealthy (or, defensively, in-use): evict + kill it so we
		// spawn a clean one below. (In-use is not expected under exclusivity;
		// if it ever happens we prefer a fresh process over sharing.)
		if !e.inUse {
			delete(p.byConv, convID)
			stale := e.handle
			p.mu.Unlock()
			stale.Kill()
			p.mu.Lock()
		}
	}
	p.mu.Unlock()

	// No reusable entry — spawn a fresh worker (outside the lock; spawn polls
	// for the socket and can take a moment).
	h, err := p.spawn(ctx, convID, gen)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// Re-check: another turn on this conversation shouldn't exist (exclusivity),
	// but if a racing entry appeared, kill ours and don't leak.
	if e, ok := p.byConv[convID]; ok && e.handle != nil {
		p.mu.Unlock()
		h.Kill()
		// Fall back to Acquire semantics on the existing entry: caller retries.
		// Simplest correct behavior: recurse once.
		return p.Acquire(ctx, convID, gen)
	}
	p.byConv[convID] = &pooledEntry{handle: h, inUse: true}
	p.mu.Unlock()
	return h, nil
}

// Release returns a worker to the pool after a turn. healthy → keep it WARM for
// the next turn (mark not-in-use). NOT healthy (crash/ambiguous state) → kill +
// remove so the next Acquire spawns fresh. On a warm release the conn is NOT
// closed (it's owned by the pooled handle and reused next turn).
func (p *workerPool) Release(convID string, healthy bool) {
	p.mu.Lock()
	e, ok := p.byConv[convID]
	if !ok {
		p.mu.Unlock()
		return
	}
	if healthy {
		e.inUse = false
		p.mu.Unlock()
		return
	}
	// Unhealthy: evict + kill.
	delete(p.byConv, convID)
	p.mu.Unlock()
	e.handle.Kill()
}

// Shutdown kills every cached worker and clears the pool (host-shutdown drain).
func (p *workerPool) Shutdown() {
	p.mu.Lock()
	entries := make([]*pooledEntry, 0, len(p.byConv))
	for _, e := range p.byConv {
		entries = append(entries, e)
	}
	p.byConv = make(map[string]*pooledEntry)
	p.mu.Unlock()

	for _, e := range entries {
		e.handle.Kill()
	}
}

// workerHealthy reports whether a pooled handle is usable for another turn.
//
// B1 uses a simple process-alive check; Task B2 strengthens this into a real
// health probe (conn READY / lightweight ping). The seam is here so B2 only has
// to fill this function in.
func workerHealthy(h *workerHandle) bool {
	if h == nil {
		return false
	}
	return processAlive(h)
}

// processAlive reports whether the worker's process is still running. A killed
// or exited process fails signal 0, so the next Acquire evicts + respawns.
func processAlive(h *workerHandle) bool {
	if h.cmd == nil || h.cmd.Process == nil {
		// A dial-injected handle (tests) has no process; treat it as alive so
		// the injected transport is reused.
		return true
	}
	// Signal 0 probes existence without delivering a signal.
	return h.cmd.Process.Signal(syscall.Signal(0)) == nil
}
