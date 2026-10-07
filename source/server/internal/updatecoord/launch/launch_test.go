package launch

// Direct-child tests: the test process itself is the initiating parent.
// Every child is reaped or terminated on every path by these fixtures.

import (
	"errors"
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
		t.Fatalf("Launch: %v", err)
	}
	pid := proc.Pid()
	// Safety net for paths where the child never exits on its own; the
	// flag keeps the normal (reaped) path from ever signalling again, so a
	// recycled pid can never be terminated by cleanup. t.Cleanup runs on
	// the same goroutine as the test body.
	confirmedGone := false
	t.Cleanup(func() {
		if !confirmedGone {
			terminateGrandchildPID(pid)
		}
	})
	p, err := os.FindProcess(pid)
	if err != nil {
		return pid
	}
	done := make(chan struct{})
	go func() {
		_, _ = p.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Wait both observed and reaped this direct child.
		confirmedGone = true
	case <-time.After(10 * time.Second):
		t.Fatalf("echo-once child %d did not exit within 10s", pid)
	}
	return pid
}

// waitForFile polls for a file until timeout, returning its contents.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
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

// An unopenable output log must fail closed BEFORE any child starts: an
// output problem is reported to the caller, never silently swallowed into
// a half-launched update utility.
func TestOneShotLaunch_OutputLogOpenFailureFailsClosed(t *testing.T) {
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	missingDir := filepath.Join(t.TempDir(), "no", "such", "dir")
	if _, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: filepath.Join(missingDir, "out.log"),
	}); !errors.Is(err, ErrOutputSetup) {
		t.Fatalf("want ErrOutputSetup, got %v", err)
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
