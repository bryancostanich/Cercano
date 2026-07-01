package watchdog

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// toolUse builds a one-block assistant message representing a tool call.
func toolUse(name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{Type: llm.BlockToolUse, ToolName: name, ToolInput: []byte(input)},
	}}
}

func editAction2() Action {
	return Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"b.go"}`)}
}

func TestCommitCheckpointApplies(t *testing.T) {
	c := CommitCheckpointCheck()

	// No prior edits → the first edit of a unit must NOT apply.
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: nil}) {
		t.Fatal("no uncommitted work → must not apply")
	}
	// One uncommitted prior edit + a new edit → applies.
	tr := []llm.Message{toolUse("edit_file", `{"path":"a.go"}`)}
	if !c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: tr}) {
		t.Fatal("uncommitted prior edit + new edit → must apply")
	}
	// A commit AFTER the prior edit clears uncommitted work → must not apply.
	tr2 := []llm.Message{toolUse("edit_file", `{"path":"a.go"}`), toolUse("checkpoint", `{"subject":"x"}`)}
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: tr2}) {
		t.Fatal("commit cleared uncommitted work → must not apply")
	}
	// Non-edit action never applies.
	if c.Applies(Action{Kind: "tool_call", ToolName: "read_file", Transcript: tr}) {
		t.Fatal("read_file must not apply")
	}
}

func TestCommitCheckpointEvaluate(t *testing.T) {
	tr := []llm.Message{toolUse("edit_file", `{"path":"auth.go"}`)}
	var gotPrompt string
	boundary := func(_ context.Context, p string) (string, error) {
		gotPrompt = p
		return "VIOLATION: yes\nCHALLENGE: commit the auth change before starting the parser", nil
	}
	a := Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"parser.go"}`), Transcript: tr}
	v, err := CommitCheckpointCheck().Evaluate(context.Background(), a, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "commit-checkpoint" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, "auth.go") || !strings.Contains(gotPrompt, "parser.go") {
		t.Fatalf("prompt must reference the prior work and the new edit: %q", gotPrompt)
	}
	// Continuation → no nudge.
	cont := func(_ context.Context, _ string) (string, error) { return "VIOLATION: no", nil }
	if vc, _ := CommitCheckpointCheck().Evaluate(context.Background(), a, cont); vc.Violation {
		t.Fatal("continuation verdict must not nudge")
	}
	// nil oneShot → fail open (no violation).
	if vn, _ := CommitCheckpointCheck().Evaluate(context.Background(), a, nil); vn.Violation {
		t.Fatal("nil oneShot must fail open")
	}
}
