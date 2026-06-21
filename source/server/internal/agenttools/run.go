package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

var osEnviron = os.Environ

// runCommandTool is the generic shell-out escape hatch. The agent can
// always reach for this when a more specific tool doesn't fit. Stdout +
// stderr are captured (NOT streamed) so they can be size-bounded before
// they hit the model context.
type runCommandTool struct{}

// RunCommand constructs the run_command tool.
func RunCommand() Tool { return runCommandTool{} }

func (runCommandTool) Name() string             { return "run_command" }
func (runCommandTool) Permission() Permission   { return PermW }
func (runCommandTool) Description() string {
	return "Run a shell command and capture its output. Args: {cmd: [argv strings], cwd?: string, timeout_seconds?: int (default 60), env?: {key: value}}."
}
func (runCommandTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (runCommandTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a runCommandArgs
	if err := json.Unmarshal(raw, &a); err != nil {
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
		envv := append([]string(nil), envOf(a.Env)...)
		cmd.Env = envv
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	startedAt := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startedAt)
	exitCode := 0
	if err != nil {
		// Check context deadline FIRST — exec.CommandContext kills the
		// child on timeout, which surfaces as a non-nil ExitError. Without
		// this guard the ExitError branch would swallow the timeout.
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
	soStr := truncateBytes(stdout.Bytes(), perStreamCap)
	seStr := truncateBytes(stderr.Bytes(), perStreamCap)

	body := fmt.Sprintf("$ %s\n\n[exit=%d, elapsed=%s]\n", joinShell(a.Cmd), exitCode, elapsed.Round(time.Millisecond))
	if soStr != "" {
		body += "\nstdout:\n" + soStr
	}
	if seStr != "" {
		body += "\nstderr:\n" + seStr
	}
	return NewTextResult(body), nil
}

// envOf turns the args env map into the os.Environ-style "K=V" slice,
// prepending the agent's existing environment so PATH etc. survive.
func envOf(overrides map[string]string) []string {
	base := envBase()
	for k, v := range overrides {
		base = append(base, k+"="+v)
	}
	return base
}

// envBase returns os.Environ. Split out so tests can stub.
var envBase = func() []string { return realEnviron() }

// realEnviron wraps os.Environ — separate function so the envBase var above
// can be overridden in tests without re-binding os.
func realEnviron() []string { return osEnviron() }

// truncateBytes caps b to maxBytes with a marker.
func truncateBytes(b []byte, maxBytes int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "\n…(truncated)"
}

// joinShell renders argv as a copy-pasteable shell line. Naive — wraps
// args containing spaces in quotes; doesn't handle existing quotes. Fine
// for display.
func joinShell(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		if containsAny(a, " \t\n\"'$`") {
			out += "'" + a + "'"
		} else {
			out += a
		}
	}
	return out
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, r := range s {
			if r == c {
				return true
			}
		}
	}
	return false
}
