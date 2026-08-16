package builtins

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestSuggestAutonomous_EntersAutonomousProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{
		ConversationID: "conv-1",
		Args:           []byte(`{"reason":"multi-step implementation","goal":"ship autonomous profile","done_when":["tests pass"],"constraints":["do not push"],"review_points":["API shape"]}`),
		Svc:            svc,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "autonomous" {
		t.Fatalf("EnterProfile called with %q, want autonomous", entered)
	}
	if !strings.Contains(res.Text, "Entered autonomous mode") || !strings.Contains(res.Text, "multi-step implementation") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
}

func TestSuggestAutonomous_RequiresGoal(t *testing.T) {
	_, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"reason":"ok"}`), Svc: capabilities.Services{EnterProfile: func(string, string) error { return nil }}})
	if err == nil || !strings.Contains(err.Error(), "goal is required") {
		t.Fatalf("expected goal required error, got %v", err)
	}
}

func TestSuggestAutonomous_ErrorsWithoutProfileHook(t *testing.T) {
	_, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"goal":"ship"}`)})
	if err == nil || !strings.Contains(err.Error(), "no profile broker") {
		t.Fatalf("expected missing hook error, got %v", err)
	}
}

func TestAutoExit_LeavesAutonomousProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := AutoExit().Execute(context.Background(), &capabilities.Call{ConversationID: "conv", Args: []byte(`{"reason":"blocked"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	if !strings.Contains(res.Text, "blocked") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
}

func TestRequestAutonomousExit_LeavesAutonomousProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := RequestAutonomousExit().Execute(context.Background(), &capabilities.Call{ConversationID: "conv", Args: []byte(`{"summary":"done","verification":"targeted tests passed"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	for _, want := range []string{"exited autonomous mode", "Summary: done", "Verification: targeted tests passed"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("result missing %q: %q", want, res.Text)
		}
	}
}
