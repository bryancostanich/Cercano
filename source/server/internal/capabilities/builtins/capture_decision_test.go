package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

func TestCaptureDecision_PersistsStructuredDecision(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-decision", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-decision", State: "running", BriefJSON: `{"goal":"ship"}`, DecisionsJSON: "[]", ReviewJSON: "{}"}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}

	res, err := CaptureDecision().Execute(ctx, &capabilities.Call{
		ConversationID: "conv-decision",
		Args: []byte(`{
			"decision_point":"Choose autonomy ledger storage shape",
			"options":[
				{"title":"JSON columns on conversations","cost":"low","risk":"bloats conversations","reward":"fast","side_effects":"harder future migration","hack_flags":["catch-all table"]},
				{"title":"Separate autonomy_runs table","cost":"medium-low","risk":"one new table","reward":"clean ledger boundary","side_effects":"future job model can wrap it","hack_flags":[]}
			],
			"counterarguments":[{"option":"JSON columns on conversations","strongest_case":"least implementation work"}],
			"recommendation":"Separate autonomy_runs table",
			"chosen_path":"Separate autonomy_runs table",
			"why_cleanest":"Keeps the ledger isolated without over-normalizing V1.",
			"reversibility":"moderate",
			"stop_required":false
		}`),
		Svc: capabilities.Services{Conversations: store},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "Captured autonomous decision #1") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	run, err := store.GetAutonomyRun(ctx, "conv-decision")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	var decisions []conversation.AutonomyDecision
	if err := json.Unmarshal([]byte(run.DecisionsJSON), &decisions); err != nil {
		t.Fatalf("unmarshal decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("len(decisions) = %d, want 1", len(decisions))
	}
	got := decisions[0]
	if got.Sequence != 1 || got.DecisionPoint != "Choose autonomy ledger storage shape" || got.ChosenPath != "Separate autonomy_runs table" || got.Reversibility != "moderate" {
		t.Fatalf("unexpected decision: %+v", got)
	}
	if got.Timestamp.IsZero() {
		t.Fatalf("timestamp should be assigned: %+v", got)
	}
	if len(got.Options) != 2 || got.Options[0].HackFlags[0] != "catch-all table" {
		t.Fatalf("options/hack flags did not persist: %+v", got.Options)
	}
	if got.Counterarguments[0].StrongestCase != "least implementation work" {
		t.Fatalf("counterarguments did not persist: %+v", got.Counterarguments)
	}
}

func TestCaptureDecision_AppendsInOrder(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-order", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	first := []conversation.AutonomyDecision{{Sequence: 1, DecisionPoint: "first", ChosenPath: "A"}}
	firstJSON, _ := json.Marshal(first)
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-order", State: "running", DecisionsJSON: string(firstJSON)}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}
	_, err = CaptureDecision().Execute(ctx, &capabilities.Call{ConversationID: "conv-order", Args: minimalDecisionArgs("second"), Svc: capabilities.Services{Conversations: store}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	run, err := store.GetAutonomyRun(ctx, "conv-order")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	var decisions []conversation.AutonomyDecision
	if err := json.Unmarshal([]byte(run.DecisionsJSON), &decisions); err != nil {
		t.Fatalf("unmarshal decisions: %v", err)
	}
	if len(decisions) != 2 || decisions[1].Sequence != 2 || decisions[1].DecisionPoint != "second" {
		t.Fatalf("decisions not appended in order: %+v", decisions)
	}
}

func TestCaptureDecision_ValidatesRequiredProtocolFields(t *testing.T) {
	_, err := CaptureDecision().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"decision_point":"thin","options":[{"title":"only one"}],"recommendation":"x","chosen_path":"x","why_cleanest":"x","reversibility":"easy","stop_required":false}`), Svc: capabilities.Services{}})
	if err == nil || !strings.Contains(err.Error(), "at least two real options") {
		t.Fatalf("expected options validation error, got %v", err)
	}
}

func TestCaptureDecision_RequiresStopReasonWhenStopping(t *testing.T) {
	_, err := CaptureDecision().Execute(context.Background(), &capabilities.Call{Args: minimalDecisionArgsWithStop("risky", true, ""), Svc: capabilities.Services{}})
	if err == nil || !strings.Contains(err.Error(), "stop_reason is required") {
		t.Fatalf("expected stop_reason validation error, got %v", err)
	}
}

func TestCaptureDecision_RequiresLedger(t *testing.T) {
	_, err := CaptureDecision().Execute(context.Background(), &capabilities.Call{ConversationID: "conv", Args: minimalDecisionArgs("no store"), Svc: capabilities.Services{}})
	if err == nil || !strings.Contains(err.Error(), "autonomy ledger is not available") {
		t.Fatalf("expected missing ledger error, got %v", err)
	}
}

func minimalDecisionArgs(point string) []byte {
	return minimalDecisionArgsWithStop(point, false, "")
}

func minimalDecisionArgsWithStop(point string, stop bool, reason string) []byte {
	stopReason := ""
	if reason != "" {
		stopReason = `,"stop_reason":"` + reason + `"`
	}
	return []byte(`{
		"decision_point":"` + point + `",
		"options":[
			{"title":"A","cost":"low","risk":"low","reward":"works","side_effects":"few"},
			{"title":"B","cost":"medium","risk":"medium","reward":"also works","side_effects":"some","hack_flags":["shortcut"]}
		],
		"counterarguments":[{"option":"B","strongest_case":"faster"}],
		"recommendation":"A",
		"chosen_path":"A",
		"why_cleanest":"It solves the problem without the shortcut.",
		"reversibility":"easy",
		"stop_required":` + map[bool]string{true: "true", false: "false"}[stop] + stopReason + `
	}`)
}
