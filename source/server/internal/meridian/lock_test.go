package meridian

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsure_LockHeldBySiblingAdoptsExternal(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)
	m.acquireLockFn = func() (func(), bool) { return nil, false }

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Errorf("state = %s, want external when spawn lock is held elsewhere", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned %d times without holding the spawn lock, want 0", spawned)
	}
}

func TestExternalWatcher_AcquiresLockWhenFreed(t *testing.T) {
	prev := externalPollInterval
	externalPollInterval = 20 * time.Millisecond
	defer func() { externalPollInterval = prev }()

	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)
	// Sibling holds the lock initially; then it dies (kernel releases).
	var lockFree atomic.Bool
	m.acquireLockFn = func() (func(), bool) {
		if !lockFree.Load() {
			return nil, false
		}
		return func() {}, true
	}

	m.Ensure(context.Background(), 3456)
	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external while sibling holds lock", got)
	}

	// The sibling dies: its lock releases and its Meridian's port goes dark
	// (portUsedFn already reports free in newTestManager). The watcher must
	// win the lock and spawn on its own — no outside Ensure calls.
	lockFree.Store(true)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times after lock freed, want 1", spawned)
	}
	m.Stop()
}

func TestStop_ReleasesLock(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)
	var released int32
	m.acquireLockFn = func() (func(), bool) {
		return func() { atomic.AddInt32(&released, 1) }, true
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)
	m.Stop()

	if got := atomic.LoadInt32(&released); got != 1 {
		t.Errorf("lock released %d times after Stop, want 1", got)
	}
}

// The owner must keep the spawn lock across its own Failed → retry cycle:
// releasing between attempts would let a contender grab ownership mid-recovery.
func TestFailedRetry_KeepsLock(t *testing.T) {
	prev := failedRetryInterval
	failedRetryInterval = 20 * time.Millisecond
	defer func() { failedRetryInterval = prev }()

	m := newTestManager()
	var acquires, released, spawnAttempts int32
	m.acquireLockFn = func() (func(), bool) {
		atomic.AddInt32(&acquires, 1)
		return func() { atomic.AddInt32(&released, 1) }, true
	}
	// First spawn fails; the retry's spawn succeeds.
	realSpawnFn := fakeSpawn(&spawnAttempts)
	m.spawnFn = func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		if atomic.AddInt32(&spawnAttempts, 1) == 1 {
			return nil, errors.New("npx exploded")
		}
		atomic.AddInt32(&spawnAttempts, -1) // fakeSpawn counts too; don't double-count
		return realSpawnFn(ctx, port, version, sink)
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateFailed)
	waitForState(t, m, StateReady)

	if got := atomic.LoadInt32(&acquires); got != 1 {
		t.Errorf("lock acquired %d times across a Failed retry, want 1 (held throughout)", got)
	}
	if got := atomic.LoadInt32(&released); got != 0 {
		t.Errorf("lock released %d times mid-recovery, want 0", got)
	}
	m.Stop()
}

// Adopting a genuinely foreign proxy (port busy, nothing of ours to reap)
// must release the lock: we aren't spawning, and a wedged holder would block
// every sibling's takeover when the foreign proxy dies.
func TestExternalAdopt_ReleasesLock(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)
	var released int32
	m.acquireLockFn = func() (func(), bool) {
		return func() { atomic.AddInt32(&released, 1) }, true
	}

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Fatalf("state = %s, want external", got)
	}
	if got := atomic.LoadInt32(&released); got != 1 {
		t.Errorf("lock released %d times after adopting foreign proxy, want 1", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned despite foreign proxy on the port")
	}
}

// Real flock semantics: conflicts are between open file descriptions, so two
// separate opens of the same lock file contend even within one test process,
// and releasing frees it for the loser.
func TestRealAcquireLock_Flock(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "meridian.log")
	a := New(nil, logPath)
	b := New(nil, logPath)

	relA, ok := a.realAcquireLock()
	if !ok {
		t.Fatal("first acquire failed, want success")
	}
	if rel, ok := b.realAcquireLock(); ok {
		rel()
		t.Fatal("second acquire succeeded while first is held, want conflict")
	}
	relA()
	relB, ok := b.realAcquireLock()
	if !ok {
		t.Fatal("acquire after release failed, want success")
	}
	relB()
}

// Empty logPath (tests, minimal embeddings) has no shared state to coordinate:
// the lock degrades to always-acquired rather than blocking spawns.
func TestRealAcquireLock_NoLogPathAlwaysAcquires(t *testing.T) {
	m := New(nil, "")
	rel, ok := m.realAcquireLock()
	if !ok {
		t.Fatal("acquire with empty logPath failed, want degraded success")
	}
	rel()
}
