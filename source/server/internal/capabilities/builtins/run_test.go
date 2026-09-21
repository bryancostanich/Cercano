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

// The tool contract must teach the argv form up front: cmd is the executable
// plus its arguments, with no implicit shell, and the docs must show both a
// direct invocation and an explicit shell invocation.
func TestRunCommandCapability_ArgvContractDocs(t *testing.T) {
	cap := RunCommand()
	desc := cap.Description()
	for _, want := range []string{
		"argv",
		`["ls", "/some/path"]`,
		`["bash", "-lc", "pwd && ls .."]`,
		"no implicit shell",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("Description missing %q:\n%s", want, desc)
		}
	}

	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(cap.Schema()), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	cmdProp, ok := schema.Properties["cmd"]
	if !ok {
		t.Fatal("schema has no cmd property")
	}
	for _, want := range []string{"argv", `["ls", "/some/path"]`, `["bash", "-lc", "pwd && ls .."]`} {
		if !strings.Contains(cmdProp.Description, want) {
			t.Errorf("cmd schema description missing %q:\n%s", want, cmdProp.Description)
		}
	}
}

// The exact failure mode from dispatch c090721b6cc1ea8754993cde: an entire
// command as the sole cmd element. The executable-lookup error must be
// retained verbatim and gain an actionable argv/explicit-shell hint.
func TestRunCommandCapability_WholeCommandStringGetsArgvHint(t *testing.T) {
	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{"cmd": []string{"ls /tmp"}})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	// Original error preserved...
	if !strings.Contains(msg, "no such file or directory") || !strings.Contains(msg, "ls /tmp") {
		t.Errorf("original lookup error not retained: %q", msg)
	}
	// ...plus the argv contract and the explicit-shell escape hatch.
	for _, want := range []string{"argv", `["bash", "-lc", "pwd && ls .."]`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing argv hint element %q:\n%s", want, msg)
		}
	}
}

// The iteration-3 variant: a list of separate shell commands, where the first
// element is itself shell syntax. Same lookup failure, same hint.
func TestRunCommandCapability_ListOfCommandsGetsArgvHint(t *testing.T) {
	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{"cmd": []string{"pwd && ls ..", "echo done"}})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "argv") {
		t.Errorf("expected argv hint for list-of-commands misuse, got: %v", err)
	}
}

// A missing absolute path is a start failure, not a PATH lookup miss, but
// it is still executable-not-found and deserves the same hint.
func TestRunCommandCapability_MissingAbsolutePathGetsArgvHint(t *testing.T) {
	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{"cmd": []string{"/nonexistent/definitely-not-here"}})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "argv") || !strings.Contains(msg, "/nonexistent/definitely-not-here") {
		t.Errorf("expected original error + argv hint, got: %v", err)
	}
}

// The hint is the recovery path the docs point to: shell syntax works when a
// shell is invoked explicitly. `&&` and friends must be honored by that shell.
func TestRunCommandCapability_ExplicitShellRunsShellSyntax(t *testing.T) {
	cap := RunCommand()
	// bash -lc: login shell executing a compound command, as documented.
	args, _ := json.Marshal(map[string]any{"cmd": []string{"bash", "-lc", "printf one && printf two"}})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "onetwo") {
		t.Fatalf("expected compound shell output 'onetwo', got: %s", res.Text)
	}
	if res.Detail != "exit 0" {
		t.Fatalf("expected exit 0, got detail %q", res.Detail)
	}
}

// Executable paths containing spaces must work: the first argv element is
// used as-is, never split or re-wrapped.
func TestRunCommandCapability_ExecutablePathWithSpaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script executable is unix-only")
	}
	dir := t.TempDir()
	spaced := filepath.Join(dir, "my tools", "run me.sh")
	if err := os.MkdirAll(filepath.Dir(spaced), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf 'argv0=%s arg=%s' \"$0\" \"$1\"\n"
	if err := os.WriteFile(spaced, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cap := RunCommand()
	args, _ := json.Marshal(map[string]any{"cmd": []string{spaced, "hello world"}})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("executable path with spaces failed: %v", err)
	}
	// The whole path ran as argv[0]; its argument arrived unsplit.
	if !strings.Contains(res.Text, "argv0="+spaced+" arg=hello world") {
		t.Fatalf("expected unsplit path and argument, got: %s", res.Text)
	}
}

// Regression (review finding): a valid executable started with a missing or
// non-directory cwd also fails at fork/exec with ENOENT on Unix, which is
// indistinguishable from a missing executable by the error alone. The argv
// hint must not fire there — the cwd, not the argv contract, is the problem,
// and the original error must be preserved.
func TestRunCommandCapability_MissingCwdNoArgvHint(t *testing.T) {
	dir := t.TempDir()
	cap := RunCommand()

	// Case 1: cwd does not exist, executable does.
	missing := filepath.Join(dir, "no", "such", "dir")
	args, _ := json.Marshal(map[string]any{"cmd": []string{"echo", "hi"}, "cwd": missing})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for missing cwd, got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, "argv") {
		t.Errorf("missing-cwd failure mislabeled with the argv hint: %v", err)
	}
	if !strings.Contains(msg, "fork/exec") || !strings.Contains(msg, "no such file or directory") {
		t.Errorf("original start error not retained for missing cwd: %q", msg)
	}

	// Case 2: cwd is a regular file, not a directory.
	notDir := filepath.Join(dir, "file-not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]any{"cmd": []string{"echo", "hi"}, "cwd": notDir})
	_, err = cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for non-directory cwd, got nil")
	}
	msg = err.Error()
	if strings.Contains(msg, "argv") {
		t.Errorf("non-directory-cwd failure mislabeled with the argv hint: %v", err)
	}
	if !strings.Contains(msg, "fork/exec") || !strings.Contains(msg, "not a directory") {
		t.Errorf("original start error not retained for non-directory cwd: %q", msg)
	}
}

// Ordinary failures must NOT be mislabeled as lookup errors: a non-zero exit
// is a normal result, and a permission failure keeps its original error
// without the argv hint.
func TestRunCommandCapability_OrdinaryFailuresNotMislabeled(t *testing.T) {
	cap := RunCommand()

	// Non-zero exit: a successful tool call carrying the exit code, no error.
	args, _ := json.Marshal(map[string]any{"cmd": []string{"sh", "-c", "printf oops >&2; exit 3"}})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("non-zero exit must not be an error, got: %v", err)
	}
	if res.Detail != "exit 3" {
		t.Errorf("expected detail 'exit 3', got %q", res.Detail)
	}
	if !strings.Contains(res.Text, "oops") {
		t.Errorf("expected stderr 'oops' in output, got: %s", res.Text)
	}

	// Permission failure on an existing file: original error, no argv hint.
	dir := t.TempDir()
	notExec := filepath.Join(dir, "noexec.sh")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]any{"cmd": []string{notExec}})
	_, err = cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
	if strings.Contains(err.Error(), "argv") {
		t.Errorf("permission failure mislabeled as executable-not-found: %v", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("original permission error not retained: %v", err)
	}
}

func TestRunCommandRejectsStandaloneCd(t *testing.T) {
	for _, exe := range []string{"cd", "/usr/bin/cd"} {
		args, _ := json.Marshal(map[string]any{"cmd": []string{exe, t.TempDir(), "&&", "echo", "not-run"}})
		_, err := RunCommand().Execute(context.Background(), &capabilities.Call{Args: args})
		if err == nil || !strings.Contains(err.Error(), "cwd") || !strings.Contains(err.Error(), "bash") {
			t.Fatalf("%s: expected actionable cd error, got %v", exe, err)
		}
	}
}

func TestRunCommandExplicitShellAndLiteralOperators(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell")
	}
	dir := t.TempDir()
	for _, cmd := range [][]string{{"sh", "-c", `cd "$1" && printf SHELL_EXECUTED`, "sh", dir}, {"printf", "%s", "&&"}} {
		args, _ := json.Marshal(map[string]any{"cmd": cmd, "cwd": dir})
		res, err := RunCommand().Execute(context.Background(), &capabilities.Call{Args: args})
		if err != nil || res.Detail != "exit 0" {
			t.Fatalf("%v: %v %v", cmd, res, err)
		}
		want := "SHELL_EXECUTED"
		if cmd[0] == "printf" {
			want = "&&"
		}
		if !strings.Contains(res.Text, "stdout:\n"+want) {
			t.Fatalf("missing actual output %q: %s", want, res.Text)
		}
	}
}
