//go:build unix

package procx

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// assertGroupReaped runs a command that spawns a grandchild recording its own
// pid, then asserts that grandchild is dead once run returns. Shared by the
// timeout and cancel variants. Process groups are unix-only, so the helper and
// its two tests live in this build-tagged file.
func assertGroupReaped(t *testing.T, run func(pidFile string) error) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")

	if err := run(pidFile); err == nil {
		t.Fatal("expected an error (timeout or cancel)")
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
	t.Fatalf("grandchild pid %d survived: process group was not killed", pid)
}

func TestRun_TimeoutKillsProcessGroup(t *testing.T) {
	assertGroupReaped(t, func(pidFile string) error {
		script := "sh -c 'echo $$ > " + pidFile + "; sleep 30' & echo spawned; sleep 30"
		_, err := Run(context.Background(), Options{
			Args:      []string{"/bin/sh", "-c", script},
			Timeout:   1 * time.Second,
			WaitDelay: 500 * time.Millisecond,
		})
		return err
	})
}

func TestRun_CancelKillsProcessGroup(t *testing.T) {
	assertGroupReaped(t, func(pidFile string) error {
		script := "sh -c 'echo $$ > " + pidFile + "; sleep 30' & echo spawned; sleep 30"
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()
		_, err := Run(ctx, Options{
			Args:      []string{"/bin/sh", "-c", script},
			Timeout:   NoTimeout,
			WaitDelay: 500 * time.Millisecond,
		})
		return err
	})
}
