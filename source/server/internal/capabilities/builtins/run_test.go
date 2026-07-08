package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
