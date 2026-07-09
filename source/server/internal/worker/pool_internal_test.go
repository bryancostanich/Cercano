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
