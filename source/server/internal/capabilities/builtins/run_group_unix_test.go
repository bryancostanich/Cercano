//go:build unix

package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
)

// waitForReaped polls the grandchild's recorded pid until it disappears, then
// returns true; it force-kills and returns false if the deadline passes.
// Shared by the process-group tests below.
func waitForReaped(t *testing.T, pidFile string) {
	t.Helper()
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
	t.Fatalf("grandchild pid %d survived: process group was not killed", pid)
}

const groupKillScript = "sh -c 'echo $$ > %s; sleep 30' & echo spawned; sleep 30"

// The timed-out command's whole process group must be dead on return, not
// leaked as orphans still holding resources.
func TestRunCommandCapability_TimeoutKillsProcessGroup(t *testing.T) {
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	// The grandchild writes a marker file with its pid, then sleeps well past
	// the timeout; we probe liveness by that pid.
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"/bin/sh", "-c", fmt.Sprintf(groupKillScript, pidFile)},
		"timeout_seconds": 1,
	})
	if _, err := RunCommand().Execute(context.Background(), &capabilities.Call{Args: args}); err == nil {
		t.Fatal("expected timeout error")
	}
	waitForReaped(t, pidFile)
}

// Cancellation must reap the whole process group, not just the direct child —
// otherwise Esc on an unbounded run leaks orphaned grandchildren.
func TestRunCommandCapability_CancelKillsProcessGroup(t *testing.T) {
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(context.Background())
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"/bin/sh", "-c", fmt.Sprintf(groupKillScript, pidFile)},
		"timeout_seconds": -1,
	})
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	if _, err := RunCommand().Execute(ctx, &capabilities.Call{Args: args}); err == nil {
		t.Fatal("expected a cancellation error")
	}
	waitForReaped(t, pidFile)
}
