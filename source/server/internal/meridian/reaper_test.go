package meridian

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// The pidfile helpers round-trip, and an empty path is inert.
func TestPidFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.pid")

	if _, ok := readPidFile(path); ok {
		t.Fatal("readPidFile on a missing file should return ok=false")
	}
	writePidFile(path, 12345)
	if pid, ok := readPidFile(path); !ok || pid != 12345 {
		t.Fatalf("readPidFile = (%d,%v), want (12345,true)", pid, ok)
	}
	removePidFile(path)
	if _, ok := readPidFile(path); ok {
		t.Fatal("readPidFile after remove should return ok=false")
	}

	// Empty path is a no-op for write and never yields a pid.
	writePidFile("", 1)
	if _, ok := readPidFile(""); ok {
		t.Fatal("empty path should never yield a pid")
	}
}

// pidFilePath sits next to the log, and is empty when logPath is unset.
func TestPidFilePath(t *testing.T) {
	if p := (&Manager{logPath: ""}).pidFilePath(); p != "" {
		t.Errorf("empty logPath -> pidFilePath %q, want empty", p)
	}
	m := &Manager{logPath: filepath.FromSlash("/tmp/x/meridian.log")}
	if got, want := m.pidFilePath(), filepath.FromSlash("/tmp/x/meridian.pid"); got != want {
		t.Errorf("pidFilePath = %q, want %q", got, want)
	}
}

// Gate 3: when reapOrphanFn reports it reaped our orphan, Ensure spawns a fresh
// process instead of adopting the port as external.
func TestEnsure_ReapsOwnOrphanThenSpawns(t *testing.T) {
	m := newTestManager()
	var portUsed atomic.Bool
	portUsed.Store(true) // the orphan holds the port
	m.portUsedFn = func(int) bool { return portUsed.Load() }
	var reaped int32
	m.reapOrphanFn = func(int) bool {
		atomic.AddInt32(&reaped, 1)
		portUsed.Store(false) // reaping frees the port
		return true
	}
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if atomic.LoadInt32(&reaped) != 1 {
		t.Errorf("reapOrphanFn called %d times, want 1", reaped)
	}
	if atomic.LoadInt32(&spawned) != 1 {
		t.Errorf("spawned %d times, want 1 (fresh spawn after reap)", spawned)
	}
	m.Stop()
}

// Gate 3: when reapOrphanFn reports nothing of ours, Ensure adopts external and
// does not spawn (a genuinely foreign proxy is left alone).
func TestEnsure_ForeignPortAdoptsExternalNoReap(t *testing.T) {
	m := newTestManager()
	m.portUsedFn = func(int) bool { return true }
	m.reapOrphanFn = func(int) bool { return false } // not ours
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)

	if got := m.Status().State; got != StateExternal {
		t.Errorf("state = %s, want external", got)
	}
	if atomic.LoadInt32(&spawned) != 0 {
		t.Errorf("spawned %d times, want 0 (adopt, don't spawn)", spawned)
	}
	m.Stop()
}

// realReapOrphan: no pidfile -> false; a dead recorded pid -> false + cleanup;
// a live recorded pid -> kills it and returns true.
func TestRealReapOrphan(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with no pidfile should be false")
	}

	// A dead pid (macOS default max pid is 99999, so 999999 cannot exist).
	writePidFile(m.pidFilePath(), 999999)
	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a dead pid should be false")
	}
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("a stale pidfile should be cleaned up")
	}

	// A live recorded process gets reaped.
	proc := exec.Command("sleep", "30")
	if err := proc.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	writePidFile(m.pidFilePath(), proc.Process.Pid)
	if !m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a live recorded pid should be true")
	}
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed after a reap")
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	select {
	case <-done: // reaped -> Wait returns
	case <-time.After(2 * time.Second):
		_ = proc.Process.Kill()
		t.Error("reaped process still alive after 2s")
	}
}

// A real spawn records a pidfile; a clean Stop removes it.
func TestSpawn_WritesPidFile_StopRemoves(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return nil }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	if pid, ok := readPidFile(m.pidFilePath()); !ok || pid <= 0 {
		t.Fatalf("expected a pidfile after spawn, got (%d,%v)", pid, ok)
	}
	m.Stop()
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed after a clean Stop")
	}
}
