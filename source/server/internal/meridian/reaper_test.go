package meridian

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var errTestProbe = errors.New("probe refused")

// fakeGroupSpawn mimics realSpawn's process shape: a shell parent with a
// background child in its own process group (Setpgid), like npx running the
// Meridian binary. Killing only the parent leaves the child holding on — the
// exact orphan shape observed in production.
func fakeGroupSpawn(counter *int32) func(context.Context, int, string, io.Writer) (*exec.Cmd, error) {
	return func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		atomic.AddInt32(counter, 1)
		c := exec.Command("sh", "-c", "sleep 60 & wait")
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}
}

// groupAlive reports whether any member of the process group is still running.
func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

// startTestGroup spawns argv in its own process group and returns the cmd.
// The caller is responsible for killing the group.
func startTestGroup(t *testing.T, argv ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(argv[0], argv[1:]...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatalf("start helper process group %v: %v", argv, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
	return c
}

// waitForGroupSize blocks until the process group has at least n members —
// e.g. a shell parent that has actually forked its background child. Without
// this, a kill can race the fork and "whole group died" passes vacuously.
func waitForGroupSize(t *testing.T, pgid, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("pgrep", "-g", strconv.Itoa(pgid)).Output()
		if err == nil && len(strings.Fields(string(out))) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d never reached %d members", pgid, n)
}

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
// a live recorded group that is ours -> kills the whole group, returns true.
func TestRealReapOrphan(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.identifyGroupFn = func(int) bool { return true } // it's ours

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with no pidfile should be false")
	}

	// A dead pid: spawn a short-lived child and wait it out, so the pid is
	// known-reaped on every platform (no magic pid-max assumptions).
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatalf("run short-lived helper: %v", err)
	}
	writePidFile(m.pidFilePath(), dead.Process.Pid)
	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a dead pid should be false")
	}
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("a stale pidfile should be cleaned up")
	}

	// A live recorded group gets reaped whole: parent AND its child die,
	// mirroring npx + the Meridian it spawns.
	proc := startTestGroup(t, "sh", "-c", "sleep 30 & wait")
	waitForGroupSize(t, proc.Process.Pid, 2)
	writePidFile(m.pidFilePath(), proc.Process.Pid)
	if !m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a live recorded group should be true")
	}
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed after a reap")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && groupAlive(proc.Process.Pid) {
		time.Sleep(20 * time.Millisecond)
	}
	if groupAlive(proc.Process.Pid) {
		t.Error("process group still alive after reap — child survived")
	}
}

// A live recorded pid that is NOT a Meridian must not be killed: a stale
// pidfile whose pid the OS recycled to an innocent process. The reaper drops
// the pidfile and reports nothing-of-ours so Gate 3 adopts external instead.
func TestRealReapOrphan_SparesRecycledPid(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.identifyGroupFn = func(int) bool { return false } // alive, but not ours

	proc := startTestGroup(t, "sleep", "30")
	writePidFile(m.pidFilePath(), proc.Process.Pid)

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan must not reap a pid it can't identify as Meridian")
	}
	if groupAlive(proc.Process.Pid) == false {
		t.Error("innocent process was killed")
	}
	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("the stale pidfile should still be dropped")
	}
}

// The production identity check: a process group whose command line mentions
// meridian is ours; anything else is not.
func TestGroupLooksLikeMeridian(t *testing.T) {
	// A fake "meridian": /bin/sleep behind a symlink whose path carries the name.
	link := filepath.Join(t.TempDir(), "meridian-fake")
	if err := os.Symlink("/bin/sleep", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	fake := startTestGroup(t, link, "30")
	if !groupLooksLikeMeridian(fake.Process.Pid) {
		t.Error("group running a meridian-named command should be identified as ours")
	}

	plain := startTestGroup(t, "sleep", "30")
	if groupLooksLikeMeridian(plain.Process.Pid) {
		t.Error("plain sleep group must not be identified as Meridian")
	}
}

// Stop must kill the spawned process's entire group, not just the direct
// child: realSpawn runs npx, and the Meridian it spawns must not survive a
// clean Stop — that is exactly how orphans were being created.
func TestStop_KillsWholeProcessGroup(t *testing.T) {
	m := newTestManager()
	var spawned int32
	m.spawnFn = fakeGroupSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	m.mu.Lock()
	pgid := m.cmd.Process.Pid
	m.mu.Unlock()
	waitForGroupSize(t, pgid, 2) // shell parent has forked its child

	m.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && groupAlive(pgid) {
		time.Sleep(20 * time.Millisecond)
	}
	if groupAlive(pgid) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // clean up the test host
		t.Error("process group still alive after Stop — child survived parent kill")
	}
}

// A probe failure must clean up the pidfile it wrote at spawn — a stale
// pidfile naming a dead pid is the raw material for reaping a recycled pid.
func TestProbeFailure_RemovesPidFile(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return errTestProbe }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	m.Ensure(ctx, 3456)
	waitForState(t, m, StateFailed)

	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed when the probe fails")
	}
}

// An unexpected process exit must clean up the pidfile too.
func TestUnexpectedExit_RemovesPidFile(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return nil }
	var spawned int32
	// Short-lived process: reaches Ready, then exits on its own.
	m.spawnFn = func(ctx context.Context, port int, version string, sink io.Writer) (*exec.Cmd, error) {
		atomic.AddInt32(&spawned, 1)
		c := exec.Command("sleep", "0.15")
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)
	waitForState(t, m, StateFailed) // process exits unexpectedly

	if _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed when Meridian exits unexpectedly")
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
