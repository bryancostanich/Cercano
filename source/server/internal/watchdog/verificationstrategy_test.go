package watchdog

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestVerificationStrategyAppliesToTestAfterChange(t *testing.T) {
	ch := VerificationStrategyCheck()
	a := Action{
		Kind:       "tool_call",
		ToolName:   "run_command",
		ToolArgs:   []byte(`{"cmd":["bash","-lc","go test ./internal/watchdog"],"cwd":"/repo"}`),
		Transcript: []llm.Message{{Role: "assistant", Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "edit_file", ToolInput: []byte(`{"path":"x.go"}`)}}}},
	}
	if !ch.Applies(a) {
		t.Fatal("verification-strategy should apply to a test command after a change")
	}
}

func TestVerificationStrategyDoesNotApplyToInspectionCommand(t *testing.T) {
	ch := VerificationStrategyCheck()
	a := Action{Kind: "tool_call", ToolName: "run_command", ToolArgs: []byte(`{"cmd":["bash","-lc","grep -R foo ."],"cwd":"/repo"}`)}
	if ch.Applies(a) {
		t.Fatal("verification-strategy should not apply to non-test inspection commands")
	}
}

func TestVerificationStrategyChallengeNamesProtocol(t *testing.T) {
	ch := VerificationStrategyCheck()
	v, err := ch.Evaluate(context.Background(), Action{Kind: "tool_call", ToolName: "run_command", ToolArgs: []byte(`{"cmd":["go","test","./..."],"cwd":"/repo"}`)}, func(ctx context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "verification-strategy protocol") {
			t.Fatalf("prompt should name the protocol: %s", prompt)
		}
		return "VIOLATION: yes\nCHALLENGE: model wording", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "verification-strategy" {
		t.Fatalf("verdict = %+v, want verification-strategy violation", v)
	}
	if !strings.Contains(v.Challenge, `get_protocol("verification-strategy")`) || !strings.Contains(v.Challenge, "comply") || !strings.Contains(v.Challenge, "justify") {
		t.Fatalf("challenge should tell the model to comply or justify with the protocol pull: %q", v.Challenge)
	}
}
