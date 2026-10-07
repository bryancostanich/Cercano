//go:build windows

package launch

// TEST-ONLY Windows positive-launch harness.
//
// The default CI context (CI run 37667337045: IsInJob=true, job limit
// flags 0x0) denies EVERY breakaway launch with ERROR_ACCESS_DENIED, so
// a positive Launch test run directly in the test-suite process cannot
// pass there — and must NOT pass there: the refusal is the production
// fail-closed contract working exactly as designed, and the production
// breakaway flags are unchanged. The positives are therefore not skipped
// and not run in the suite process: they run in helper subprocesses that
// first enter a fixture-OWNED permissive job (allow-breakaway,
// kill-on-close; see enterOwnedPermissiveFixtureJob) and only then call
// the production Launch. The suite process, the inherited CI job and
// every global policy remain untouched, and CI 37667337045 proved this
// owned permitted path launches successfully.
//
// The default context itself is covered by the expectation test below:
// in a restrictive context the launch must be REFUSED with the
// classified breakaway denial and no child started (not a generic
// positive failure); in a permissive context the same launch must
// succeed and complete.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// launchEchoOncePositive is the Windows dispatch target for the shared
// launchEchoOnce (see launch_test.go): every echo-once positive runs
// inside the owned permissive job, and the request is always handled
// (a launch refused even from the owned job is reported inside as an
// explicit skip, never a silent pass).
func launchEchoOncePositive(t *testing.T, stdoutPath, stderrPath string, extraBytes int) (int, bool) {
	return launchEchoOnceThroughOwnedHarness(t, stdoutPath, stderrPath, extraBytes)
}

// ownedEchoMain is the harness helper: it enters an owned permissive job,
// launches the echo-once child through the production primitive, observes
// the child through a REAL bound handle (no pid heuristics) and records
// the classified outcome for the test process.
func ownedEchoMain() {
	if *echoResultPath == "" {
		fmt.Fprintln(os.Stderr, "owned-echo: no result path supplied")
		os.Exit(3)
	}
	writeResult := func(text string) {
		_ = os.WriteFile(*echoResultPath, []byte(text+"\n"), 0o600)
	}
	if err := enterOwnedPermissiveFixtureJob(); err != nil {
		writeResult("harness-job-failed: " + err.Error())
		os.Exit(3)
	}
	exe, err := helperExecutable()
	if err != nil {
		writeResult("own-path-failed: " + err.Error())
		os.Exit(3)
	}
	start := time.Now()
	proc, err := Launch(Options{
		Executable: exe,
		Argv: []string{
			"-launch-testprocess-role=" + helperRoleEchoOnce,
			"-launch-testprocess-echo-line=" + *echoLine,
			"-launch-testprocess-echo-bytes=" + strconv.Itoa(*echoBytes),
		},
		StdoutPath: *echoStdoutPath,
		StderrPath: *echoStderrPath,
	})
	if err != nil {
		// Classified, never guessed: the fixture decides whether this is
		// the environment's honest refusal of even the owned permitted
		// path, or a real failure.
		code, interpretation, _ := classifyCreateProcessRefusal(err)
		writeResult(fmt.Sprintf("launch-refused code=%s (%s) job-context=[%s] error=%v",
			code, interpretation, currentProcessJobReport(), err))
		os.Exit(0)
	}
	// Bind the child handle immediately after the launch — the earliest
	// possible moment — and verify it is this test binary's image, so
	// every later observation and the cleanup kill refer to exactly this
	// child and can never hit a recycled pid.
	watch, err := watchFixtureChild(proc.Pid())
	if err != nil {
		writeResult("child-bind-failed: " + err.Error())
		os.Exit(3)
	}
	defer watch.Close() //nolint:errcheck // the helper is exiting anyway
	// Bounded exit observation through the bound handle: errors fail,
	// a child that never exits is terminated through the same handle.
	exited := false
	deadline := time.Now().Add(30 * time.Second)
	for !exited {
		gone, werr := watch.Exited()
		if werr != nil {
			writeResult("child-wait-failed: " + werr.Error())
			os.Exit(3)
		}
		if gone {
			exited = true
			break
		}
		if time.Now().After(deadline) {
			watch.TerminateIfRunning()
			writeResult("child-hung: echo-once child did not exit within 30s")
			os.Exit(3)
		}
		time.Sleep(10 * time.Millisecond)
	}
	writeResult(fmt.Sprintf("ok pid=%d elapsed-ms=%d", proc.Pid(), time.Since(start).Milliseconds()))
	os.Exit(0)
}

// launchEchoOnceThroughOwnedHarness runs the echo-once positive inside an
// owned permissive job in a helper subprocess and asserts its classified
// result. It returns the launched child pid and true (the request was
// handled); on Windows the direct-launch path in launch_test.go is never
// reached for positives.
func launchEchoOnceThroughOwnedHarness(t *testing.T, stdoutPath, stderrPath string, extraBytes int) (int, bool) {
	t.Helper()
	dir := t.TempDir()
	resultPath := filepath.Join(dir, "echo-result")
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	argv := []string{
		"-launch-testprocess-role=" + helperRoleOwnedEcho,
		"-launch-testprocess-echo-result=" + resultPath,
		"-launch-testprocess-echo-stdout=" + stdoutPath,
		"-launch-testprocess-echo-stderr=" + stderrPath,
		"-launch-testprocess-echo-line=fixture-line",
		"-launch-testprocess-echo-bytes=" + strconv.Itoa(extraBytes),
	}
	harness := exec.Command(exe, argv...)
	if err := harness.Start(); err != nil {
		t.Fatalf("starting owned-echo harness: %v", err)
	}
	t.Cleanup(func() {
		_ = harness.Process.Kill() // no-op if it already exited
		_, _ = harness.Process.Wait()
	})

	result := strings.TrimSpace(waitForFile(t, resultPath, 60*time.Second))
	switch {
	case strings.HasPrefix(result, "ok pid="):
		fields := strings.Fields(result)
		pid, perr := strconv.Atoi(strings.TrimPrefix(fields[1], "pid="))
		if perr != nil {
			t.Fatalf("unreadable harness result %q: %v", result, perr)
		}
		return pid, true
	case strings.HasPrefix(result, "launch-refused code=access-denied"):
		// The real OS denied the launch even from the owned permitted
		// path: this environment's outer job policy makes the positive
		// unprovable here. Reported explicitly — never a silent pass,
		// never a claimed escape.
		t.Skipf("host job policy denies the launch even from the owned permissive fixture job; positive launch cannot be proven in this environment (result: %s)", result)
		return 0, true
	default:
		t.Fatalf("owned-echo harness failed: %s", result)
		return 0, true
	}
}

// TestOneShotLaunch_DefaultContextRefusalClassifiedNoChild encodes the
// EXPECTED behavior of a launch attempted from this process's own,
// unmodified default context (on CI: the inherited job with limit flags
// 0x0 that CI run 37667337045 showed denies every breakaway).
//
//   - Restrictive default context: Launch must be REFUSED with the
//     classified breakaway denial (access-denied), and NO child may
//     exist. CreateProcess refusal is atomic, so the only observable a
//     started child would leave — its fixed log line — must never
//     appear. This is the production fail-closed contract, verified as
//     an expectation, not a generic positive-test failure.
//   - Permissive default context (e.g. a local host outside a
//     restrictive job chain, where CREATE_BREAKAWAY_FROM_JOB is a
//     documented no-op): the launch must succeed and the child must
//     complete, observed through a bound handle.
func TestOneShotLaunch_DefaultContextRefusalClassifiedNoChild(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "default-out.log")
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv: []string{
			"-launch-testprocess-role=" + helperRoleEchoOnce,
			"-launch-testprocess-echo-line=default-context-line",
		},
		StdoutPath: out,
	})
	if err != nil {
		code, interpretation, unexpected := classifyCreateProcessRefusal(err)
		if unexpected {
			t.Fatalf("Launch refused with an unexpected code %q for a verified trusted test binary: %v", code, err)
		}
		if code != "access-denied" {
			t.Errorf("Launch refused with code %q, want the classified breakaway denial (access-denied) in a restrictive default context: %v", code, err)
		}
		t.Logf("default context refused the launch as classified (code=%s: %s) — fail-closed contract verified: %v",
			code, interpretation, err)
		// No child may exist: CreateProcess refusal is atomic. The log
		// file exists (the parent opens it before CreateProcess) but
		// must stay empty — a started child writes its line within
		// milliseconds.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if data, rerr := os.ReadFile(out); rerr == nil && len(data) > 0 {
				t.Fatalf("a child was started despite the refused launch: log = %q", data)
			}
			time.Sleep(25 * time.Millisecond)
		}
		return
	}

	// Permissive default context: the launch succeeded, so the child must
	// run to completion. Observed through a bound, image-verified handle —
	// never a pid heuristic.
	watch, err := watchFixtureChild(proc.Pid())
	if err != nil {
		t.Fatalf("binding default-context child: %v", err)
	}
	defer watch.Close() //nolint:errcheck // test-scoped handle
	confirmedGone := false
	t.Cleanup(func() {
		if !confirmedGone {
			watch.TerminateIfRunning()
		}
	})
	data := waitForFile(t, out, 10*time.Second)
	if !strings.Contains(data, "default-context-line") {
		t.Errorf("default-context child log = %q, want its fixed line", data)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gone, werr := watch.Exited()
		if werr != nil {
			t.Fatalf("watching default-context child: %v", werr)
		}
		if gone {
			confirmedGone = true
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Errorf("default-context child %d still present after completing", proc.Pid())
}
