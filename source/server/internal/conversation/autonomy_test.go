package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAutonomyRun_SaveGetRoundTrip(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "conv-auto", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	brief := AutonomyBrief{
		Goal:         "implement autonomous protocol",
		DoneWhen:     []string{"profile active", "ledger persists"},
		Constraints:  []string{"do not push"},
		ReviewPoints: []string{"storage shape"},
	}
	briefJSON, _ := json.Marshal(brief)
	revsJSON, _ := json.Marshal([]AutonomyBriefRevision{{Number: 1, Actor: "assistant", Reason: "initial autonomous brief", Brief: brief}})
	decisions := `[{"decision_point":"storage shape","chosen_path":"separate table"}]`

	if err := store.SaveAutonomyRun(ctx, AutonomyRun{
		ConversationID: "conv-auto",
		State:          "running",
		SourceKind:     "direct_user_request",
		BriefJSON:      string(briefJSON),
		RevisionsJSON:  string(revsJSON),
		DecisionsJSON:  decisions,
	}); err != nil {
		t.Fatalf("SaveAutonomyRun: %v", err)
	}

	got, err := store.GetAutonomyRun(ctx, "conv-auto")
	if err != nil {
		t.Fatalf("GetAutonomyRun: %v", err)
	}
	if got.ConversationID != "conv-auto" || got.State != "running" || got.SourceKind != "direct_user_request" {
		t.Fatalf("unexpected run metadata: %+v", got)
	}
	if got.BriefJSON != string(briefJSON) || got.RevisionsJSON != string(revsJSON) || got.DecisionsJSON != decisions {
		t.Fatalf("json payloads did not round trip: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps should be set: %+v", got)
	}
}

func TestAutonomyRun_UpsertPreservesCreatedAtAndUpdatesState(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "proposed", BriefJSON: `{"goal":"draft"}`}); err != nil {
		t.Fatalf("SaveAutonomyRun first: %v", err)
	}
	first, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun first: %v", err)
	}
	if err := store.SaveAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "abandoned", BriefJSON: `{"goal":"draft"}`, CreatedAt: first.CreatedAt}); err != nil {
		t.Fatalf("SaveAutonomyRun second: %v", err)
	}
	got, err := store.GetAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetAutonomyRun second: %v", err)
	}
	if got.State != "abandoned" {
		t.Fatalf("State = %q, want abandoned", got.State)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt changed: got %v want %v", got.CreatedAt, first.CreatedAt)
	}
}

func TestAutonomyRun_MissingReturnsNoRows(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	_, err = store.GetAutonomyRun(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetAutonomyRun missing err = %v, want sql.ErrNoRows", err)
	}
}

func TestAutonomyRun_RequiresConversationID(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	err = store.SaveAutonomyRun(context.Background(), AutonomyRun{})
	if err == nil || !strings.Contains(err.Error(), "conversation id required") {
		t.Fatalf("expected conversation id error, got %v", err)
	}
}
