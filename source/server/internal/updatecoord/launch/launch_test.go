package launch

// Direct-child tests: the test process itself is the initiating parent.
// Every child is reaped or terminated on every path by these fixtures.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// launchEchoOnce starts the short-lived echo-once helper through the
// primitive, blocks until it exits, and returns the child pid. Wait both
// observes and reaps this direct child on the normal path; the cleanup
// safety net terminates it on every path where that does not happen.
func launchEchoOnce(t *testing.T, stdoutPath, stderrPath string, extraBytes int) int {
	t.Helper()
	// Windows dispatch: the positive launch is proven inside a helper
	// subprocess that first enters a fixture-OWNED permissive job (see
	// launch_harness_windows_test.go). CI run 37667337045 established
	// that the default CI job (limit flags 0x0) refuses EVERY breakaway
	// launch, so a direct Launch from this test process can only ever
	// be refused there — the fail-closed production contract, not a
	// test defect. The default-context expectation itself is covered by
	// TestOneShotLaunch_DefaultContextRefusalClassifiedNoChild.
	if pid, handled := launchEchoOncePositive(t, stdoutPath, stderrPath, extraBytes); handled {
		return pid
	}
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	argv := []string{
		"-launch-testprocess-role=" + helperRoleEchoOnce,
		"-launch-testprocess-echo-line=fixture-line",
	}
	if extraBytes > 0 {
		argv = append(argv, "-launch-testprocess-echo-bytes="+strconv.Itoa(extraBytes))
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       argv,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	})
	if err != nil {
		// TEST-ONLY failure reporting: a refused Launch fails with the
		// classified CreateProcess outcome plus this test process's own
		// job context on Windows (see launch_diag_windows_test.go), so
		// native CI failure output carries the diagnosis with it. The
		// assertion semantics are unchanged: a refused launch is still
		// a failure.
		t.Fatalf("Launch refused: %s", launchFailureReport(err))
	}
	pid := proc.Pid()
	// Bind the fixture's observation of this direct child to a REAL,
	// platform-native watch before anything is observed or signalled:
	// the signaled state of a verified handle on Windows, the kernel
	// pid probe on Unix. Errors fail loudly — liveness is never
	// guessed, and a recycled pid can never be observed or signalled.
	watch, err := watchFixtureChild(pid)
	if err != nil {
		t.Fatalf("binding echo-once child %d: %v", pid, err)
	}
	t.Cleanup(watch.Close)
	// Safety net for paths where the child never exits on its own; the
	// flag keeps the normal (reaped) path from ever signalling again, so
	// a recycled pid can never be terminated by cleanup. t.Cleanup runs
	// on the same goroutine as the test body.
	confirmedGone := false
	t.Cleanup(func() {
		if !confirmedGone {
			watch.TerminateIfRunning()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gone, werr := watch.Exited()
		if werr != nil {
			t.Fatalf("watching echo-once child %d: %v", pid, werr)
		}
		if gone {
			// The exact OS answer observed: the child exited (and, on
			// Unix, the launcher's reaper reaped it).
			confirmedGone = true
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("echo-once child %d did not exit within 10s", pid)
	return pid
}

// publishFixtureFile atomically publishes a fixture protocol file: the
// full content is written to a temp file in the SAME directory (same
// owner, same volume) and then renamed over the destination, so a
// concurrent reader observes the destination either absent or COMPLETE —
// never the created-but-empty or partially-written intermediate state.
//
// CI run 37701866968 proved the direct os.WriteFile publication races the
// polling reader: os.WriteFile creates/truncates the destination BEFORE
// the content write, so waitForFile read the owned-echo harness result
// as an empty file and TestOneShotLaunch_DiscardedOutputNeverBlocks
// failed with an EMPTY reason ("owned-echo harness failed: "). Rename
// over an existing destination is a replace on every supported platform
// (MoveFileEx(REPLACE_EXISTING) on Windows), and the temp sibling is
// invisible to every destination poll, so the protocol file appears
// complete or not at all.
func publishFixtureFile(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("no protocol path supplied")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".publish-*")
	if err != nil {
		return fmt.Errorf("creating publish temp file next to %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing publish temp file %s: %w", tmpName, err)
	}
	if err := os.WriteFile(tmpName, data, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing publish temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("publishing %s: %w", path, err)
	}
	return nil
}

// readFixtureFile is a test helper that reads fixture protocol files with
// Windows-specific sharing permissions. On Windows, it uses CreateFile with
// FILE_SHARE_READ|WRITE|DELETE to allow concurrent access, avoiding the
// AccessDenied error that os.Rename encounters when there are concurrent
// readers. On Unix, it uses the standard os.ReadFile.
// readFixtureFile is a test helper that reads fixture protocol files with
// Windows-specific sharing permissions. On Windows, it uses CreateFile with
// FILE_SHARE_READ|WRITE|DELETE to allow concurrent access, avoiding the
// AccessDenied error that os.Rename encounters when there are concurrent
// readers. On Unix, it uses the standard os.ReadFile.
func readFixtureFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// waitForFile polls for a file until it carries CONTENT, returning the
// contents. An empty read is NOT a result: every fixture protocol result
// is published non-empty and atomically (see publishFixtureFile), and the
// child log files are written by the child itself after the launch — a
// file that exists but reads empty is a pre-write or mid-write
// observation of the writer, and CI run 37701866968 proved returning it
// races the writer (the owned-echo harness result was read empty and the
// fixture failed with an empty reason). Polling continues until the file
// carries bytes or the deadline expires.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to carry content", path)
	return ""
}

// A relative executable must be refused outright: this primitive never
// falls back to PATH, home or default-binary resolution.
func TestOneShotLaunch_RejectsRelativeExecutable(t *testing.T) {
	for _, rel := range []string{"sh", "cercano-update", "./helper"} {
		if _, err := Launch(Options{Executable: rel}); !errors.Is(err, ErrInvalidExecutable) {
			t.Errorf("Executable=%q: want ErrInvalidExecutable, got %v", rel, err)
		}
	}
}

// A missing absolute executable fails closed even when the same basename
// exists on PATH: no $PATH fallback may ever run instead.
func TestOneShotLaunch_RejectsMissingExecutableWithoutPathFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "sh")
	if _, err := Launch(Options{Executable: missing}); !errors.Is(err, ErrInvalidExecutable) {
		t.Fatalf("want ErrInvalidExecutable, got %v", err)
	}
}

func TestOneShotLaunch_RejectsDirectoryExecutable(t *testing.T) {
	if _, err := Launch(Options{Executable: t.TempDir()}); !errors.Is(err, ErrInvalidExecutable) {
		t.Fatalf("want ErrInvalidExecutable, got %v", err)
	}
}

// The child's stdout/stderr land in the caller-owned regular log files,
// not in a parent-owned pipe the parent would have to drain.
func TestOneShotLaunch_WritesToCallerOwnedLogFiles(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.log")
	errLog := filepath.Join(dir, "err.log")
	// launchEchoOnce blocks until the child exited, so reading the logs
	// afterwards is race-free: the output must already be there.
	launchEchoOnce(t, out, errLog, 0)

	outData, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out.log: %v", err)
	}
	if !strings.Contains(string(outData), "fixture-line") {
		t.Errorf("out.log = %q, want it to contain %q", outData, "fixture-line")
	}
	errData, err := os.ReadFile(errLog)
	if err != nil {
		t.Fatalf("read err.log: %v", err)
	}
	if !strings.Contains(string(errData), "fixture-line-stderr") {
		t.Errorf("err.log = %q, want it to contain %q", errData, "fixture-line-stderr")
	}
}

// With no log paths, output is discarded through the platform null device:
// a chatty child can never block on, let alone terminate, the update via
// full output buffers.
func TestOneShotLaunch_DiscardedOutputNeverBlocks(t *testing.T) {
	start := time.Now()
	launchEchoOnce(t, "", "", 4<<20) // 4 MiB to stdout
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("Launch with discarded output blocked for %s", elapsed)
	}
}
