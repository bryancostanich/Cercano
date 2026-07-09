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
	"time"

	"google.golang.org/grpc/connectivity"
)

// spawnFunc spawns a worker process and returns a connected handle. It is the
// pool's injectable seam: production uses spawnWorker; tests inject a seam that
// returns bufconn-backed handles and counts spawns.
type spawnFunc func(ctx context.Context, convID string, gen uint64) (*workerHandle, error)

// pooledEntry is one conversation's cached worker.
type pooledEntry struct {
	handle *workerHandle
	inUse  bool
	// lastUsed records when this worker was last active for its conversation —
	// set on spawn/Acquire (a turn starts) and on Release (a turn ends and the
	// worker returns warm). The idle-reaper compares now()-lastUsed against the
	// window. An inUse entry is never reaped regardless of lastUsed.
	lastUsed time.Time
}

// workerPool keeps at most one warm worker per conversation.
type workerPool struct {
	mu     sync.Mutex
	byConv map[string]*pooledEntry
	spawn  spawnFunc

	// now is the pool's clock, injectable so tests exercise the idle-reaper with
	// a fake clock + tiny window instead of sleeping real durations. Defaults to
	// time.Now.
	now func() time.Time

	// done is closed by Shutdown to stop the reaper goroutine (in addition to
	// the reaper's own ctx). closeOnce guards against a double close.
	done      chan struct{}
	closeOnce sync.Once
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
		now:    time.Now,
		done:   make(chan struct{}),
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
			e.lastUsed = p.now() // reuse bumps the idle clock.
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
	// Re-check: a racing entry may have appeared while we spawned outside the
	// lock. Under Phase-4 turn-exclusivity a single conversation is used by at
	// most one live turn, but during a SUPERSESSION window a new turn's Acquire
	// can race the old (superseded) turn's Release. Resolve this DETERMINISTICALLY
	// and BOUNDED — no recursion, no spin, no process thrash:
	if e, ok := p.byConv[convID]; ok && e.handle != nil {
		if !e.inUse && workerHealthy(e.handle) {
			// A reusable warm entry raced in. Prefer it and discard our spawn
			// (bounded: exactly one extra spawn+kill, no loop).
			e.inUse = true
			e.lastUsed = p.now()
			reuse := e.handle
			p.mu.Unlock()
			h.Kill()
			return reuse, nil
		}
		// The racing entry is in-use (the old superseded turn still holds it) or
		// unhealthy. Do NOT kill it blindly (its owner will Release it, and our
		// Release-by-handle guard makes that a no-op once we replace the entry)
		// and do NOT spin. REPLACE the entry with OUR fresh worker; the displaced
		// handle is now unreferenced by the pool, so the stale turn's
		// Release(convID, displaced, …) will compare-and-find-mismatch → no-op,
		// and that turn's own cleanup path Kills its handle. Our fresh worker is
		// the pool's live entry for the next turn.
		p.byConv[convID] = &pooledEntry{handle: h, inUse: true, lastUsed: p.now()}
		p.mu.Unlock()
		return h, nil
	}
	p.byConv[convID] = &pooledEntry{handle: h, inUse: true, lastUsed: p.now()}
	p.mu.Unlock()
	return h, nil
}

// Release returns a SPECIFIC handle to the pool after a turn. It is a
// compare-and-act: it only touches the pool entry if that entry STILL holds the
// exact handle this turn acquired (mirrors the Phase-4 turn-registry `cur == h`
// release-guard). This disambiguates concurrent handles for one conversation in
// the supersession window — a stale (superseded) turn whose handle was already
// REPLACED by a newer Acquire must NOT mark the newer worker not-in-use or evict
// it. The stale turn instead Kills its own displaced handle so it doesn't leak.
//
//   - healthy + entry still holds h → keep it WARM (mark not-in-use). The conn
//     is NOT closed (owned by the pooled handle, reused next turn).
//   - NOT healthy (crash/ambiguous) + entry still holds h → evict + kill.
//   - entry no longer holds h (displaced by a racing Acquire) → this handle is
//     orphaned; Kill it here so it doesn't leak. The live entry is untouched.
func (p *workerPool) Release(convID string, h *workerHandle, healthy bool) {
	p.mu.Lock()
	e, ok := p.byConv[convID]
	if !ok || e.handle != h {
		// Our handle was displaced (supersession) or already gone. Don't touch
		// the pool entry (it belongs to a newer turn). Kill our orphan handle.
		p.mu.Unlock()
		h.Kill() // nil-safe; no-op on a nil handle.
		return
	}
	if healthy {
		e.inUse = false
		e.lastUsed = p.now() // returned warm: start the idle clock from now.
		p.mu.Unlock()
		return
	}
	// Unhealthy: evict + kill.
	delete(p.byConv, convID)
	p.mu.Unlock()
	e.handle.Kill()
}

// Shutdown kills every cached worker and clears the pool (host-shutdown drain).
// It also stops the idle-reaper goroutine (by closing p.done).
func (p *workerPool) Shutdown() {
	p.closeOnce.Do(func() { close(p.done) })

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

// StartReaper launches the background idle-reaper goroutine. Every tick it scans
// cached entries and reaps any that are NOT inUse (no active turn) AND whose
// idle age (now-lastUsed) exceeds window. An inUse worker (a live turn) is NEVER
// reaped. The goroutine stops on ctx cancel or Shutdown (p.done closed).
//
// window <= 0 DISABLES reaping (config sentinel for "never reap"): no goroutine
// is started. The tick interval is derived from the window so a small test window
// sweeps promptly while the production window sweeps modestly (bounded to a sane
// range so a huge window still gets swept and a tiny one doesn't busy-spin).
func (p *workerPool) StartReaper(ctx context.Context, window time.Duration) {
	if window <= 0 {
		return // reaping disabled.
	}
	go p.reapLoop(ctx, window, reapTick(window))
}

// reapTick derives a sweep interval from the idle window: a fraction of the
// window (so an idle worker is reaped within ~one tick of crossing it), clamped
// so we neither busy-spin on a tiny window nor sleep excessively on a huge one.
func reapTick(window time.Duration) time.Duration {
	tick := window / 4
	if tick < minReapTick {
		tick = minReapTick
	}
	if tick > maxReapTick {
		tick = maxReapTick
	}
	return tick
}

const (
	// minReapTick floors the sweep interval so a tiny (test) window doesn't spin
	// the reaper hot; maxReapTick caps it so a large window still sweeps within a
	// bounded latency. Named, not buried literals.
	minReapTick = 250 * time.Millisecond
	maxReapTick = 30 * time.Second
)

// reapLoop is the reaper's ticker loop. Extracted so tests can drive it with a
// tiny tick and fake clock deterministically.
func (p *workerPool) reapLoop(ctx context.Context, window, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case <-t.C:
			p.reapIdle(window)
		}
	}
}

// reapIdle scans the pool once and reaps every not-inUse entry idle longer than
// window. Map mutation happens under the mutex; Kill() runs OUTSIDE the lock
// (same discipline as Acquire/Release — Kill may block on process wait).
func (p *workerPool) reapIdle(window time.Duration) {
	now := p.now()
	var reaped []*workerHandle

	p.mu.Lock()
	for convID, e := range p.byConv {
		if e.inUse {
			continue // a live turn holds it — never reap.
		}
		if now.Sub(e.lastUsed) <= window {
			continue // used recently — keep warm.
		}
		reaped = append(reaped, e.handle)
		delete(p.byConv, convID)
	}
	p.mu.Unlock()

	for _, h := range reaped {
		h.Kill()
	}
}

// workerHealthy reports whether a pooled handle is usable for another turn.
//
// B2 strengthens the B1 process-alive check with a cheap gRPC connectivity
// check so a worker that died BETWEEN turns is caught on the next Acquire and
// transparently respawned (rather than failing the turn):
//
//   - the process must still be alive (signal-0 to the process group), and
//   - the gRPC conn must not be in a terminal/persistently-broken state
//     (Shutdown or TransientFailure) — those mean the transport to the worker
//     is unusable, e.g. the socket closed when the process died.
//
// This is a CONNECTIVITY check, NOT a full round-trip: a per-Acquire ping would
// add latency to every warm turn. Process-alive + conn-state is enough for B2;
// a real ping remains an optional future refinement.
func workerHealthy(h *workerHandle) bool {
	if h == nil {
		return false
	}
	if !processAlive(h) {
		return false
	}
	return connUsable(h)
}

// connUsable reports whether the handle's gRPC conn is not in a terminal or
// persistently-failed connectivity state. A nil conn (a not-yet-connected or
// partially-built handle) is treated as usable so we don't evict on absence of
// a conn; the process-alive gate already covers a dead worker in that case.
func connUsable(h *workerHandle) bool {
	if h.conn == nil {
		return true
	}
	switch h.conn.GetState() {
	case connectivity.Shutdown, connectivity.TransientFailure:
		return false
	default:
		// Idle / Connecting / Ready are all reusable: a fresh RunTurn stream will
		// drive an Idle conn back to Ready.
		return true
	}
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
