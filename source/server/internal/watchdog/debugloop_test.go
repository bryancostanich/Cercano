package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestDebugLoopApplies(t *testing.T) {
	c := DebugLoopCheck()
	if !c.Applies(Action{Kind: "tool_call", ToolName: "edit_file"}) {
		t.Fatal("edit_file should apply")
	}
	if c.Applies(Action{Kind: "tool_call", ToolName: "read_file"}) {
		t.Fatal("read_file must not apply")
	}
	if c.Applies(Action{Kind: "turn_end"}) {
		t.Fatal("turn_end must not apply to debug-loop")
	}
}

func TestDebugLoopEvaluate(t *testing.T) {
	var gotPrompt string
	oneShot := func(_ context.Context, prompt string) (string, error) {
		gotPrompt = prompt
		return "VIOLATION: yes\nCHALLENGE: no debug evidence", nil
	}
	v, err := DebugLoopCheck().Evaluate(context.Background(),
		Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"x.go"}`)}, oneShot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "debug-loop" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, "edit_file") {
		t.Fatalf("prompt should reference the proposed action: %q", gotPrompt)
	}
}
