package procx

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Several tests drive a POSIX shell and unix process semantics. They are
// skipped on Windows rather than silently passing or failing on a missing
// `/bin/sh`; dedicated Windows coverage is part of the pending support work
// (docs/windows-artifact.md).
func requirePosixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test drives a POSIX shell; Windows-native coverage is pending")
	}
}

func TestRun_CapturesOutputAndExitCode(t *testing.T) {
	requirePosixShell(t)
	res, err := Run(context.Background(), Options{
		Args:    []string{"/bin/sh", "-c", "echo out; echo err 1>&2"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "out" {
		t.Errorf("stdout = %q, want %q", got, "out")
	}
	if got := strings.TrimSpace(string(res.Stderr)); got != "err" {
		t.Errorf("stderr = %q, want %q", got, "err")
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
}

// A non-zero exit is a legitimate answer for callers like
// `git merge-base --is-ancestor`, so it must not be reported as an error.
func TestRun_NonZeroExitIsNotAnError(t *testing.T) {
	requirePosixShell(t)
	res, err := Run(context.Background(), Options{
		Args:    []string{"/bin/sh", "-c", "exit 3"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("non-zero exit should not be an error, got: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", res.ExitCode)
	}
}

func TestRun_RejectsBadTimeout(t *testing.T) {
	for _, d := range []time.Duration{0, -5 * time.Second} {
		if _, err := Run(context.Background(), Options{
			Args:    []string{"echo", "hi"},
			Timeout: d,
		}); err == nil {
			t.Errorf("Timeout=%v should be rejected", d)
		}
	}
}

func TestRun_RejectsEmptyArgs(t *testing.T) {
	if _, err := Run(context.Background(), Options{Timeout: time.Second}); err == nil {
		t.Fatal("empty Args should be rejected")
	}
}

func TestRun_Stdin(t *testing.T) {
	requirePosixShell(t)
	res, err := Run(context.Background(), Options{
		Args:    []string{"cat"},
		Stdin:   []byte("piped"),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(res.Stdout); got != "piped" {
		t.Errorf("stdout = %q, want %q", got, "piped")
	}
}

func TestRun_DirAndEnv(t *testing.T) {
	requirePosixShell(t)
	dir := t.TempDir()
	res, err := Run(context.Background(), Options{
		Args:    []string{"/bin/sh", "-c", "pwd; echo $MARKER"},
		Dir:     dir,
		Env:     []string{"MARKER=set"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(res.Stdout)
	// macOS resolves TempDir through /private, so compare resolved paths.
	resolved, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(out, dir) && !strings.Contains(out, resolved) {
		t.Errorf("expected cwd %q in output, got %q", dir, out)
	}
	if !strings.Contains(out, "set") {
		t.Errorf("expected env MARKER in output, got %q", out)
	}
}

// The core regression: a backgrounded grandchild holding the stdout pipe must
// not defeat the timeout.
func TestRun_TimeoutWithBackgroundedChild(t *testing.T) {
	requirePosixShell(t)
	start := time.Now()
	res, err := Run(context.Background(), Options{
		Args:      []string{"/bin/sh", "-c", "sleep 30 & echo started; sleep 30"},
		Timeout:   1 * time.Second,
		WaitDelay: 500 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got: %v", err)
	}
	if !res.TimedOut {
		t.Error("Result.TimedOut should be true")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("timeout not enforced: returned after %s, want ~1s", elapsed.Round(time.Millisecond))
	}
	// Partial output must survive the kill.
	if !strings.Contains(string(res.Stdout), "started") {
		t.Errorf("expected partial stdout, got %q", res.Stdout)
	}
}

// NoTimeout removes the deadline but must still honour ctx cancellation.
func TestRun_NoTimeoutStopsOnCancel(t *testing.T) {
	requirePosixShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res, err := Run(ctx, Options{
		Args:      []string{"/bin/sh", "-c", "echo working; sleep 300"},
		Timeout:   NoTimeout,
		WaitDelay: 500 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("expected ErrCanceled, got: %v", err)
	}
	if res.TimedOut {
		t.Error("cancellation must not be reported as a timeout")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("cancel not honoured: returned after %s", elapsed.Round(time.Millisecond))
	}
	if !strings.Contains(string(res.Stdout), "working") {
		t.Errorf("expected partial stdout, got %q", res.Stdout)
	}
}

func TestRun_NoTimeoutRunsToCompletion(t *testing.T) {
	requirePosixShell(t)
	res, err := Run(context.Background(), Options{
		Args:    []string{"echo", "unbounded"},
		Timeout: NoTimeout,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(res.Stdout), "unbounded") {
		t.Errorf("stdout = %q", res.Stdout)
	}
}
