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

// deadPid returns a pid that is known-reaped: a short-lived child we spawn
// and wait out ourselves, so it's dead on every platform (no magic pid-max
// assumptions). Stands in for a hard-killed spawner.
func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatalf("run short-lived helper: %v", err)
	}
	return c.Process.Pid
}

// The pidfile helpers round-trip both pids, and an empty path is inert.
func TestPidFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.pid")

	if _, _, ok := readPidFile(path); ok {
		t.Fatal("readPidFile on a missing file should return ok=false")
	}
	writePidFile(path, 12345, 678)
	if pid, spawner, ok := readPidFile(path); !ok || pid != 12345 || spawner != 678 {
		t.Fatalf("readPidFile = (%d,%d,%v), want (12345,678,true)", pid, spawner, ok)
	}
	removePidFile(path)
	if _, _, ok := readPidFile(path); ok {
		t.Fatal("readPidFile after remove should return ok=false")
	}

	// Empty path is a no-op for write and never yields a pid.
	writePidFile("", 1, 2)
	if _, _, ok := readPidFile(""); ok {
		t.Fatal("empty path should never yield a pid")
	}
}

// A legacy one-line pidfile (older build) parses with an unknown spawner, and
// a malformed group pid doesn't parse at all.
func TestReadPidFile_LegacyAndMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.pid")

	if err := os.WriteFile(path, []byte("4242"), 0o644); err != nil {
		t.Fatalf("write legacy pidfile: %v", err)
	}
	if pid, spawner, ok := readPidFile(path); !ok || pid != 4242 || spawner != 0 {
		t.Fatalf("legacy readPidFile = (%d,%d,%v), want (4242,0,true)", pid, spawner, ok)
	}

	if err := os.WriteFile(path, []byte("4242\nnot-a-pid"), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if pid, spawner, ok := readPidFile(path); !ok || pid != 4242 || spawner != 0 {
		t.Fatalf("malformed-spawner readPidFile = (%d,%d,%v), want (4242,0,true)", pid, spawner, ok)
	}

	if err := os.WriteFile(path, []byte("not-a-pid\n123"), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if _, _, ok := readPidFile(path); ok {
		t.Fatal("a malformed group pid should not parse")
	}
}

// removePidFileIfOwned only deletes a pidfile that still names our pid — a
// sibling's record must survive our teardown.
func TestRemovePidFileIfOwned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.pid")

	writePidFile(path, 100, 200)
	removePidFileIfOwned(path, 999) // not ours
	if _, _, ok := readPidFile(path); !ok {
		t.Fatal("a pidfile naming someone else's pid must not be removed")
	}
	removePidFileIfOwned(path, 100) // ours
	if _, _, ok := readPidFile(path); ok {
		t.Fatal("our own pidfile should be removed")
	}
	removePidFileIfOwned("", 100) // empty path is inert
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
// a live recorded group whose spawner is dead and that is ours -> kills the
// whole group, returns true (the genuine hard-killed-agent orphan).
func TestRealReapOrphan(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.identifyGroupFn = func(int) bool { return true } // it's ours

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with no pidfile should be false")
	}

	writePidFile(m.pidFilePath(), deadPid(t), deadPid(t))
	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a dead pid should be false")
	}
	if _, _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("a stale pidfile should be cleaned up")
	}

	// A live recorded group with a dead spawner gets reaped whole: parent AND
	// its child die, mirroring npx + the Meridian it spawns.
	proc := startTestGroup(t, "sh", "-c", "sleep 30 & wait")
	waitForGroupSize(t, proc.Process.Pid, 2)
	writePidFile(m.pidFilePath(), proc.Process.Pid, deadPid(t))
	if !m.realReapOrphan(3456) {
		t.Error("realReapOrphan with a live recorded group and dead spawner should be true")
	}
	if _, _, ok := readPidFile(m.pidFilePath()); ok {
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
	writePidFile(m.pidFilePath(), proc.Process.Pid, deadPid(t))

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan must not reap a pid it can't identify as Meridian")
	}
	if groupAlive(proc.Process.Pid) == false {
		t.Error("innocent process was killed")
	}
	if _, _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("the stale pidfile should still be dropped")
	}
}

// A live group whose spawner is also alive is a sibling cercano's healthy
// Meridian, not an orphan — the reaper must not kill it, and must leave the
// sibling's pidfile intact. This is the "Meridian kill war" fix.
func TestRealReapOrphan_SparesSiblingWithLiveSpawner(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	var identified int32
	m.identifyGroupFn = func(int) bool {
		atomic.AddInt32(&identified, 1)
		return true // would pass the identity check — the spawner check must fire first
	}

	proc := startTestGroup(t, "sleep", "30")
	// Our own test process stands in for the live sibling spawner.
	writePidFile(m.pidFilePath(), proc.Process.Pid, os.Getpid())

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan must not reap a Meridian whose spawner is alive")
	}
	if !groupAlive(proc.Process.Pid) {
		t.Error("sibling's healthy Meridian was killed")
	}
	if pid, spawner, ok := readPidFile(m.pidFilePath()); !ok || pid != proc.Process.Pid || spawner != os.Getpid() {
		t.Error("the sibling's pidfile must be left intact")
	}
	if atomic.LoadInt32(&identified) != 0 {
		t.Error("identity check ran before the spawner-liveness check settled ownership")
	}
}

// A legacy one-line pidfile has an unknown spawner: be conservative, don't
// reap, and preserve the file — the caller adopts the process as External and
// the External watcher handles it dying later.
func TestRealReapOrphan_LegacyPidfileNotReaped(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.identifyGroupFn = func(int) bool { return true }

	proc := startTestGroup(t, "sleep", "30")
	if err := os.WriteFile(m.pidFilePath(), []byte(strconv.Itoa(proc.Process.Pid)), 0o644); err != nil {
		t.Fatalf("write legacy pidfile: %v", err)
	}

	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan must not reap on a legacy pidfile with unknown spawner")
	}
	if !groupAlive(proc.Process.Pid) {
		t.Error("process behind a legacy pidfile was killed")
	}
	if pid, _, ok := readPidFile(m.pidFilePath()); !ok || pid != proc.Process.Pid {
		t.Error("the legacy pidfile must be preserved")
	}
}

// A pidfile whose group pid doesn't parse yields nothing to reap, and the
// reaper leaves the unparseable file alone (never validated its contents).
func TestRealReapOrphan_MalformedPidfileNotReaped(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.identifyGroupFn = func(int) bool { return true }

	if err := os.WriteFile(m.pidFilePath(), []byte("not-a-pid\n123"), 0o644); err != nil {
		t.Fatalf("write malformed pidfile: %v", err)
	}
	if m.realReapOrphan(3456) {
		t.Error("realReapOrphan on a malformed pidfile should be false")
	}
	if _, err := os.Stat(m.pidFilePath()); err != nil {
		t.Error("the malformed pidfile should be left in place")
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

	if _, _, ok := readPidFile(m.pidFilePath()); ok {
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

	if _, _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed when Meridian exits unexpectedly")
	}
}

// If a sibling cercano reaped our Meridian and recorded ITS group pid in the
// shared pidfile, our supervise's exit-path cleanup must not delete it — that
// record is the sibling's orphan tracking, not ours.
func TestUnexpectedExit_LeavesSiblingPidfile(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return nil }
	var spawned int32
	// Long-lived process we kill "from outside", playing the sibling's reap.
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	// The "sibling" reaps our Meridian and records its own in the pidfile.
	m.mu.Lock()
	ourPid := m.cmd.Process.Pid
	m.mu.Unlock()
	writePidFile(m.pidFilePath(), ourPid+1, os.Getpid())
	_ = syscall.Kill(ourPid, syscall.SIGKILL)
	waitForState(t, m, StateFailed) // our supervise sees the unexpected exit

	if pid, _, ok := readPidFile(m.pidFilePath()); !ok || pid != ourPid+1 {
		t.Error("teardown deleted a pidfile it no longer owns")
	}
	m.Stop()
	// Stop's cleanup path must respect the same ownership rule.
	if pid, _, ok := readPidFile(m.pidFilePath()); !ok || pid != ourPid+1 {
		t.Error("Stop deleted a pidfile it no longer owns")
	}
	removePidFile(m.pidFilePath())
}

// Same ownership rule on the clean-Stop path: a sibling overwrote the pidfile
// while we were Ready; our Stop kills our own process but leaves the file.
func TestStop_LeavesSiblingPidfile(t *testing.T) {
	m := New(nil, filepath.Join(t.TempDir(), "meridian.log"))
	m.prereqsFn = func() Prereqs { return Prereqs{NodeOK: true} }
	m.authFn = func() bool { return true }
	m.portUsedFn = func(int) bool { return false }
	m.probeFn = func(context.Context, int) error { return nil }
	var spawned int32
	m.spawnFn = fakeSpawn(&spawned)

	m.Ensure(context.Background(), 3456)
	waitForState(t, m, StateReady)

	m.mu.Lock()
	ourPid := m.cmd.Process.Pid
	m.mu.Unlock()
	writePidFile(m.pidFilePath(), ourPid+1, os.Getpid())

	m.Stop()
	if pid, _, ok := readPidFile(m.pidFilePath()); !ok || pid != ourPid+1 {
		t.Error("Stop deleted a pidfile a sibling had overwritten")
	}
	removePidFile(m.pidFilePath())
}

// A real spawn records a pidfile naming both the process and us as spawner;
// a clean Stop removes it.
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

	pid, spawner, ok := readPidFile(m.pidFilePath())
	if !ok || pid <= 0 {
		t.Fatalf("expected a pidfile after spawn, got (%d,%d,%v)", pid, spawner, ok)
	}
	if spawner != os.Getpid() {
		t.Errorf("pidfile spawner = %d, want our pid %d", spawner, os.Getpid())
	}
	m.Stop()
	if _, _, ok := readPidFile(m.pidFilePath()); ok {
		t.Error("pidfile should be removed after a clean Stop")
	}
}
