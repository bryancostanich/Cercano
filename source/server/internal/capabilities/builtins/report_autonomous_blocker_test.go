package builtins

import (
	"context"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/conversation"
)

// blockerEnv seeds an in-memory store with a conversation and, when state is
// non-empty, an active autonomy run in that state.
func blockerEnv(t *testing.T, conv, state string) conversation.Store {
	t.Helper()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, conv, "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if state == "" {
		return store
	}
	if err := store.SaveAutonomyRun(ctx, conversation.AutonomyRun{
		ConversationID: conv,
		State:          state,
		BriefJSON:      `{"goal":"ship the feature","done_when":["tests pass"]}`,
		DecisionsJSON:  `[]`,
		ReviewJSON:     `{}`,
	}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}
	return store
}

func callBlocker(t *testing.T, store conversation.Store, conv, args string) (*capabilities.Result, error) {
	t.Helper()
	return ReportAutonomousBlocker().Execute(context.Background(), &capabilities.Call{
		ConversationID: conv,
		Args:          []byte(args),
		Svc:           capabilities.Services{Autonomy: store},
	})
}

// TestReportAutonomousBlocker_RecordsBlocker_PreservesApprovals pins the happy
// path: on a running run, report_autonomous_blocker records a structured
// blocker carrying the required reason and leaves every other piece of run
// state — notably the approval state "running" — untouched. The run does not
// exit and does not flip to review_pending; brief/decisions survive intact.
func TestReportAutonomousBlocker_RecordsBlocker_PreservesApprovals(t *testing.T) {
	store := blockerEnv(t, "conv-blocker-1", "running")

	res, err := callBlocker(t, store, "conv-blocker-1", `{"reason":"need production API credentials to finish the deploy"}`)
	if err != nil {
		t.Fatalf("report_autonomous_blocker: %v", err)
	}
	if res == nil || res.Text == "" {
		t.Fatal("expected non-empty acknowledgment result")
	}

	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-blocker-1")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	// Approval semantics preserved: state untouched, still running.
	if cur.State != "running" {
		t.Fatalf("state must stay \"running\" while blocked, got %q", cur.State)
	}
	if cur.BriefJSON != `{"goal":"ship the feature","done_when":["tests pass"]}` || cur.DecisionsJSON != "[]" {
		t.Fatal("brief/decisions must be preserved by the blocker write")
	}
	blk, ok := cur.ActiveBlocker()
	if !ok {
		t.Fatal("expected a recorded blocker")
	}
	if blk.Reason != "need production API credentials to finish the deploy" {
		t.Fatalf("blocker reason not persisted: got %q", blk.Reason)
	}
	if blk.RecordedAt.IsZero() {
		t.Fatal("blocker must carry a timestamp")
	}
}

// TestReportAutonomousBlocker_ReasonRequired pins that the pause reason is
// mandatory: without it there is no durable record of what the user must act
// on, so the capability refuses rather than pausing silently.
func TestReportAutonomousBlocker_ReasonRequired(t *testing.T) {
	store := blockerEnv(t, "conv-blocker-2", "running")
	for _, args := range []string{
		`{}`,
		`{"reason":""}`,
		`{"reason":"   "}`,
	} {
		if _, err := callBlocker(t, store, "conv-blocker-2", args); err == nil {
			t.Fatalf("expected error for args %s: reason is required", args)
		}
	}
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-blocker-2")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if _, blocked := cur.ActiveBlocker(); blocked {
		t.Fatal("a refused call must not record a blocker")
	}
}

// TestReportAutonomousBlocker_PendingApprovalRefused pins the review_pending
// guard: the blocker cap is a live-run pause, not a way to mutate a run parked
// waiting for the human's exit-review verdict. A review_pending run refuses the
// call and its durable state is unchanged.
func TestReportAutonomousBlocker_PendingApprovalRefused(t *testing.T) {
	store := blockerEnv(t, "conv-blocker-3", "review_pending")
	if _, err := callBlocker(t, store, "conv-blocker-3", `{"reason":"cannot proceed"}`); err == nil {
		t.Fatal("expected review_pending run to refuse report_autonomous_blocker")
	}
	cur, err := store.GetActiveAutonomyRun(context.Background(), "conv-blocker-3")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if cur.State != "review_pending" {
		t.Fatalf("state must stay review_pending, got %q", cur.State)
	}
	if _, blocked := cur.ActiveBlocker(); blocked {
		t.Fatal("no blocker may be recorded on a refused review_pending call")
	}
}

// TestReportAutonomousBlocker_NoActiveRunRefused pins the no-ledger guard:
// without an active run there is nothing to pause, so the call errors.
func TestReportAutonomousBlocker_NoActiveRunRefused(t *testing.T) {
	store := blockerEnv(t, "conv-blocker-4", "")
	if _, err := callBlocker(t, store, "conv-blocker-4", `{"reason":"blocked outside a run"}`); err == nil {
		t.Fatal("expected no-active-run call to error")
	}
}
