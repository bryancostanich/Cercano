//go:build unix

package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// The production identity guard: a group whose command line is a cercano
// worker matches; a plain process does not. Uses real short-lived processes in
// their own groups (no injected seam) to prove the pgrep pattern is precise
// enough not to kill non-workers.
func TestGroupLooksLikeWorker(t *testing.T) {
	startGroup := func(argv ...string) *exec.Cmd {
		t.Helper()
		c := exec.Command(argv[0], argv[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatalf("start %v: %v", argv, err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
		return c
	}

	// A fake binary named "cercano" that ignores its args and just sleeps, so a
	// real process carries the command line "…/cercano worker --socket …".
	dir := t.TempDir()
	bin := filepath.Join(dir, "cercano")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake cercano: %v", err)
	}

	fake := startGroup(bin, "worker", "--socket", "/tmp/x.sock")
	if !groupLooksLikeWorker(fake.Process.Pid) {
		t.Error("a group running `cercano worker …` should be identified as ours")
	}

	// A plain sleep must NOT match — the guard can't kill an unrelated process.
	plain := startGroup("sleep", "30")
	if groupLooksLikeWorker(plain.Process.Pid) {
		t.Error("a plain sleep group must not be identified as a cercano worker")
	}

	// `cercano agent` (the host itself) must NOT match — precise on "worker".
	host := startGroup(bin, "agent")
	if groupLooksLikeWorker(host.Process.Pid) {
		t.Error("a `cercano agent` host must not be identified as a worker")
	}
}
