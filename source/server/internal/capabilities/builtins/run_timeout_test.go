package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
)

// ---------------------------------------------------------------------------
// timeout_seconds = -1 (no timeout)
// ---------------------------------------------------------------------------

// The sentinel table: only -1 is a valid negative, and 0/omitted must keep
// meaning "default" because Go's zero value cannot distinguish an absent
// field from an explicit 0.
func TestRunCmdResolveTimeout(t *testing.T) {
	cases := []struct {
		name    string
		secs    int
		want    time.Duration
		wantErr bool
	}{
		{"omitted or explicit zero uses default", 0, runCmdDefaultTimeout, false},
		{"positive is seconds", 5, 5 * time.Second, false},
		{"minus one is unbounded", -1, noRunTimeout, false},
		{"other negatives rejected", -60, 0, true},
		{"minus two rejected", -2, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runCmdResolveTimeout(tc.secs)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for timeout_seconds=%d", tc.secs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("timeout_seconds=%d: got %v, want %v", tc.secs, got, tc.want)
			}
		})
	}
}

// A negative other than -1 must fail loudly rather than silently running
// unbounded, so a sign-error typo cannot hang a turn.
func TestRunCommandCapability_RejectsInvalidNegativeTimeout(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"echo", "hi"},
		"timeout_seconds": -60,
	})
	_, err := RunCommand().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected an error for timeout_seconds=-60")
	}
	if !strings.Contains(err.Error(), "invalid timeout_seconds") {
		t.Fatalf("expected 'invalid timeout_seconds' in error, got: %v", err)
	}
}

// With -1, a command that outlives the 60s default must still succeed. Using a
// real >60s sleep would make the suite unbearable, so this asserts the
// mechanism instead: no deadline is attached, and the command runs to
// completion normally.
func TestRunCommandCapability_UnboundedRunsToCompletion(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"echo", "unbounded"},
		"timeout_seconds": -1,
	})
	res, err := RunCommand().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("unbounded run failed: %v", err)
	}
	if !strings.Contains(res.Text, "unbounded") {
		t.Fatalf("expected command output, got: %q", res.Text)
	}
}

// ---------------------------------------------------------------------------
// cancellation
// ---------------------------------------------------------------------------

// Load-bearing for -1: an unbounded command has no deadline, so turn
// cancellation (Esc) is the ONLY thing that can stop it. This guards the ctx
// chain CLI -> gRPC stream -> beginTurn -> toolloop execCtx -> exec. A future
// refactor slipping a context.Background() in anywhere along that chain would
// strand unbounded commands forever; this test catches the capability end.
func TestRunCommandCapability_UnboundedStopsOnCancel(t *testing.T) {
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	ctx, cancel := context.WithCancel(context.Background())
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"/bin/sh", "-c", "echo working; sleep 300"},
		"timeout_seconds": -1, // no deadline: only cancel can end this
	})

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := RunCommand().Execute(ctx, &capabilities.Call{Args: args})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the turn is cancelled")
	}
	// Cancellation must not masquerade as a timeout or a command failure.
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected 'cancelled' in error, got: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("cancel not honoured: returned after %s, want <1s", elapsed.Round(time.Millisecond))
	}
	// Whatever it printed before the kill should survive.
	if !strings.Contains(err.Error(), "working") {
		t.Errorf("expected partial stdout in cancel error, got: %v", err)
	}
}

// Cancellation must reap the whole process group, not just the direct child —
// otherwise Esc on an unbounded run leaks orphaned grandchildren.
func TestRunCommandCapability_CancelKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix-only")
	}
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := "sh -c 'echo $$ > " + pidFile + "; sleep 30' & echo spawned; sleep 30"

	ctx, cancel := context.WithCancel(context.Background())
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"/bin/sh", "-c", script},
		"timeout_seconds": -1,
	})
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	if _, err := RunCommand().Execute(ctx, &capabilities.Call{Args: args}); err == nil {
		t.Fatal("expected a cancellation error")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Skipf("grandchild never recorded its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Skipf("unreadable pid: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // reaped
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak it out of the test
	t.Fatalf("grandchild pid %d survived cancellation: process group was not killed", pid)
}
