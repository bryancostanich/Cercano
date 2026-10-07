package launch

// Owned Go helper processes ("testprocess") for the one-shot launch
// fixtures. These helpers run only inside THIS package's test binary; no
// real installation, developer agent or package manager is involved.
//
//   - intermediate-parent: launches the final child via Launch, records the
//     child pid, then performs every parent-side shutdown action available
//     to the real initiating app — cancel its own context, close all its
//     stdio, exit — none of which may stop the child.
//   - final-child: waits until the intermediate parent is gone, then proves
//     it survived by writing to its inherited output stream and a
//     completion marker.
//   - echo-once: a short-lived direct child for the stream-semantics tests.
//
// Every helper the fixture starts is reaped or terminated on every path.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	helperRoleNone               = ""
	helperRoleIntermediateParent = "intermediate-parent"
	helperRoleFinalChild         = "final-child"
	helperRoleEchoOnce           = "echo-once"
	helperRoleJobParent          = "job-parent"
	helperRoleProbeChild         = "probe-child"
	helperRoleOwnedEcho          = "owned-echo"
	helperRoleHoldChild          = "hold-child"
)

var (
	helperRole = flag.String("launch-testprocess-role", helperRoleNone, "internal fixture role")

	// intermediate-parent and job-parent inputs (paths supplied by the
	// test):
	childPidFilePath     = flag.String("launch-testprocess-pidfile", "", "intermediate/job parent: where to record the launched child pid")
	childLogPath         = flag.String("launch-testprocess-child-log", "", "intermediate/job parent: child stdout/stderr log path")
	parentDoneMarkerPath = flag.String("launch-testprocess-parent-done", "", "parent exit marker path")
	completionMarkerPath = flag.String("launch-testprocess-completion", "", "final child completion marker path")

	// intermediate-parent TEST-ONLY failure reporting (path supplied by
	// the test): where an immediately failed Launch is persisted so the
	// fixture fails fast with the classified error instead of timing out
	// on the pidfile. See persistStartError.
	startErrorPath = flag.String("launch-testprocess-start-error", "", "intermediate parent: where to persist an immediately refused Launch")

	// probe-child inputs (Windows CreateProcess flag diagnostics; see
	// launch_diag_windows_test.go):
	probeMarkerPath = flag.String("launch-testprocess-probe-marker", "", "probe-child: known temp marker path to write and exit")

	// job-parent inputs (Windows job-object fixtures; see
	// testprocess_windows_test.go):
	jobVariant    = flag.String("launch-testprocess-job-variant", "", "job-parent: \"deny\" (breakaway forbidden) or \"allow\" (breakaway permitted)")
	jobResultPath = flag.String("launch-testprocess-job-result", "", "job-parent: where to record the launch outcome")

	// owned-echo harness inputs (Windows owned permissive job launch
	// context; see launch_harness_windows_test.go):
	echoResultPath = flag.String("launch-testprocess-echo-result", "", "owned-echo harness: where to record the launch outcome")
	echoStdoutPath = flag.String("launch-testprocess-echo-stdout", "", "owned-echo harness: child stdout log path (empty discards)")
	echoStderrPath = flag.String("launch-testprocess-echo-stderr", "", "owned-echo harness: child stderr log path (empty discards)")

	// bound-handshake inputs (Windows handle-based liveness; see
	// launch_liveness_windows_test.go):
	parentBoundMarkerPath = flag.String("launch-testprocess-parent-bound", "", "final child: where to record that it holds the parent's real process handle")
	childReleasePath      = flag.String("launch-testprocess-child-release", "", "final child: fixture-written release path; the child exits only after completing AND being released")

	// echo-once inputs:
	echoLine  = flag.String("launch-testprocess-echo-line", "fixture-line", "echo-once: fixed line to write")
	echoBytes = flag.Int("launch-testprocess-echo-bytes", 0, "echo-once: extra bytes written to stdout before exit")

	// final-child inputs:
	parentPID = flag.Int("launch-testprocess-parent-pid", 0, "final child: pid of the intermediate parent to outlive")
)

// TestMain dispatches the helper roles before running the tests. flag.Parse
// runs first because helper invocations never reach m.Run; testing's own
// -test.* flags are already registered by the time TestMain executes.
func TestMain(m *testing.M) {
	flag.Parse()
	switch *helperRole {
	case helperRoleNone:
		os.Exit(m.Run())
	case helperRoleIntermediateParent:
		intermediateParentMain()
	case helperRoleFinalChild:
		finalChildMain()
	case helperRoleEchoOnce:
		echoOnceMain()
	case helperRoleJobParent:
		jobParentMain()
	case helperRoleProbeChild:
		probeChildMain()
	case helperRoleOwnedEcho:
		ownedEchoMain()
	case helperRoleHoldChild:
		holdChildMain()
	default:
		fmt.Fprintln(os.Stderr, "testprocess: unknown role:", *helperRole)
		os.Exit(2)
	}
}

// helperExecutable returns this test binary as an absolute path — the same
// contract the production caller must satisfy, since Launch refuses
// relative paths and never resolves anything itself.
func helperExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(exe) {
		abs, aerr := filepath.Abs(exe)
		if aerr != nil {
			return "", aerr
		}
		exe = abs
	}
	return exe, nil
}

// intermediateParentMain is the initiating parent of the fixture chain. It
// launches the final child through the primitive, records the child pid
// for the fixture, then performs everything a dying or cancelling
// initiating app can do to the child. None of it may terminate the child.
func intermediateParentMain() {
	exe, err := helperExecutable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "intermediate: own path:", err)
		os.Exit(3)
	}
	// Windows: enter a fixture-OWNED permissive job (allow-breakaway,
	// kill-on-close) BEFORE the launch, so the production breakaway
	// launch is attempted from a context the OS permits. CI run
	// 37667337045 proved exactly this: the inherited CI job (limit
	// flags 0x0) denies every breakaway variant, while a launch from a
	// helper inside its own permissive job SUCCEEDS. The job belongs to
	// this helper process and dies with it; the inherited CI job and
	// every global policy are untouched. No-op on other platforms.
	if err := enterOwnedPermissiveFixtureJob(); err != nil {
		persistStartError(err)
		fmt.Fprintln(os.Stderr, "intermediate: owned permissive fixture job:", err)
		os.Exit(3)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv: []string{
			"-launch-testprocess-role=" + helperRoleFinalChild,
			"-launch-testprocess-completion=" + *completionMarkerPath,
			"-launch-testprocess-parent-done=" + *parentDoneMarkerPath,
			"-launch-testprocess-parent-bound=" + *parentBoundMarkerPath,
			"-launch-testprocess-child-release=" + *childReleasePath,
			"-launch-testprocess-parent-pid=" + strconv.Itoa(os.Getpid()),
		},
		StdoutPath: *childLogPath,
		StderrPath: *childLogPath,
	})
	if err != nil {
		// TEST-ONLY failure reporting: persist the refused launch
		// IMMEDIATELY, before any other failure mode can occur. Without
		// this the fixture can only observe the missing pidfile, and a
		// refused launch surfaces as an opaque 20-second timeout (this
		// helper's stderr is invisible: os/exec connects it to the null
		// device). The persisted report carries the classified
		// CreateProcess outcome plus this helper's own native job
		// context on Windows (see launchFailureReport).
		persistStartError(err)
		fmt.Fprintln(os.Stderr, "intermediate: launch:", err)
		os.Exit(3)
	}

	// Cancel the initiating app's own operation context — a disconnected
	// UI or an explicit cancel. Launch deliberately accepts no context, so
	// no cancellation channel can reach the child; this call exists to
	// prove the wiring, not to transmit anything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx

	// Close all of this parent's stdio. The child holds none of these
	// descriptors: its stdin is the null device and its output goes to the
	// caller-owned log file, so nothing here blocks or terminates it.
	_ = os.Stdin.Close()
	_ = os.Stdout.Close()
	_ = os.Stderr.Close()

	// Bound handshake (Windows; no-op elsewhere): do not exit until the
	// child has provably opened a real handle to this process. Without
	// it the child's OpenProcess could race this process's teardown and
	// the test fixture's handle close, and the parent-death proof would
	// be nondeterministic. A timeout is a persisted failure, never a
	// silently assumed bind.
	if err := awaitChildParentBound(); err != nil {
		persistStartError(fmt.Errorf("child bound-handshake: %w", err))
		fmt.Fprintln(os.Stderr, "intermediate: child bound-handshake:", err)
		os.Exit(3)
	}

	// Signal the fixture, then exit. The child observes the death itself
	// (getppid on Unix, this marker plus a grace period elsewhere). The
	// marker is published BEFORE the pidfile so the fixture can safely
	// hard-kill this process as soon as the pidfile appears. Both are
	// published ATOMICALLY (see publishFixtureFile): the fixture polls
	// them, and a created-but-empty or partial file would race the poll
	// (the failure mode CI run 37701866968 proved on the owned-echo
	// result).
	if err := publishFixtureFile(*parentDoneMarkerPath, []byte("gone")); err != nil {
		fmt.Fprintln(os.Stderr, "intermediate: parent-done marker:", err)
		os.Exit(3)
	}
	if err := publishFixtureFile(*childPidFilePath, []byte(strconv.Itoa(proc.Pid()))); err != nil {
		fmt.Fprintln(os.Stderr, "intermediate: pidfile:", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// persistStartError is TEST-ONLY failure reporting: it writes the refused
// Launch to the fixture-supplied start-error path immediately (see the
// intermediateParentMain call site). launchFailureReport supplies the
// platform-specific detail: the classified CreateProcess outcome plus the
// calling helper's own job context on Windows, or the plain error
// elsewhere. It never changes any launch behavior: writing the report is
// the only effect.
func persistStartError(err error) {
	if *startErrorPath == "" {
		return
	}
	// Atomic publication (see publishFixtureFile): the fixture polls this
	// file, and a created-but-empty or partial write would race the poll.
	if perr := publishFixtureFile(*startErrorPath,
		[]byte("start-failed: "+launchFailureReport(err)+"\n")); perr != nil {
		fmt.Fprintln(os.Stderr, "intermediate: persisting start error:", perr)
	}
}

// finalChildMain waits until its initiating parent is genuinely gone, then
// proves it survived by writing to its inherited output stream FIRST — the
// log must receive this after the parent died — and only then the
// completion marker.
func finalChildMain() {
	if *parentPID <= 0 {
		fmt.Fprintln(os.Stderr, "child: no parent pid supplied")
		os.Exit(3)
	}
	// The expected parent pid is passed explicitly: this process may start
	// after its parent already exited, when the kernel has already
	// reparented us. Reading getppid() here would then record the
	// reparented parent and never observe the change.
	expectedParent := *parentPID
	// Parent-death proof is platform-native and authoritative: kernel
	// reparenting on Unix, a REAL process handle + wait on Windows (the
	// former marker-plus-500ms-grace heuristic could observe a living
	// parent as gone; a signaled handle cannot). Any error fails this
	// child loudly — death is never assumed.
	if err := waitForParentGone(expectedParent); err != nil {
		fmt.Fprintln(os.Stderr, "child: parent-gone proof:", err)
		os.Exit(4)
	}
	fmt.Println("child-alive-after-parent-exit " + sessionIndependenceLine())
	// The completion marker is polled by the fixture, so it is published
	// ATOMICALLY (see publishFixtureFile): complete or absent, never a
	// created-but-empty or partial file racing the poll.
	if err := publishFixtureFile(*completionMarkerPath, []byte("complete")); err != nil {
		fmt.Fprintln(os.Stderr, "child: completion marker:", err)
		os.Exit(3)
	}
	// Bound release (Windows; no-op elsewhere): after the completion
	// marker this child stays alive until the fixture — which by then
	// holds a handle bound to this exact process — releases it. That
	// makes the fixture's exit observation deterministic and its
	// cleanup kill immune to pid reuse (no post-exit pid signalling).
	if err := holdForFixtureRelease(); err != nil {
		fmt.Fprintln(os.Stderr, "child: release:", err)
		os.Exit(4)
	}
	os.Exit(0)
}

// echoOnceMain is a short-lived direct child: it writes a fixed line to
// stdout and stderr, optionally a large volume to stdout to prove a
// non-pipe sink never blocks, and exits 0.
func echoOnceMain() {
	fmt.Println(*echoLine)
	fmt.Fprintln(os.Stderr, *echoLine+"-stderr")
	if *echoBytes > 0 {
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'x'
		}
		written := 0
		for written < *echoBytes {
			n, err := os.Stdout.Write(chunk)
			written += n
			if err != nil || n <= 0 {
				break
			}
		}
	}
	os.Exit(0)
}

// holdChildMain is the small liveness-probe child: it writes its
// completion marker FIRST, then stays alive until the fixture releases it
// through the release path (the same bound holdForFixtureRelease used by
// the final child), then exits 0. The deterministic hold makes it the
// probe target for the handle-liveness proof: a fixture can bind a real
// handle while the child is provably alive, verify the handle is NOT
// signaled, then release and observe the signaled exit object — exactly
// the Windows "exit object can exist and be signaled" contract (see
// launch_liveness_windows_test.go).
func holdChildMain() {
	if *completionMarkerPath == "" || *childReleasePath == "" {
		fmt.Fprintln(os.Stderr, "hold-child: no marker or release path supplied")
		os.Exit(3)
	}
	// The marker is polled by the fixture, so it is published ATOMICALLY
	// (see publishFixtureFile): complete or absent, never a created-but-
	// empty or partial file racing the poll.
	if err := publishFixtureFile(*completionMarkerPath, []byte("complete")); err != nil {
		fmt.Fprintln(os.Stderr, "hold-child: marker:", err)
		os.Exit(3)
	}
	if err := holdForFixtureRelease(); err != nil {
		fmt.Fprintln(os.Stderr, "hold-child: release:", err)
		os.Exit(4)
	}
	os.Exit(0)
}
