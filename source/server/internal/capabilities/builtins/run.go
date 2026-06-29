package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"cercano/source/server/internal/capabilities"
)

// runCommandCap is the generic shell-out escape hatch. Stdout + stderr are
// captured (NOT streamed) so they can be size-bounded before hitting the
// model context. Each stream is capped at 16 KiB independently.
type runCommandCap struct{}

// RunCommand constructs the run_command capability (display name "Bash").
func RunCommand() capabilities.Capability { return runCommandCap{} }

func (runCommandCap) Name() string                  { return "run_command" }
func (runCommandCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (runCommandCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (runCommandCap) Description() string {
	return "Run a shell command and capture its output. Args: {cmd: [argv strings], cwd?: string, timeout_seconds?: int (default 60), env?: {key: value}}."
}
func (runCommandCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["cmd"],
		"properties": {
			"cmd":             {"type": "array", "items": {"type": "string"}, "minItems": 1},
			"cwd":             {"type": "string"},
			"timeout_seconds": {"type": "integer", "minimum": 1, "default": 60},
			"env":             {"type": "object", "additionalProperties": {"type": "string"}}
		}
	}`)
}

type runCommandArgs struct {
	Cmd            []string          `json:"cmd"`
	Cwd            string            `json:"cwd"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Env            map[string]string `json:"env"`
}

func (runCommandCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a runCommandArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("run_command: parse args: %w", err)
	}
	if len(a.Cmd) == 0 {
		return nil, errors.New("run_command: cmd is required and must have at least one element")
	}

	timeout := time.Duration(a.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, a.Cmd[0], a.Cmd[1:]...)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	if len(a.Env) > 0 {
		// Append to the agent's environment so the command sees PATH etc.
		cmd.Env = runCmdEnvOf(a.Env)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startedAt)

	exitCode := 0
	if err != nil {
		// Check context deadline FIRST — exec.CommandContext kills the child
		// on timeout, which surfaces as a non-nil ExitError. Without this
		// guard the ExitError branch would swallow the timeout.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("run_command: timed out after %s", timeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return nil, fmt.Errorf("run_command: %w", err)
		}
	}

	// Truncate each stream independently — caps at 16 KiB each so a single
	// chatty side doesn't eat the entire budget. NewTextResult later may
	// apply the joint 32 KiB cap.
	const perStreamCap = 16 * 1024
	soStr := runCmdTruncateBytes(stdout.Bytes(), perStreamCap)
	seStr := runCmdTruncateBytes(stderr.Bytes(), perStreamCap)

	body := fmt.Sprintf("$ %s\n\n[exit=%d, elapsed=%s]\n", runCmdJoinShell(a.Cmd), exitCode, elapsed.Round(time.Millisecond))
	if soStr != "" {
		body += "\nstdout:\n" + soStr
	}
	if seStr != "" {
		body += "\nstderr:\n" + seStr
	}

	res := capabilities.NewTextResult(body)
	res.Detail = fmt.Sprintf("exit %d", exitCode)

	// One-line glance summary for the folded scrollback entry. Prepend rather
	// than overwrite so a truncation note from NewTextResult survives.
	summary := fmt.Sprintf("exit %d · %s", exitCode, elapsed.Round(time.Millisecond))
	if res.Note == "" {
		res.Note = summary
	} else {
		res.Note = summary + " · " + res.Note
	}

	return res, nil
}

// runCmdEnvOf builds the "K=V" env slice, prepending the process's existing
// environment so PATH etc. survive.
func runCmdEnvOf(overrides map[string]string) []string {
	base := append([]string(nil), runCmdEnvBase()...)
	for k, v := range overrides {
		base = append(base, k+"="+v)
	}
	return base
}

// runCmdEnvBase returns os.Environ. Split out so tests can stub.
var runCmdEnvBase = func() []string { return os.Environ() }

// runCmdTruncateBytes caps b to maxBytes with a marker.
func runCmdTruncateBytes(b []byte, maxBytes int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "\n…(truncated)"
}

// runCmdJoinShell renders argv as a copy-pasteable shell line.
func runCmdJoinShell(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		if runCmdContainsAny(a, " \t\n\"'$`") {
			out += "'" + a + "'"
		} else {
			out += a
		}
	}
	return out
}

func runCmdContainsAny(s, chars string) bool {
	for _, c := range chars {
		for _, r := range s {
			if r == c {
				return true
			}
		}
	}
	return false
}
