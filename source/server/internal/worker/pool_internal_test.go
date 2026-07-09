package worker

// pool_internal_test.go — Task B2: strengthened between-turns health check and
// the supersession spawn-race fix, tested DIRECTLY against the pool (internal
// package) so we can drive gRPC conn state and concurrent Acquire/Release.
//
//   - a worker whose conn died BETWEEN turns is caught by workerHealthy on the
//     next Acquire → evicted + respawned transparently (spawn increments);
//   - a supersession-style race (a second Acquire while the first handle is
//     still inUse) resolves BOUNDED — no unbounded recursion / process thrash;
//   - Release is compare-and-act by handle: a stale (displaced) turn's Release
//     does NOT evict or mark-not-in-use the live turn's worker.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// liveConn returns a connected gRPC conn to an in-process bufconn server plus a
// stop func. The conn is usable (not Shutdown/TransientFailure) until stopped.
func liveConn(t *testing.T) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	return conn, func() { _ = conn.Close(); srv.Stop() }
}

// TestPool_BetweenTurnsDeath_RespawnsTransparently: turn 1 spawns a worker
// (count 1). The worker "dies" between turns — we close its gRPC conn so its
// connectivity state becomes Shutdown. On the next Acquire, workerHealthy must
// return false, so the pool evicts + spawns a FRESH worker (count 2) instead of
// handing back the dead one. This is the between-turns-death-is-transparent
// guarantee (distinct from a mid-turn crash, which yields Unavailable).
func TestPool_BetweenTurnsDeath_RespawnsTransparently(t *testing.T) {
	var spawns int32

	deadConn, deadStop := liveConn(t)
	defer deadStop()
	freshConn, freshStop := liveConn(t)
	defer freshStop()

	spawn := func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		n := atomic.AddInt32(&spawns, 1)
		if n == 1 {
			return &workerHandle{conn: deadConn}, nil // turn 1's worker
		}
		return &workerHandle{conn: freshConn}, nil // respawn after death
	}
	p := newWorkerPool(spawn)
	ctx := context.Background()

	// Turn 1: spawn + warm-release (healthy).
	h1, err := p.Acquire(ctx, "conv-A", 1)
	if err != nil {
		t.Fatalf("turn 1 acquire: %v", err)
	}
	p.Release("conv-A", h1, true) // kept WARM

	if got := atomic.LoadInt32(&spawns); got != 1 {
		t.Fatalf("turn 1 should spawn exactly once: spawns = %d", got)
	}

	// The worker dies BETWEEN turns: closing the conn drives its connectivity
	// state to Shutdown, which workerHealthy must reject.
	_ = deadConn.Close()
	// GetState is immediate after Close; assert the health check sees it dead.
	if workerHealthy(&workerHandle{conn: deadConn}) {
		t.Fatalf("workerHealthy must reject a Shutdown conn (state=%v)", deadConn.GetState())
	}

	// Turn 2: Acquire must detect the dead cached worker → evict → spawn fresh.
	h2, err := p.Acquire(ctx, "conv-A", 2)
	if err != nil {
		t.Fatalf("turn 2 acquire (post-death respawn): %v", err)
	}
	if got := atomic.LoadInt32(&spawns); got != 2 {
		t.Fatalf("between-turns death must respawn: spawns = %d, want 2", got)
	}
	if h2.conn != freshConn {
		t.Fatalf("turn 2 must get the FRESH worker, not the dead one")
	}
	p.Release("conv-A", h2, true)
}

// TestPool_ConnStateHealth verifies the connectivity gate directly: a live conn
// is usable; a closed (Shutdown) conn is not; a nil conn is treated as usable
// (process-alive gate covers that case).
func TestPool_ConnStateHealth(t *testing.T) {
	conn, stop := liveConn(t)
	defer stop()

	if !connUsable(&workerHandle{conn: conn}) {
		t.Fatalf("a live conn (state=%v) should be usable", conn.GetState())
	}
	if !connUsable(&workerHandle{conn: nil}) {
		t.Fatalf("a nil conn should be treated as usable")
	}
	_ = conn.Close()
	if conn.GetState() != connectivity.Shutdown {
		t.Fatalf("closed conn state = %v, want Shutdown", conn.GetState())
	}
	if connUsable(&workerHandle{conn: conn}) {
		t.Fatalf("a Shutdown conn must NOT be usable")
	}
}

// TestPool_ReleaseByHandleGuard: a stale turn's Release, keyed by its OWN
// (already-displaced) handle, must be a no-op on the live entry. We simulate
// supersession by placing a "new" handle in the entry, then Releasing the OLD
// handle: the entry must remain inUse and keep the new handle (the stale Release
// neither marks it not-in-use nor evicts it).
func TestPool_ReleaseByHandleGuard(t *testing.T) {
	oldConn, oldStop := liveConn(t)
	defer oldStop()
	newConn, newStop := liveConn(t)
	defer newStop()

	p := newWorkerPool(func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		return &workerHandle{conn: newConn}, nil
	})

	oldHandle := &workerHandle{conn: oldConn}
	newHandle := &workerHandle{conn: newConn}
	// Entry currently holds the NEW handle, inUse by the live (new) turn.
	p.byConv["conv-A"] = &pooledEntry{handle: newHandle, inUse: true}

	// Stale turn releases its OWN (old, displaced) handle — must be a no-op on
	// the entry.
	p.Release("conv-A", oldHandle, true)

	e, ok := p.byConv["conv-A"]
	if !ok {
		t.Fatal("stale Release must NOT evict the live entry")
	}
	if e.handle != newHandle {
		t.Fatal("stale Release must NOT replace the live handle")
	}
	if !e.inUse {
		t.Fatal("stale Release must NOT mark the live entry not-in-use")
	}
}

// TestPool_SupersessionRace_Bounded: two concurrent Acquire on the SAME convID
// where the first is still inUse when the second acquires. The old (recursive)
// code busy-spawned+killed workers in a tight loop until the first released;
// the fix resolves this deterministically and BOUNDED. We assert: no panic,
// spawn count is bounded (≤ number of Acquire calls), and after both turns
// release, exactly one live entry remains.
func TestPool_SupersessionRace_Bounded(t *testing.T) {
	var spawns int32
	// Each spawn gets its own live conn so Kill()/Close() on one doesn't wedge
	// another. Track for cleanup.
	var mu sync.Mutex
	var stops []func()

	spawn := func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		atomic.AddInt32(&spawns, 1)
		conn, stop := liveConn(t)
		mu.Lock()
		stops = append(stops, stop)
		mu.Unlock()
		return &workerHandle{conn: conn}, nil
	}
	defer func() {
		mu.Lock()
		for _, s := range stops {
			s()
		}
		mu.Unlock()
	}()

	p := newWorkerPool(spawn)
	ctx := context.Background()

	// First Acquire holds the entry inUse.
	h1, err := p.Acquire(ctx, "conv-A", 1)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}

	// Fire N concurrent Acquires while h1 is still inUse (supersession window).
	const n = 8
	var wg sync.WaitGroup
	handles := make([]*workerHandle, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			handles[i], errs[i] = p.Acquire(ctx, "conv-A", uint64(i+2))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent acquire %d: %v", i, err)
		}
	}

	// BOUNDED: each Acquire spawns at most once (the fix does one spawn +
	// possibly discards it, no recursion). With 1 + n Acquire calls, spawns must
	// not exceed 1 + n (the old unbounded-recursion code could blow well past).
	if got := atomic.LoadInt32(&spawns); got > int32(1+n) {
		t.Fatalf("supersession spawns unbounded: spawns = %d, want <= %d", got, 1+n)
	}

	// Release everything (by handle). Stale/displaced releases are no-ops on the
	// live entry; the winning handle keeps the entry warm.
	p.Release("conv-A", h1, true)
	for i := 0; i < n; i++ {
		p.Release("conv-A", handles[i], true)
	}

	// Exactly one live entry should remain for the conversation.
	p.mu.Lock()
	_, ok := p.byConv["conv-A"]
	nEntries := len(p.byConv)
	p.mu.Unlock()
	if !ok || nEntries != 1 {
		t.Fatalf("after supersession, want exactly 1 live entry, got %d (present=%v)", nEntries, ok)
	}

	p.Shutdown()
	// Give any background Kill/Wait goroutines a beat under -race.
	time.Sleep(10 * time.Millisecond)
}

// fakeClock is a monotonic, test-controlled clock. now() reads it; advance moves
// it forward. Guarded by a mutex so the reaper goroutine (if any) and the test
// don't race on it under -race.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestPool_ReapIdle_ReapsIdlePastWindow: a warm (not-inUse) worker idle longer
// than the window is reaped by reapIdle — its process is Killed via the spawn
// seam and its entry removed. Uses a fake clock + tiny window, no real sleep.
func TestPool_ReapIdle_ReapsIdlePastWindow(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	killed := make(chan struct{}, 1)

	p := newWorkerPool(func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		return &workerHandle{onKill: func() { killed <- struct{}{} }}, nil
	})
	p.now = clk.now

	const window = 60 * time.Second
	h, err := p.Acquire(context.Background(), "conv-A", 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	p.Release("conv-A", h, true) // warm at t=0.

	// Advance PAST the window, then sweep once.
	clk.advance(window + time.Second)
	p.reapIdle(window)

	p.mu.Lock()
	_, present := p.byConv["conv-A"]
	p.mu.Unlock()
	if present {
		t.Fatal("idle-past-window worker must be reaped (entry removed)")
	}
	select {
	case <-killed:
	case <-time.After(time.Second):
		t.Fatal("reaped worker's process must be Killed")
	}
}

// TestPool_ReapIdle_SkipsInUse: an inUse (active-turn) worker is NEVER reaped
// even if its lastUsed is far in the past.
func TestPool_ReapIdle_SkipsInUse(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := newWorkerPool(func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		return &workerHandle{onKill: func() { t.Error("in-use worker must NOT be killed") }}, nil
	})
	p.now = clk.now

	const window = 60 * time.Second
	if _, err := p.Acquire(context.Background(), "conv-A", 1); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Do NOT Release — the entry stays inUse (a live turn holds it).

	clk.advance(window * 10) // very old, but in use.
	p.reapIdle(window)

	p.mu.Lock()
	_, present := p.byConv["conv-A"]
	p.mu.Unlock()
	if !present {
		t.Fatal("in-use worker must NOT be reaped")
	}
}

// TestPool_ReapIdle_SkipsRecent: a worker released within the window is NOT
// reaped (still warm for the next turn).
func TestPool_ReapIdle_SkipsRecent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := newWorkerPool(func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		return &workerHandle{onKill: func() { t.Error("recent worker must NOT be killed") }}, nil
	})
	p.now = clk.now

	const window = 60 * time.Second
	h, err := p.Acquire(context.Background(), "conv-A", 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	p.Release("conv-A", h, true) // warm at t=0.

	clk.advance(window / 2) // still within the window.
	p.reapIdle(window)

	p.mu.Lock()
	_, present := p.byConv["conv-A"]
	p.mu.Unlock()
	if !present {
		t.Fatal("recently-released worker must NOT be reaped")
	}
}

// TestPool_Reaper_LoopReapsAndStops drives the full reaper goroutine with a tiny
// tick + fake clock: it eventually reaps an idle worker, and Shutdown stops the
// loop (verified by the goroutine returning; a second Shutdown is a no-op).
func TestPool_Reaper_LoopReapsAndStops(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	killed := make(chan struct{}, 1)
	p := newWorkerPool(func(_ context.Context, _ string, _ uint64) (*workerHandle, error) {
		return &workerHandle{onKill: func() { killed <- struct{}{} }}, nil
	})
	p.now = clk.now

	const window = 60 * time.Second
	h, err := p.Acquire(context.Background(), "conv-A", 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	p.Release("conv-A", h, true)

	// Start the loop with a fast real tick so the test doesn't wait long; the
	// idle decision uses the FAKE clock, so advancing it past the window makes
	// the next tick reap.
	go p.reapLoop(context.Background(), window, 2*time.Millisecond)
	clk.advance(window + time.Second)

	select {
	case <-killed:
	case <-time.After(2 * time.Second):
		t.Fatal("reaper loop must reap the idle worker")
	}

	p.Shutdown() // stops the loop; second call must not panic (closeOnce).
	p.Shutdown()
	time.Sleep(10 * time.Millisecond)
}

// TestPool_Shutdown_DrainsAllWorkers is the B5 graceful-drain guard: Shutdown()
// kills EVERY cached warm worker (not just idle ones) and empties the pool, so
// a clean host shutdown leaves no orphaned worker processes. Also asserts
// Shutdown is idempotent (safe to call twice).
func TestPool_Shutdown_DrainsAllWorkers(t *testing.T) {
	killed := make(chan string, 8)
	p := newWorkerPool(func(_ context.Context, conv string, _ uint64) (*workerHandle, error) {
		c := conv
		return &workerHandle{onKill: func() { killed <- c }}, nil
	})

	convs := []string{"conv-A", "conv-B", "conv-C"}
	for _, conv := range convs {
		h, err := p.Acquire(context.Background(), conv, 1)
		if err != nil {
			t.Fatalf("acquire %s: %v", conv, err)
		}
		p.Release(conv, h, true) // return warm — the between-turns state a drain must clean up.
	}

	p.Shutdown()

	// Pool emptied.
	p.mu.Lock()
	n := len(p.byConv)
	p.mu.Unlock()
	if n != 0 {
		t.Errorf("Shutdown left %d pool entries, want 0", n)
	}

	// Every warm worker's process was killed.
	got := map[string]bool{}
	for range convs {
		select {
		case c := <-killed:
			got[c] = true
		case <-time.After(time.Second):
			t.Fatalf("Shutdown killed only %d workers, want %d", len(got), len(convs))
		}
	}
	for _, conv := range convs {
		if !got[conv] {
			t.Errorf("Shutdown did not kill %s's worker", conv)
		}
	}

	// Idempotent: a second Shutdown must not panic (closeOnce guards the done chan).
	p.Shutdown()
}
