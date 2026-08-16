package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
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
		Conversations: store,
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
	_, err := SuggestAutonomous().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"goal":"ship"}`)})
	if err == nil || !strings.Contains(err.Error(), "no profile broker") {
		t.Fatalf("expected missing hook error, got %v", err)
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
	svc := capabilities.Services{Conversations: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
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

func TestRequestAutonomousExit_EntersReviewPendingAndKeepsAutonomousProfile(t *testing.T) {
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
	svc := capabilities.Services{Conversations: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := RequestAutonomousExit().Execute(ctx, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"summary":"done","verification":"targeted tests passed"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "" {
		t.Fatalf("request_autonomous_exit should keep autonomous profile active during review; EnterProfile called with %q", entered)
	}
	for _, want := range []string{"ready for final review", "Summary: done", "Verification: targeted tests passed", "Captured decisions to review", "storage shape", "complete_autonomous_review"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("result missing %q: %q", want, res.Text)
		}
	}
	run, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "review_pending" {
		t.Fatalf("run.State = %q, want review_pending", run.State)
	}
	if !strings.Contains(run.ReviewJSON, "targeted tests passed") {
		t.Fatalf("review json missing verification: %q", run.ReviewJSON)
	}
}

func TestCompleteAutonomousReview_MarksCompletedAndLeavesProfile(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv", State: "review_pending", BriefJSON: `{"goal":"ship"}`, ReviewJSON: `{"summary":"done"}`}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}
	var entered string
	svc := capabilities.Services{Conversations: store, EnterProfile: func(convID, name string) error { entered = name; return nil }}
	res, err := CompleteAutonomousReview().Execute(ctx, &capabilities.Call{ConversationID: "conv", Args: []byte(`{"summary":"decisions accepted"}`), Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	if !strings.Contains(res.Text, "decision review complete") || !strings.Contains(res.Text, "decisions accepted") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	run, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "completed" {
		t.Fatalf("run.State = %q, want completed", run.State)
	}
	if !strings.Contains(run.ReviewJSON, "completed_at") || !strings.Contains(run.ReviewJSON, "decisions accepted") {
		t.Fatalf("review json not completed: %q", run.ReviewJSON)
	}
}
