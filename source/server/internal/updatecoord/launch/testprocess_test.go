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
	"time"
)

const (
	helperRoleNone               = ""
	helperRoleIntermediateParent = "intermediate-parent"
	helperRoleFinalChild         = "final-child"
	helperRoleEchoOnce           = "echo-once"
)

var (
	helperRole = flag.String("launch-testprocess-role", helperRoleNone, "internal fixture role")

	// intermediate-parent inputs (paths supplied by the test):
	childPidFilePath     = flag.String("launch-testprocess-pidfile", "", "intermediate parent: where to record the launched child pid")
	childLogPath         = flag.String("launch-testprocess-child-log", "", "intermediate parent: child stdout/stderr log path")
	parentDoneMarkerPath = flag.String("launch-testprocess-parent-done", "", "parent exit marker path")
	completionMarkerPath = flag.String("launch-testprocess-completion", "", "final child completion marker path")

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
	proc, err := Launch(Options{
		Executable: exe,
		Argv: []string{
			"-launch-testprocess-role=" + helperRoleFinalChild,
			"-launch-testprocess-completion=" + *completionMarkerPath,
			"-launch-testprocess-parent-done=" + *parentDoneMarkerPath,
			"-launch-testprocess-parent-pid=" + strconv.Itoa(os.Getpid()),
		},
		StdoutPath: *childLogPath,
		StderrPath: *childLogPath,
	})
	if err != nil {
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

	// Signal the fixture, then exit. The child observes the death itself
	// (getppid on Unix, this marker plus a grace period elsewhere). The
	// marker is written BEFORE the pidfile so the fixture can safely
	// hard-kill this process as soon as the pidfile appears.
	if err := os.WriteFile(*parentDoneMarkerPath, []byte("gone"), 0o644); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(*childPidFilePath, []byte(strconv.Itoa(proc.Pid())), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "intermediate: pidfile:", err)
		os.Exit(3)
	}
	os.Exit(0)
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
	gone := false
	deadline := time.Now().Add(60 * time.Second)
	for !gone && time.Now().Before(deadline) {
		gone = parentIsGone(expectedParent)
		if !gone {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !gone {
		fmt.Fprintln(os.Stderr, "child: intermediate parent never exited")
		os.Exit(4)
	}
	fmt.Println("child-alive-after-parent-exit " + sessionIndependenceLine())
	if err := os.WriteFile(*completionMarkerPath, []byte("complete"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "child: completion marker:", err)
		os.Exit(3)
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
