package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutonomyRun_CreateActiveAndLatestRoundTrip(t *testing.T) {
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

	created, err := store.CreateAutonomyRun(ctx, AutonomyRun{
		ConversationID: "conv-auto",
		State:          "running",
		SourceKind:     "direct_user_request",
		BriefJSON:      string(briefJSON),
		RevisionsJSON:  string(revsJSON),
		DecisionsJSON:  decisions,
	})
	if err != nil {
		t.Fatalf("CreateAutonomyRun: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("CreateAutonomyRun should assign RunID")
	}

	got, err := store.GetActiveAutonomyRun(ctx, "conv-auto")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.RunID != created.RunID || got.ConversationID != "conv-auto" || got.State != "running" || got.SourceKind != "direct_user_request" {
		t.Fatalf("unexpected run metadata: %+v", got)
	}
	if got.BriefJSON != string(briefJSON) || got.RevisionsJSON != string(revsJSON) || got.DecisionsJSON != decisions {
		t.Fatalf("json payloads did not round trip: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps should be set: %+v", got)
	}

	latest, err := store.GetLatestAutonomyRun(ctx, "conv-auto")
	if err != nil {
		t.Fatalf("GetLatestAutonomyRun: %v", err)
	}
	if latest.RunID != created.RunID {
		t.Fatalf("latest RunID = %q, want %q", latest.RunID, created.RunID)
	}
}

func TestAutonomyRun_AppendOnlyAllowsMultipleHistoricalRuns(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	first, err := store.CreateAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "completed", BriefJSON: `{"goal":"first"}`, CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(100, 0)})
	if err != nil {
		t.Fatalf("CreateAutonomyRun first: %v", err)
	}
	second, err := store.CreateAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "abandoned", BriefJSON: `{"goal":"second"}`, CreatedAt: time.Unix(200, 0), UpdatedAt: time.Unix(200, 0)})
	if err != nil {
		t.Fatalf("CreateAutonomyRun second: %v", err)
	}
	if first.RunID == second.RunID {
		t.Fatal("append-only runs should have distinct run ids")
	}

	runs, err := store.ListAutonomyRuns(ctx, "conv")
	if err != nil {
		t.Fatalf("ListAutonomyRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].RunID != second.RunID || runs[1].RunID != first.RunID {
		t.Fatalf("runs should be newest first: %+v", runs)
	}
	if _, err := store.GetActiveAutonomyRun(ctx, "conv"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed/abandoned history should not be active, err=%v", err)
	}
}

func TestAutonomyRun_OneActiveRunPerConversation(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	if _, err := store.CreateAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "running", BriefJSON: `{"goal":"first"}`}); err != nil {
		t.Fatalf("CreateAutonomyRun running: %v", err)
	}
	if _, err := store.CreateAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "review_pending", BriefJSON: `{"goal":"second"}`}); err == nil {
		t.Fatal("second active run should violate unique active-run invariant")
	}
}

func TestAutonomyRun_UpdateByRunID(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.EnsureConversation(ctx, "conv", "/proj", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	created, err := store.CreateAutonomyRun(ctx, AutonomyRun{ConversationID: "conv", State: "running", BriefJSON: `{"goal":"draft"}`})
	if err != nil {
		t.Fatalf("CreateAutonomyRun: %v", err)
	}
	created.State = "review_pending"
	created.ReviewJSON = `{"summary":"done"}`
	if err := store.UpdateAutonomyRun(ctx, created); err != nil {
		t.Fatalf("UpdateAutonomyRun: %v", err)
	}
	got, err := store.GetActiveAutonomyRun(ctx, "conv")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun: %v", err)
	}
	if got.RunID != created.RunID || got.State != "review_pending" || got.ReviewJSON != `{"summary":"done"}` {
		t.Fatalf("update did not round trip: %+v", got)
	}
}

func TestAutonomyRun_MissingReturnsNoRows(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	_, err = store.GetActiveAutonomyRun(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveAutonomyRun missing err = %v, want sql.ErrNoRows", err)
	}
	_, err = store.GetLatestAutonomyRun(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetLatestAutonomyRun missing err = %v, want sql.ErrNoRows", err)
	}
}

func TestAutonomyRun_RequiresConversationID(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	_, err = store.CreateAutonomyRun(context.Background(), AutonomyRun{})
	if err == nil || !strings.Contains(err.Error(), "conversation id required") {
		t.Fatalf("expected conversation id error, got %v", err)
	}
}

func TestAutonomyRun_MigratesLegacySingleRowToAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;
		CREATE TABLE conversations (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '', project_dir TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', title_source TEXT NOT NULL DEFAULT 'user',
			started_at INTEGER NOT NULL, last_turn_at INTEGER NOT NULL, recap TEXT NOT NULL DEFAULT '', recap_updated_at INTEGER NOT NULL DEFAULT 0,
			kind TEXT NOT NULL DEFAULT 'main', parent_id TEXT NOT NULL DEFAULT '', precursor_id TEXT NOT NULL DEFAULT '', granted_tools TEXT NOT NULL DEFAULT '', dismissed INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE autonomy_runs (
			conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
			state TEXT NOT NULL DEFAULT 'proposed', source_kind TEXT NOT NULL DEFAULT '', source_plan_path TEXT NOT NULL DEFAULT '', source_spec_path TEXT NOT NULL DEFAULT '',
			brief_json TEXT NOT NULL DEFAULT '', revisions_json TEXT NOT NULL DEFAULT '', decisions_json TEXT NOT NULL DEFAULT '', review_json TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
		INSERT INTO conversations (id, started_at, last_turn_at) VALUES ('legacy', 1, 1);
		INSERT INTO autonomy_runs (conversation_id, state, source_kind, brief_json, decisions_json, review_json, created_at, updated_at)
		VALUES ('legacy', 'review_pending', 'accepted_plan', '{"goal":"legacy"}', '[{"decision_point":"d"}]', '{"summary":"done"}', 10, 20);`); err != nil {
		db.Close()
		t.Fatalf("seed legacy db: %v", err)
	}
	db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer store.Close()

	runs, err := store.ListAutonomyRuns(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("ListAutonomyRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	got := runs[0]
	if got.RunID == "" || got.ConversationID != "legacy" || got.State != "review_pending" || got.SourceKind != "accepted_plan" {
		t.Fatalf("legacy metadata not preserved: %+v", got)
	}
	if got.BriefJSON != `{"goal":"legacy"}` || got.DecisionsJSON != `[{"decision_point":"d"}]` || got.ReviewJSON != `{"summary":"done"}` {
		t.Fatalf("legacy JSON not preserved: %+v", got)
	}
	if _, err := store.CreateAutonomyRun(context.Background(), AutonomyRun{ConversationID: "legacy", State: "running"}); err == nil {
		t.Fatal("migrated review_pending run should still count as active")
	}
}
