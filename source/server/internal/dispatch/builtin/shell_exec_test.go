package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellExec_ZeroExit(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "echo hello"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "exit_code: 0") {
		t.Errorf("got %q, want it to contain exit_code: 0", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("got %q, want it to contain 'hello' from stdout", got)
	}
}

func TestShellExec_NonZeroExitIsData(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "exit 7"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("non-zero exit should NOT be a Go error, got: %v", err)
	}
	if !strings.Contains(got, "exit_code: 7") {
		t.Errorf("got %q, want it to contain exit_code: 7", got)
	}
}

func TestShellExec_StderrCaptured(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "echo boom >&2"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("got %q, want it to contain stderr 'boom'", got)
	}
}

func TestShellExec_Timeout(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "sleep 10", "timeout_sec": 1})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "killed") {
		t.Errorf("err = %q, want it to mention timeout/killed", err.Error())
	}
}

func TestShellExec_Cwd(t *testing.T) {
	dir := t.TempDir()
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "pwd", "cwd": dir})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, dir) {
		t.Errorf("got %q, want it to contain cwd %s", got, dir)
	}
}

func TestShellExec_BadArgs(t *testing.T) {
	tool := NewShellExec()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
