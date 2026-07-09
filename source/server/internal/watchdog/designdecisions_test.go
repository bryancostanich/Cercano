package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestDesignDecisionsCheckAppliesToMutations(t *testing.T) {
	ch := DesignDecisionsCheck()
	if !ch.Applies(Action{Kind: "tool_call", ToolName: "edit_file"}) {
		t.Fatal("edit_file mutation should be checked for design-decision skips")
	}
	if !ch.Applies(Action{Kind: "tool_call", ToolName: "run_command"}) {
		t.Fatal("run_command should be checked for design-decision skips")
	}
	if ch.Applies(Action{Kind: "turn_end"}) {
		t.Fatal("turn_end should not apply to design-decisions")
	}
}

func TestDesignDecisionsCheckUsesCanonicalProtocolAndChallenge(t *testing.T) {
	ch := DesignDecisionsCheck()
	v, err := ch.Evaluate(context.Background(), Action{Kind: "tool_call", ToolName: "Edit", ToolArgs: []byte(`{"path":"x.go"}`)}, func(ctx context.Context, prompt string) (string, error) {
		if !strings.Contains(prompt, "design-decisions protocol") {
			t.Fatalf("prompt should name the protocol: %s", prompt)
		}
		return "VIOLATION: yes\nCHALLENGE: model wording", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "design-decisions" {
		t.Fatalf("verdict = %+v, want design-decisions violation", v)
	}
	if !strings.Contains(v.Challenge, `get_protocol("design-decisions")`) || !strings.Contains(v.Challenge, "comply") || !strings.Contains(v.Challenge, "justify") {
		t.Fatalf("challenge should tell the model to comply or justify with the protocol pull: %q", v.Challenge)
	}
}
