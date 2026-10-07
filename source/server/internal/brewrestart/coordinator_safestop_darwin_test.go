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
	"strings"
	"testing"
	"time"
)

// startCoordinatorFixture builds a two-version Cellar layout rooted in a
// temp directory, starts the OLD version's process as a test fixture agent
// (mode: "", "busy", "lost", "stall", or "old") bound to a reserved loopback
// endpoint, and registers cleanup. It never touches a developer's live
// agent: discovery is restricted to the fixture PID via fixtureProcesses,
// never the developer's real processes.
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

// Active update-relevant work at the restart deadline: the client's
// DeadlineExceeded proves nothing — the agent may have committed the stop
// just before the deadline with the confirmation lost. The coordinator
// therefore reports the typed UNCERTAIN outcome (never a busy claim), the
// agent is not force-stopped, no replacement is started, no legacy bounce
// is attempted, and the bounded post-install deadline leaves the state
// unconfirmed rather than claiming "left running".
func TestNativeCoordinatorBusyDeadlineIsUncertainWithoutForce(t *testing.T) {
	root, newExe, endpoint, old := startCoordinatorFixture(t, "busy")
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if restarted || !isSafeStopUncertain(err) {
		t.Fatalf("restarted=%v err=%v, want typed uncertain diagnostic", restarted, err)
	}
	if isSafeStopUnsupported(err) {
		t.Fatalf("deadline misclassified as definitive unsupported: %v", err)
	}
	if strings.Contains(err.Error(), "busy") {
		t.Fatalf("err=%v must not claim busy from the client deadline alone", err)
	}
	if *starts != 0 {
		t.Fatalf("started %d replacement agents while work was in flight", *starts)
	}
	if !fixtureStillOwns(t, old, endpoint) {
		t.Fatal("agent was force-stopped during an unconfirmed wait")
	}
	if _, err := os.Stat(filepath.Join(root, "2-ready")); !os.IsNotExist(err) {
		t.Fatal("replacement agent was started during a busy wait")
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was called after a busy wait")
	}
}

// Regression: the agent commits the safe stop just before the deadline and
// the confirmation is lost in transit (Unavailable). The client-side ending
// is ambiguous, so the coordinator resolves it with a fresh bounded local
// inspection under the held launch lock: the verified PID is positively
// gone, so the EXISTING restart continues — wait-exit, start replacement,
// readiness — without ever falling back to the legacy bounce.
func TestNativeCoordinatorCommittedStopLostConfirmationStillRestarts(t *testing.T) {
	root, newExe, endpoint, old := startCoordinatorFixture(t, "lost")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if err != nil || !restarted {
		t.Fatalf("restarted=%v err=%v, want the existing restart to complete", restarted, err)
	}
	if *starts != 1 {
		t.Fatalf("started %d replacement agents, want exactly one", *starts)
	}
	// The committed stop was for the verified PID only.
	pidBytes, err := os.ReadFile(filepath.Join(root, "safe-stop-pid"))
	if err != nil {
		t.Fatalf("no safe stop committed: %v", err)
	}
	if got, want := string(pidBytes), strconv.Itoa(old.Process.Pid); got != want {
		t.Fatalf("safe-stop-pid=%q want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was used to resolve the ambiguous ending")
	}
	waitFixtureFile(t, filepath.Join(root, "2-ready"))
}

// The transport drops (Unavailable) without a commit and the exact same
// agent process is verifiably still alive: nothing may be forced, no
// replacement may be started, and the coordinator reports the unconfirmed
// state with clear guidance — never a busy claim or a forced stop.
func TestNativeCoordinatorUncertainAliveAgentIsNeverForced(t *testing.T) {
	root, newExe, endpoint, old := startCoordinatorFixture(t, "stall")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ops := nativeRestartOps()
	ops.source = fixtureProcesses{pid: old.Process.Pid} // never enumerate/inspect the developer's processes
	starts := countingStarts(&ops)
	restarted, err := coordinateRestart(ctx, ops, newExe, uint32(os.Getuid()), endpoint)
	if restarted || !isSafeStopUncertain(err) {
		t.Fatalf("restarted=%v err=%v, want typed uncertain diagnostic", restarted, err)
	}
	if !strings.Contains(err.Error(), "same agent was present at the last inspection") {
		t.Fatalf("err=%v, want explicit last-observation guidance", err)
	}
	if *starts != 0 {
		t.Fatalf("started %d replacement agents although the agent never stopped", *starts)
	}
	if !fixtureStillOwns(t, old, endpoint) {
		t.Fatal("alive agent was stopped despite an unconfirmed outcome")
	}
	if _, err := os.Stat(filepath.Join(root, "safe-stop-pid")); !os.IsNotExist(err) {
		t.Fatal("safe stop must not be recorded for a dropped transport")
	}
	if _, err := os.Stat(filepath.Join(root, "2-ready")); !os.IsNotExist(err) {
		t.Fatal("replacement agent was started although the agent never stopped")
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-called")); !os.IsNotExist(err) {
		t.Fatal("legacy ShutdownAgent was used as an uncertainty fallback")
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
