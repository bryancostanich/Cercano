package meridian

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestManager builds a Manager wired entirely to fakes — no real spawns,
// no network, no keychain. Callers pass nil for any fn they want to use the
// defaults (NodeOK prereqs, auth=true, no port in use, never-ready probe).
func newTestManager() *Manager {
	m := New(nil, "")
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true, NodeVersion: "v22.10.0"} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return nil } // ready immediately
	// No external Meridian identified / nothing to reap by default; the
	// stale-external tests override these. Keeps newTestManager network-free.
	m.versionProbeFn = func(context.Context, int) (string, bool) { return "", false }
	m.reapForeignFn = func(int) bool { return false }
	return m
}

// fakeSpawn returns a long-lived sleep subprocess so the supervisor's
// cmd.Wait() blocks until we explicitly Kill or Stop. Each spawn increments
// the counter so tests can assert how many times we spawned.
func fakeSpawn(counter *int32) func(context.Context, int, string, io.Writer) (*exec.Cmd, error) {
	return func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		atomic.AddInt32(counter, 1)
		// `sleep 60` is plenty for test scope and self-cleans when killed.
		c := exec.CommandContext(ctx, "sleep", "60")
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}
}

// waitForState busy-waits up to 3s for the manager to enter the given state.
// Tests fail if it doesn't get there — three seconds is plenty of slack for
// a goroutine handoff that should take microseconds.
func waitForState(t *testing.T, m *Manager, want State) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never reached state %s (current: %s, msg: %q)", want, m.Status().State, m.Status().Message)
}

func TestEnsure_PrereqsMissing(t *testing.T) {
	m := newTestManager()
	m.prereqsFn = func() Prereqs {
		return Prereqs{NodeOK: false, MissingNotes: []string{"Node.js 22+ not on PATH"}}
	}
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StatePrereqsMissing {
		t.Errorf("state = %s, want prereqs_missing", got)
	}
	if len(m.Status().MissingDeps) == 0 {
		t.Errorf("MissingDeps empty, want at least one note")
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned %d times, want 0 when prereqs missing", spawned)
	}
}

func TestEnsure_NeedsAuth(t *testing.T) {
	m := newTestManager()
	m.authFn = func() bool { return false }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateNeedsAuth {
		t.Errorf("state = %s, want needs_auth", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned %d times, want 0 when auth missing", spawned)
	}
}

func TestEnsure_PortInUseAdoptsExternal(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Errorf("state = %s, want external", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned when port already in use")
	}
}

func TestEnsure_ExternalGoneRespawns(t *testing.T) {
	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true)
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external", got)
	}

	// The external Meridian dies (port goes free). A later Ensure on the
	// same port must notice and spawn our own instead of no-opping.
	portUsed.Store(false)
	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want 1 after external went away", spawned)
	}
	m.Stop()
}

func TestExternalWatcher_AutoRespawnsWhenExternalDies(t *testing.T) {
	prev := externalPollInterval
	externalPollInterval = 20 * time.Millisecond
	defer func() { externalPollInterval = prev }()

	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true)
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external", got)
	}

	// The external Meridian dies. The watcher must notice on its own and
	// respawn — no further Ensure calls from the outside.
	portUsed.Store(false)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want 1 after watcher respawn", spawned)
	}
	m.Stop()
}

func TestStop_StopsExternalWatcher(t *testing.T) {
	prev := externalPollInterval
	externalPollInterval = 20 * time.Millisecond
	defer func() { externalPollInterval = prev }()

	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true)
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external", got)
	}

	// Stop while External, then the external process dies. A stopped manager
	// must stay Disabled — the watcher must not resurrect Meridian.
	m.Stop()
	portUsed.Store(false)
	time.Sleep(100 * time.Millisecond) // several poll intervals

	if got := m.Status().State; got != StateDisabled {
		t.Errorf("state = %s after Stop, want disabled", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned %d times after Stop, want 0", spawned)
	}
}

func TestEnsure_SuccessfulSpawnReachesReady(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want exactly 1", spawned)
	}
	if got := m.Status().Port; got != 3456 {
		t.Errorf("Port = %d, want 3456", got)
	}
	m.Stop()
}

func TestEnsure_ProbeNeverSucceedsMarksFailed(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)
	// Probe always fails — simulates a Meridian that started but never bound.
	m.probeFn = func(context.Context, int) error { return errors.New("connection refused") }

	// Drive supervise with a tight context so we don't wait the full 60s.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	m.Ensure(ctx, 3456)
	waitForState(t, m, StateFailed)
}

func TestEnsure_IdempotentOnSamePort(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)
	m.Ensure(context.Background(), 3456) // should no-op
	time.Sleep(50 * time.Millisecond)    // give a phantom second spawn a chance to register

	if got := atomic.LoadInt32(&spawned); got != 1 {
		t.Errorf("spawned %d times, want 1 (second Ensure on same port should no-op)", got)
	}
	m.Stop()
}

// StateFailed must not be a dead end: after a failure, the manager retries
// Ensure on its own (like the auth poller for NeedsAuth and the port watcher
// for External).
func TestFailedState_AutoRetries(t *testing.T) {
	prev := failedRetryInterval
	failedRetryInterval = 20 * time.Millisecond
	defer func() { failedRetryInterval = prev }()

	m := newTestManager()
	var spawned int32
	// First spawn fails; subsequent spawns succeed.
	realSpawnFn := fakeSpawn(&spawned)
	m.spawnFn = func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		if atomic.AddInt32(&spawned, 1) == 1 {
			return nil, errors.New("npx exploded")
		}
		atomic.AddInt32(&spawned, -1) // fakeSpawn counts too; don't double-count
		return realSpawnFn(ctx, port, version, sink)
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateFailed)
	// No further Ensure calls from the outside — the retry must fire itself.
	waitForState(t, m, StateReady)
	m.Stop()
}

// Stop while Failed must cancel the pending retry — a stopped manager never
// resurrects Meridian.
func TestStop_CancelsFailedRetry(t *testing.T) {
	prev := failedRetryInterval
	failedRetryInterval = 20 * time.Millisecond
	defer func() { failedRetryInterval = prev }()

	m := newTestManager()
	var spawned int32
	m.spawnFn = func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		atomic.AddInt32(&spawned, 1)
		return nil, errors.New("npx exploded")
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateFailed)
	m.Stop()

	time.Sleep(100 * time.Millisecond) // several retry intervals
	if got := m.Status().State; got != StateDisabled {
		t.Errorf("state = %s after Stop, want disabled", got)
	}
	if got := atomic.LoadInt32(&spawned); got != 1 {
		t.Errorf("spawn attempted %d times, want 1 (no retries after Stop)", got)
	}
}

func TestStop_FromReadyReturnsToDisabled(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	m.Stop()
	if got := m.Status().State; got != StateDisabled {
		t.Errorf("after Stop state = %s, want disabled", got)
	}
}

func TestEnsure_AuthPollerRetriesWhenAuthAppears(t *testing.T) {
	// Compress the poll interval so the test runs in <1s. Restore after.
	prev := authPollInterval
	authPollInterval = 20 * time.Millisecond
	defer func() { authPollInterval = prev }()

	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	// authFn returns false for the first 3 calls (initial gate + a couple
	// poll cycles), then true thereafter — simulating the user running
	// `claude login` after seeing the chip.
	var authCalls int32
	m.authFn = func() bool {
		n := atomic.AddInt32(&authCalls, 1)
		return n > 3
	}

	m.Ensure(context.Background(), 3456)

	// First state should be NeedsAuth (auth returned false on entry).
	waitForState(t, m, StateNeedsAuth)

	// Poller should re-Ensure once auth flips, eventually reaching Ready.
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times after auth returned, want 1", spawned)
	}
	m.Stop()
}

func TestStop_StopsAuthPoller(t *testing.T) {
	prev := authPollInterval
	authPollInterval = 20 * time.Millisecond
	defer func() { authPollInterval = prev }()

	m := newTestManager()
	// authFn never returns true — poller would loop forever absent Stop.
	var authCalls int32
	m.authFn = func() bool {
		atomic.AddInt32(&authCalls, 1)
		return false
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateNeedsAuth)

	// Let a few polls happen, then Stop.
	time.Sleep(80 * time.Millisecond)
	before := atomic.LoadInt32(&authCalls)
	m.Stop()
	time.Sleep(80 * time.Millisecond)
	after := atomic.LoadInt32(&authCalls)

	// After Stop, polling must cease. Allow a small grace for one in-flight
	// tick that might race the cancel.
	if after-before > 1 {
		t.Errorf("authFn called %d more times after Stop (before=%d after=%d); want ≤1", after-before, before, after)
	}
}

func TestStatusListener_FiresOnTransitions(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	var mu sync.Mutex
	var seen []State
	m.SetStatusListener(func(s Status) {
		mu.Lock()
		seen = append(seen, s.State)
		mu.Unlock()
	})

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)
	m.Stop()

	mu.Lock()
	defer mu.Unlock()
	// Expect Starting → Ready → Disabled (Stop sets Disabled directly).
	want := []State{StateStarting, StateReady, StateDisabled}
	if len(seen) != len(want) {
		t.Fatalf("listener saw %v, want %v", seen, want)
	}
	for i, s := range seen {
		if s != want[i] {
			t.Errorf("transition[%d] = %s, want %s (full: %v)", i, s, want[i], seen)
		}
	}
}
