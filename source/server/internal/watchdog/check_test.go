package watchdog

import (
	"context"
	"testing"
)

type fakeCheck struct {
	applies bool
	verdict Verdict
}

func (fakeCheck) Name() string            { return "fake" }
func (f fakeCheck) Applies(a Action) bool { return f.applies }
func (f fakeCheck) Evaluate(_ context.Context, _ Action, _ OneShotFunc) (Verdict, error) {
	return f.verdict, nil
}

func TestCheckInterface(t *testing.T) {
	var c Check = fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "fake", Challenge: "x"}}
	if !c.Applies(Action{Kind: "tool_call"}) {
		t.Fatal("Applies should be true")
	}
	v, err := c.Evaluate(context.Background(), Action{}, nil)
	if err != nil || !v.Violation {
		t.Fatalf("Evaluate: %+v %v", v, err)
	}
}
