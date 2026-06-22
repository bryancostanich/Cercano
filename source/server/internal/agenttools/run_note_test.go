package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The Bash tool should set a clean one-line Note summarizing the run (exit code
// + elapsed), so the folded scrollback entry shows "exit N · …" instead of a
// truncated prefix of the raw "$ cmd\n[exit=...]" body.
func TestBashSetsExitNote(t *testing.T) {
	res, err := RunCommand().Execute(context.Background(), json.RawMessage(`{"cmd":["sh","-c","exit 3"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(res.Note, "exit 3 ") && res.Note != "exit 3" {
		t.Fatalf("Note = %q, want it to start with the exit status", res.Note)
	}
	if strings.Contains(res.Note, "\n") {
		t.Fatalf("Note must be one line, got %q", res.Note)
	}
}

func TestBashExitZeroNote(t *testing.T) {
	res, err := RunCommand().Execute(context.Background(), json.RawMessage(`{"cmd":["true"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(res.Note, "exit 0") {
		t.Fatalf("Note = %q, want exit 0", res.Note)
	}
}
