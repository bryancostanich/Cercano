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

func TestRunCommandCapability_Meta(t *testing.T) {
	cap := RunCommand()
	if cap.Name() != "run_command" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	if cap.Surfaces() != capabilities.SurfaceAgent|capabilities.SurfaceMCP {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestRunCommandCapability_SimpleCommand(t *testing.T) {
	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{"cmd": []string{"echo", "hello world"}})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != capabilities.ResultText {
		t.Fatalf("expected text result, got %q", res.Type)
	}
	if !strings.Contains(res.Text, "hello world") {
		t.Fatalf("expected 'hello world' in output, got: %s", res.Text)
	}
	if res.Detail != "exit 0" {
		t.Fatalf("expected detail 'exit 0', got %q", res.Detail)
	}
}

func TestRunCommandCapability_ExitCode(t *testing.T) {
	cap := RunCommand()
	// false exits with 1
	args, _ := json.Marshal(map[string]any{"cmd": []string{"false"}})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Detail != "exit 1" {
		t.Fatalf("expected detail 'exit 1', got %q", res.Detail)
	}
}

func TestRunCommandCapability_MissingCmd(t *testing.T) {
	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for missing cmd")
	}
	if !strings.Contains(err.Error(), "run_command:") {
		t.Fatalf("expected error prefix 'run_command:', got %q", err.Error())
	}
}

func TestRunCommandCapability_OutputCap(t *testing.T) {
	cap := RunCommand()
	// Generate > 16 KiB on stdout. dd writes 20 KiB of 'A' chars.
	args, _ := json.Marshal(map[string]any{
		"cmd": []string{"/bin/sh", "-c", "dd if=/dev/zero bs=1024 count=20 2>/dev/null | tr '\\0' 'A'"},
	})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	// Per-stream truncation appends the marker to the text body;
	// Result.Truncated is only set by NewTextResult at the 32 KiB joint cap.
	if !strings.Contains(res.Text, "truncated") {
		t.Fatalf("expected truncation marker in text; got %d bytes without marker", len(res.Text))
	}
}

func TestRunCommandCapability_Timeout(t *testing.T) {
	cap := RunCommand()
	// timeout_seconds=1, sleep 10 — should time out and return an error.
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"sleep", "10"},
		"timeout_seconds": 1,
	})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got: %v", err)
	}
}

// Regression: a command that backgrounds a long-lived grandchild used to
// defeat the timeout entirely. exec.CommandContext killed only the direct
// shell, while the grandchild kept the inherited stdout pipe open, so Wait
// blocked until the grandchild exited — a 2s cap took 60s to return.
func TestRunCommandCapability_TimeoutWithBackgroundedChild(t *testing.T) {
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{
		// The backgrounded sleep inherits stdout and outlives the shell.
		"cmd":             []string{"/bin/sh", "-c", "sleep 30 & echo started; sleep 30"},
		"timeout_seconds": 1,
	})

	start := time.Now()
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got: %v", err)
	}
	// Must return promptly: 1s timeout + WaitDelay, not the 30s grandchild.
	if elapsed > 10*time.Second {
		t.Fatalf("timeout not enforced: returned after %s, want ~1s", elapsed.Round(time.Millisecond))
	}
	// Output captured before the kill should survive into the error.
	if !strings.Contains(err.Error(), "started") {
		t.Errorf("expected partial stdout in timeout error, got: %v", err)
	}
}

// The timed-out command's whole process group must be dead on return, not
// leaked as orphans still holding resources.
func TestRunCommandCapability_TimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix-only")
	}
	restore := runCommandWaitDelay
	runCommandWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { runCommandWaitDelay = restore })

	// The grandchild writes a marker file, then sleeps well past the timeout.
	// If it is still alive after we return, it deletes nothing — so we probe
	// liveness by pid instead, recorded into the marker.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := "sh -c 'echo $$ > " + pidFile + "; sleep 30' & echo spawned; sleep 30"

	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{
		"cmd":             []string{"/bin/sh", "-c", script},
		"timeout_seconds": 1,
	})
	if _, err := cap.Execute(context.Background(), &capabilities.Call{Args: args}); err == nil {
		t.Fatal("expected timeout error")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Skipf("grandchild never recorded its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Skipf("unreadable pid: %v", err)
	}

	// Give the group-kill a moment to land, then assert the pid is gone.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // reaped
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak it out of the test
	t.Fatalf("grandchild pid %d survived the timeout: process group was not killed", pid)
}

func TestRun_DefaultsCwdToWorkDir(t *testing.T) {
	dir := t.TempDir()
	call := &capabilities.Call{
		WorkDir: dir,
		Args:    []byte(`{"cmd":["pwd"]}`),
		Emit:    func(string) {},
	}
	res, err := RunCommand().Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(res.Text); !strings.Contains(got, dir) {
		t.Errorf("pwd = %q, want WorkDir %q in output", got, dir)
	}
}
