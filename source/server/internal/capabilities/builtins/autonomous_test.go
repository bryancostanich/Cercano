package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

func TestSuggestAutonomous_EntersAutonomousProfile(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-1", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	var entered string
	svc := capabilities.Services{Autonomy: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := SuggestAutonomous().Execute(ctx, &capabilities.Call{
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

func TestSuggestAutonomous_PersistsRunBriefWhenStoreWired(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-brief", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	svc := capabilities.Services{
		Autonomy: store,
		EnterProfile:  func(convID, name string) error { return nil },
	}
	_, err = SuggestAutonomous().Execute(ctx, &capabilities.Call{
		ConversationID: "conv-brief",
		Args:           []byte(`{"reason":"plan accepted","goal":"ship autonomy","done_when":["brief saved",""],"constraints":["do not push"],"review_points":["storage"],"source_plan_path":"efforts/autonomy/plan.md","source_spec_path":"efforts/autonomy/spec.md"}`),
		Svc:            svc,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	run, err := store.GetAutonomyRun(ctx, "conv-brief")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "running" || run.SourceKind != "accepted_plan" || run.SourcePlanPath == "" || run.SourceSpecPath == "" {
		t.Fatalf("unexpected run metadata: %+v", run)
	}
	var brief conversation.AutonomyBrief
	if err := json.Unmarshal([]byte(run.BriefJSON), &brief); err != nil {
		t.Fatalf("unmarshal brief: %v", err)
	}
	if brief.Goal != "ship autonomy" || len(brief.DoneWhen) != 1 || brief.DoneWhen[0] != "brief saved" || brief.Constraints[0] != "do not push" || brief.ReviewPoints[0] != "storage" {
		t.Fatalf("unexpected brief: %+v", brief)
	}
	var revisions []conversation.AutonomyBriefRevision
	if err := json.Unmarshal([]byte(run.RevisionsJSON), &revisions); err != nil {
		t.Fatalf("unmarshal revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Number != 1 || revisions[0].Actor != "assistant" || revisions[0].Reason != "plan accepted" {
		t.Fatalf("unexpected revisions: %+v", revisions)
	}
	if run.DecisionsJSON != "[]" || run.ReviewJSON != "{}" {
		t.Fatalf("unexpected initial ledger payloads: decisions=%q review=%q", run.DecisionsJSON, run.ReviewJSON)
	}
}

func TestSuggestAutonomous_RequiresGoal(t *testing.T) {
	_, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"reason":"ok"}`), Svc: capabilities.Services{EnterProfile: func(string, string) error { return nil }}})
	if err == nil || !strings.Contains(err.Error(), "goal is required") {
		t.Fatalf("expected goal required error, got %v", err)
	}
}

func TestSuggestAutonomous_ErrorsWithoutProfileHook(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	_, err = SuggestAutonomous().Execute(ctx, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"goal":"ship"}`), Svc: capabilities.Services{Autonomy: store}})
	if err == nil || !strings.Contains(err.Error(), "no profile broker") {
		t.Fatalf("expected missing hook error, got %v", err)
	}
	run, getErr := store.GetLatestAutonomyRun(ctx, "conv")
	if getErr != nil {
		t.Fatalf("GetLatestAutonomyRun: %v", getErr)
	}
	if run.State != "abandoned" {
		t.Fatalf("failed profile entry should abandon created run, state=%q", run.State)
	}
}

func TestSuggestAutonomous_RequiresConversationStoreAndID(t *testing.T) {
	_, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{ConversationID: "conv", Args: []byte(`{"goal":"ship"}`), Svc: capabilities.Services{EnterProfile: func(string, string) error { return nil }}})
	if err == nil || !strings.Contains(err.Error(), "autonomy ledger is not available") {
		t.Fatalf("expected missing ledger error, got %v", err)
	}
	store, openErr := conversation.Open(":memory:")
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	defer store.Close()
	_, err = SuggestAutonomous().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"goal":"ship"}`), Svc: capabilities.Services{Autonomy: store, EnterProfile: func(string, string) error { return nil }}})
	if err == nil || !strings.Contains(err.Error(), "conversation id is required") {
		t.Fatalf("expected missing conversation id error, got %v", err)
	}
}

func TestAutoExit_LeavesAutonomousProfile(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv", State: "running", BriefJSON: `{"goal":"ship"}`}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}
	var entered string
	svc := capabilities.Services{Autonomy: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := AutoExit().Execute(ctx, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"reason":"blocked"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	if !strings.Contains(res.Text, "blocked") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	run, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "abandoned" {
		t.Fatalf("run.State = %q, want abandoned", run.State)
	}
}

func TestRequestAutonomousExit_CompletesWithoutDecisionReplay(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	decisionsJSON, _ := json.Marshal([]conversation.AutonomyDecision{{Sequence: 1, DecisionPoint: "storage shape", ChosenPath: "separate table", WhyCleanest: "clean ledger boundary", Reversibility: "moderate"}})
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv", State: "running", BriefJSON: `{"goal":"ship"}`, DecisionsJSON: string(decisionsJSON)}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}
	var entered string
	svc := capabilities.Services{Autonomy: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := RequestAutonomousExit().Execute(ctx, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"summary":"done","verification":"targeted tests passed"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("exit should leave autonomous mode, got profile %q; result: %s", entered, res.Text)
	}
	for _, unwanted := range []string{"storage shape", "Captured decisions", "complete_autonomous_review", "one by one"} {
		if strings.Contains(res.Text, unwanted) {
			t.Fatalf("exit replays decisions: %s", res.Text)
		}
	}
	run, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "completed" {
		t.Fatalf("run.State = %q, want completed", run.State)
	}
	if !strings.Contains(run.ReviewJSON, "targeted tests passed") {
		t.Fatalf("review json missing verification: %q", run.ReviewJSON)
	}
}

func TestRequestAutonomousExit_LegacyAndEmptyLedger(t *testing.T) {
	for _, state := range []string{"running", "review_pending"} {
		t.Run(state, func(t *testing.T) {
			store, call := completionTestCall(t, state)
			before, _ := store.GetActiveAutonomyRun(context.Background(), "conv")
			res, err := RequestAutonomousExit().Execute(context.Background(), call)
			if err != nil {
				t.Fatal(err)
			}
			if res.Text != "Autonomous run completed. Left autonomous mode." {
				t.Fatal(res.Text)
			}
			after, err := store.GetAutonomyRun(context.Background(), "conv")
			if err != nil {
				t.Fatal(err)
			}
			if after.State != "completed" || after.DecisionsJSON != before.DecisionsJSON || !strings.Contains(after.ReviewJSON, "completed_at") {
				t.Fatalf("unexpected completion: %+v", after)
			}
			if _, err := RequestAutonomousExit().Execute(context.Background(), call); err == nil {
				t.Fatal("completed run must not complete again")
			}
		})
	}
}

func completionTestCall(t *testing.T, state string) (conversation.Store, *capabilities.Call) {
	t.Helper()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv", State: state, BriefJSON: `{"goal":"ship"}`, DecisionsJSON: "[]", ReviewJSON: `{"legacy_detail":"preserved"}`}); err != nil {
		t.Fatal(err)
	}
	return store, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"summary":"done","verification":"tests passed"}`), Svc: capabilities.Services{Autonomy: store, EnterProfile: func(string, string) error { return nil }}}
}

func TestRequestAutonomousExit_FailuresPreserveActiveRun(t *testing.T) {
	for _, state := range []string{"running", "review_pending"} {
		for _, failure := range []string{"missing broker", "profile error", "write error", "bad args", "missing verification"} {
			t.Run(state+"/"+failure, func(t *testing.T) {
				store, call := completionTestCall(t, state)
				ctx := context.Background()
				before, _ := store.GetActiveAutonomyRun(ctx, "conv")
				switched := false
				call.Svc.EnterProfile = func(string, string) error { switched = true; return nil }
				switch failure {
				case "missing broker":
					call.Svc.EnterProfile = nil
				case "profile error":
					call.Svc.EnterProfile = func(string, string) error { return fmt.Errorf("profile unavailable") }
				case "write error":
					call.Svc.Autonomy = completionWriteFailure{Store: store}
				case "bad args":
					call.Args = []byte("{")
				case "missing verification":
					call.Args = []byte(`{"summary":"done"}`)
				}
				if _, err := RequestAutonomousExit().Execute(ctx, call); err == nil {
					t.Fatal("expected failure")
				}
				after, err := store.GetActiveAutonomyRun(ctx, "conv")
				if err != nil {
					t.Fatal(err)
				}
				if after.State != before.State || after.ReviewJSON != before.ReviewJSON || switched {
					t.Fatalf("failed exit changed state: %+v, switched=%v", after, switched)
				}
			})
		}
	}
}

type completionWriteFailure struct{ conversation.Store }

func (completionWriteFailure) UpdateAutonomyRun(context.Context, conversation.AutonomyRun) error {
	return fmt.Errorf("write unavailable")
}

func TestAutonomousLifecycle_StartCaptureComplete(t *testing.T) {
	store, call := completionTestCall(t, "running")
	ctx := context.Background()
	call.Args = minimalDecisionArgs("choose lifecycle shape")
	if _, err := CaptureDecision().Execute(ctx, call); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetActiveAutonomyRun(ctx, "conv")
	active := "autonomous"
	call.Svc.EnterProfile = func(_ string, name string) error { active = name; return nil }
	call.Args = []byte(`{"summary":"done","verification":"tests passed"}`)
	if _, err := RequestAutonomousExit().Execute(ctx, call); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatal(err)
	}
	if active != "default" || run.State != "completed" || run.DecisionsJSON != before.DecisionsJSON {
		t.Fatalf("unexpected completion: %+v profile=%s", run, active)
	}
	if _, err := CaptureDecision().Execute(ctx, call); err == nil {
		t.Fatal("capture after completion should fail")
	}
}
