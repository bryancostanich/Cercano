package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

func TestRequestAutonomousExecution_Meta(t *testing.T) {
	c := RequestAutonomousExecution()
	if c.Name() != "request_autonomous_execution" {
		t.Fatalf("Name() = %q", c.Name())
	}
	if c.Tier() != capabilities.TierX {
		t.Fatalf("Tier() = %q, want TierX", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) || c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Fatalf("request_autonomous_execution should be agent-only, got %v", c.Surfaces())
	}
}

func TestRequestAutonomousExecution_ExecuteStartsAutonomousForApprovedPlan(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-plan-auto", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	var enteredConv, enteredName string
	svc := capabilities.Services{
		Conversations: store,
		EnterProfile: func(convID, name string) error {
			enteredConv = convID
			enteredName = name
			return nil
		},
	}
	res, err := RequestAutonomousExecution().Execute(ctx, &capabilities.Call{
		ConversationID: "conv-plan-auto",
		Args:           []byte(`{"effort":"efforts/demo","summary":"three phases","spec_path":"efforts/demo/spec.md","plan_path":"efforts/demo/plan.md","reason":"approved plan is bounded","goal":"ship demo","done_when":["tests pass", ""],"constraints":["do not push"],"review_points":["protocol wording"]}`),
		Svc:            svc,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if enteredConv != "conv-plan-auto" || enteredName != "autonomous" {
		t.Fatalf("EnterProfile called with (%q, %q), want (conv-plan-auto, autonomous)", enteredConv, enteredName)
	}
	for _, want := range []string{"Autonomous execution approved", "Entered autonomous mode", "efforts/demo", "three phases", "spec.md", "plan.md"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("result missing %q: %q", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "suggest_autonomous") {
		t.Fatalf("result should not instruct a second gate: %q", res.Text)
	}
	run, err := store.GetAutonomyRun(ctx, "conv-plan-auto")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if run.State != "running" || run.SourceKind != "accepted_plan" || run.SourcePlanPath != "efforts/demo/plan.md" || run.SourceSpecPath != "efforts/demo/spec.md" {
		t.Fatalf("unexpected run metadata: %+v", run)
	}
	var brief conversation.AutonomyBrief
	if err := json.Unmarshal([]byte(run.BriefJSON), &brief); err != nil {
		t.Fatalf("unmarshal brief: %v", err)
	}
	if brief.Goal != "ship demo" || len(brief.DoneWhen) != 1 || brief.DoneWhen[0] != "tests pass" || brief.Constraints[0] != "do not push" || brief.ReviewPoints[0] != "protocol wording" {
		t.Fatalf("unexpected brief: %+v", brief)
	}
	var revisions []conversation.AutonomyBriefRevision
	if err := json.Unmarshal([]byte(run.RevisionsJSON), &revisions); err != nil {
		t.Fatalf("unmarshal revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Reason != "approved plan is bounded" {
		t.Fatalf("unexpected revisions: %+v", revisions)
	}
}

func TestRequestAutonomousExecution_RejectsExistingActiveRun(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-active", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if _, err := store.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-active", State: "running", BriefJSON: `{"goal":"first"}`}); err != nil {
		t.Fatalf("CreateAutonomyRun: %v", err)
	}
	_, err = RequestAutonomousExecution().Execute(ctx, &capabilities.Call{
		ConversationID: "conv-active",
		Args:           []byte(`{"goal":"second"}`),
		Svc: capabilities.Services{
			Conversations: store,
			EnterProfile:  func(string, string) error { return nil },
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected active-run rejection, got %v", err)
	}
	runs, listErr := store.ListAutonomyRuns(ctx, "conv-active")
	if listErr != nil {
		t.Fatalf("ListAutonomyRuns: %v", listErr)
	}
	if len(runs) != 1 || runs[0].State != "running" || runs[0].BriefJSON != `{"goal":"first"}` {
		t.Fatalf("active-run rejection should leave existing run unchanged: %+v", runs)
	}
}

func TestRequestAutonomousExecution_RequiresGoal(t *testing.T) {
	_, err := RequestAutonomousExecution().Execute(context.Background(), &capabilities.Call{
		Args: []byte(`{"effort":"efforts/demo"}`),
		Svc:  capabilities.Services{EnterProfile: func(string, string) error { return nil }},
	})
	if err == nil || !strings.Contains(err.Error(), "goal is required") {
		t.Fatalf("expected goal required error, got %v", err)
	}
}
