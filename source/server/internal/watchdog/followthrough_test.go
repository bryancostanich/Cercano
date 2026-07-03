package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestFollowThroughApplies(t *testing.T) {
	c := FollowThroughCheck()
	if !c.Applies(Action{Kind: "turn_end", Text: "Let me check the server log for the dispatch line."}) {
		t.Fatal("a turn_end reply should apply")
	}
	if c.Applies(Action{Kind: "turn_end", Text: "   "}) {
		t.Fatal("an empty reply must be skipped")
	}
	if c.Applies(Action{Kind: "tool_call", ToolName: "Bash", Text: "Let me check."}) {
		t.Fatal("tool_call must not apply")
	}
}

func TestFollowThroughEvaluate(t *testing.T) {
	reply := "Found the config. Let me grep the fresh server log for the grant line now."
	var gotPrompt string
	oneShot := func(_ context.Context, p string) (string, error) {
		gotPrompt = p
		return "VIOLATION: yes\nCHALLENGE: you announced a check you never ran", nil
	}
	v, err := FollowThroughCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: reply}, oneShot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "follow-through" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, reply) {
		t.Fatal("prompt must embed the reply text")
	}
	// A violation must carry a revise instruction telling the model to DO the
	// announced work, not to rewrite prose.
	if !strings.Contains(strings.ToLower(v.Revise), "tool call") {
		t.Fatalf("violation should carry an act-now revise instruction, got: %q", v.Revise)
	}

	// Non-violation clears revise.
	ok := func(_ context.Context, _ string) (string, error) { return "VIOLATION: no", nil }
	vn, err := FollowThroughCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: reply}, ok)
	if err != nil {
		t.Fatal(err)
	}
	if vn.Violation || vn.Revise != "" {
		t.Fatalf("non-violation must be clean, got: %+v", vn)
	}

	// nil oneShot → fail open
	if vo, _ := FollowThroughCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: reply}, nil); vo.Violation {
		t.Fatal("nil oneShot must fail open")
	}
}
