//go:build darwin && cgo

package brewrestart

import (
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// startCoordinatorFixture builds a two-version Cellar layout rooted in a
// temp directory, starts the OLD version's process as a test fixture agent
// (mode: "", "busy", or "old") bound to a reserved loopback endpoint, and
// registers cleanup. It never touches a developer's live agent: discovery is
// restricted to the fixture PID via fixtureProcesses, never the developer's
// real processes.
func startCoordinatorFixture(t *testing.T, mode string) (string, string, netip.AddrPort, *exec.Cmd) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	oldExe := filepath.Join(root, "Cellar", "cercano", "1", "bin", "cercano")
	newExe := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
	for _, path := range []string{oldExe, newExe} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0700); err != nil {
			t.Fatal(err)
		}
	}
	reserve, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := netip.MustParseAddrPort(reserve.Addr().String())
	_ = reserve.Close()
	old := exec.Command(oldExe, "agent")
	old.Dir = root
	old.Env = []string{
		"HOME=" + root,
		"TMPDIR=" + root,
		"CERCANO_COORD_FIXTURE=1",
		"CERCANO_COORD_FIXTURE_MODE=" + mode,
		"CERCANO_COORD_ROOT=" + root,
		"CERCANO_COORD_ENDPOINT=" + endpoint.String(),
	}
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = old.Wait(); close(done) }()
	t.Cleanup(func() { _ = old.Process.Kill(); <-done })
	_ = os.WriteFile(filepath.Join(root, "old-pid"), []byte(strconv.Itoa(old.Process.Pid)), 0600)
	waitFixtureFile(t, filepath.Join(root, "1-ready"))
	return root, newExe, endpoint, old
}

// fixtureStillOwns reports whether the fixture agent is still the same live
// process holding the endpoint's listener.
func fixtureStillOwns(t *testing.T, old *exec.Cmd, endpoint netip.AddrPort) bool {
	t.Helper()
	id, err := Inspect(old.Process.Pid)
	if err != nil {
		return false
	}
	listens, err := HoldsListener(id, endpoint)
	return err == nil && listens
}

// countingStarts wraps the ops.start adapter so a test can assert exactly
// how many replacement agents were (not) started.
func countingStarts(ops *restartOps) *int {
	count := new(int)
	realStart := ops.start
	ops.start = func(path string, state LaunchState) (Identity, error) {
		*count++
		return realStart(path, state)
	}
	return count
}

// Active update-relevant work at the restart deadline: the agent is LEFT
// RUNNING, no replacement is started, no legacy bounce is attempted, and
// the failure carries the typed busy diagnostic.
func TestNativeCoordinatorBusyAgentIsLeftRunning(t *testing.T) {
	root, newExe, endpoint, old := startCoordinatorFixture(t, "busy")
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if restarted || !isSafeStopBusy(err) {
		t.Fatalf("restarted=%v err=%v, want typed busy diagnostic", restarted, err)
	}
	if *starts != 0 {
		t.Fatalf("started %d replacement agents while work was in flight", *starts)
	}
	if !fixtureStillOwns(t, old, endpoint) {
		t.Fatal("busy agent was stopped or lost its listener")
	}
	if _, err := os.Stat(filepath.Join(root, "2-ready")); !os.IsNotExist(err) {
		t.Fatal("replacement agent was started during a busy wait")
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was called after a busy wait")
	}
}

// A released agent predating the safe-stop RPC answers Unimplemented: it is
// LEFT RUNNING, nothing is restarted, and the coordinator must NEVER fall
// back to the legacy ShutdownAgent bounce.
func TestNativeCoordinatorOldAgentUnsupportedIsLeftRunning(t *testing.T) {
	root, newExe, endpoint, old := startCoordinatorFixture(t, "old")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if restarted || !isSafeStopUnsupported(err) {
		t.Fatalf("restarted=%v err=%v, want typed unsupported diagnostic", restarted, err)
	}
	if *starts != 0 {
		t.Fatalf("started %d replacement agents for an unsupported agent", *starts)
	}
	if !fixtureStillOwns(t, old, endpoint) {
		t.Fatal("old agent was stopped despite Unimplemented")
	}
	if _, err := os.Stat(filepath.Join(root, "2-ready")); !os.IsNotExist(err) {
		t.Fatal("replacement agent was started for an unsupported agent")
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was used as an Unimplemented fallback")
	}
}

// No agent => no start: with nothing owned by this installation listening
// on the endpoint, the native coordinate path must report (false, nil) and
// must not start or stop anything. The candidate source is the fixture seam
// restricted to a non-existent PID so no real user process is enumerated or
// inspected.
func TestRestartInstalledNoAgentStartsNothing(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	newExe := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
	if err := os.MkdirAll(filepath.Dir(newExe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newExe, contents, 0700); err != nil {
		t.Fatal(err)
	}
	reserve, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := netip.MustParseAddrPort(reserve.Addr().String())
	_ = reserve.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: 0} // no candidate: never enumerates the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if restarted || err != nil {
		t.Fatalf("restarted=%v err=%v, want no start and no error when no agent exists", restarted, err)
	}
	if *starts != 0 {
		t.Fatalf("started %d agents although none was running", *starts)
	}
	// Nothing was started on the endpoint by the no-agent path.
	conn, err := net.DialTimeout("tcp", endpoint.String(), 250*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("an agent was started although none was running")
	}
}
