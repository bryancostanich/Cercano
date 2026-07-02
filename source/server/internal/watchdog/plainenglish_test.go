package watchdog

import (
	"context"
	"strings"
	"testing"
)

func TestPlainEnglishApplies(t *testing.T) {
	c := PlainEnglishCheck()
	long := strings.Repeat("word ", 20) // > 40 chars
	if !c.Applies(Action{Kind: "turn_end", Text: long}) {
		t.Fatal("a substantive turn_end reply should apply")
	}
	if c.Applies(Action{Kind: "turn_end", Text: "Done."}) {
		t.Fatal("a terse reply must be skipped")
	}
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Text: long}) {
		t.Fatal("tool_call must not apply")
	}
}

func TestPlainEnglishEvaluate(t *testing.T) {
	long := strings.Repeat("leverage synergies ", 5)
	var gotPrompt string
	oneShot := func(_ context.Context, p string) (string, error) {
		gotPrompt = p
		return "VIOLATION: yes\nCHALLENGE: drop the corporate jargon", nil
	}
	v, err := PlainEnglishCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: long}, oneShot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "plain-english" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, long) {
		t.Fatal("prompt must embed the reply text")
	}
	// nil oneShot → fail open
	if vn, _ := PlainEnglishCheck().Evaluate(context.Background(), Action{Kind: "turn_end", Text: long}, nil); vn.Violation {
		t.Fatal("nil oneShot must fail open")
	}
}
