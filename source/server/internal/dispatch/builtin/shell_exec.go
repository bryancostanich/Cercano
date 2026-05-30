package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"cercano/source/server/internal/dispatch"
)

const defaultShellTimeoutSec = 60

// ShellExec runs a shell command (via sh -c) and returns combined stdout / stderr / exit_code.
type ShellExec struct{}

func NewShellExec() *ShellExec { return &ShellExec{} }

func (t *ShellExec) Name() string { return "shell_exec" }

func (t *ShellExec) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "shell_exec",
		Description: "Run a shell command via 'sh -c'. Returns exit_code, stdout, and stderr. Non-zero exit is NOT an error — it is data. Default timeout 60s.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command":     map[string]interface{}{"type": "string", "description": "The shell command to run."},
				"cwd":         map[string]interface{}{"type": "string", "description": "Optional working directory. Defaults to cercano's cwd."},
				"timeout_sec": map[string]interface{}{"type": "integer", "description": "Optional timeout in seconds. Default 60."},
			},
			"required": []string{"command"},
		},
	}
}

type shellExecArgs struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

func (t *ShellExec) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a shellExecArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	timeout := time.Duration(a.TimeoutSec) * time.Second
	if a.TimeoutSec <= 0 {
		timeout = defaultShellTimeoutSec * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", a.Command)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
			err = nil
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("shell_exec timeout after %s (process killed)", timeout)
	}
	if err != nil {
		return "", fmt.Errorf("shell_exec failed: %w", err)
	}
	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String()), nil
}
