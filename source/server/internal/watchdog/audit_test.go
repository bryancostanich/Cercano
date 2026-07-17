package watchdog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

type oneShotCheck struct{}

func (oneShotCheck) Name() string        { return "one-shot-check" }
func (oneShotCheck) Applies(Action) bool { return true }
func (oneShotCheck) Evaluate(ctx context.Context, _ Action, oneShot OneShotFunc) (Verdict, error) {
	text, err := oneShot(ctx, "watchdog prompt text")
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("design-decisions", text), nil
}

func TestSQLiteAuditLogRecordsEvaluationAndResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog-audit.db")
	audit, err := OpenSQLiteAuditLog(path)
	if err != nil {
		t.Fatalf("OpenSQLiteAuditLog: %v", err)
	}
	defer audit.Close()

	w := New(Config{
		Mode:           ModeChallenge,
		Audit:          audit,
		AuditPrompts:   true,
		AuditResponses: true,
	}, []Check{oneShotCheck{}}, func(context.Context, string) (string, error) {
		return "VIOLATION: yes\nCHALLENGE: stop and decide", nil
	})

	d := w.Gate(context.Background(), "conv-a", Action{Kind: "tool_call", ToolName: "Edit", ToolArgs: []byte(`{"path":"x.go"}`)})
	if d.Action != "challenge" {
		t.Fatalf("Gate action = %q, want challenge", d.Action)
	}

	db := openAuditDB(t, path)
	defer db.Close()

	var evals int
	if err := db.QueryRow(`SELECT count(*) FROM watchdog_events WHERE event_type='evaluation' AND check_name='one-shot-check' AND violation=1 AND prompt='watchdog prompt text' AND response LIKE '%VIOLATION: yes%'`).Scan(&evals); err != nil {
		t.Fatalf("query evaluation event: %v", err)
	}
	if evals != 1 {
		t.Fatalf("evaluation event count = %d, want 1", evals)
	}

	var resolutions int
	if err := db.QueryRow(`SELECT count(*) FROM watchdog_events WHERE event_type='resolution' AND conversation_id='conv-a' AND resolution='challenge' AND protocol='design-decisions' AND challenge='stop and decide'`).Scan(&resolutions); err != nil {
		t.Fatalf("query resolution event: %v", err)
	}
	if resolutions != 1 {
		t.Fatalf("challenge resolution event count = %d, want 1", resolutions)
	}
}

func TestSQLiteAuditLogRecordsJustifyResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog-audit.db")
	audit, err := OpenSQLiteAuditLog(path)
	if err != nil {
		t.Fatalf("OpenSQLiteAuditLog: %v", err)
	}
	defer audit.Close()

	w := New(Config{Mode: ModeChallenge, Audit: audit}, []Check{fakeCheck{applies: true, verdict: Verdict{Violation: true, Protocol: "systematic-debugging", Challenge: "probe first"}}}, nil)
	if d := w.Gate(context.Background(), "conv-b", editAction()); d.Action != "challenge" {
		t.Fatalf("Gate action = %q, want challenge", d.Action)
	}
	key := w.lastChallengedKey("conv-b")
	if key == "" {
		t.Fatal("expected last challenged key")
	}
	w.recordJustify("conv-b", key)
	w.recordAudit(context.Background(), AuditEvent{ConversationID: "conv-b", EventType: "resolution", Decision: "allow", Key: key, Resolution: "justify", Reason: "test override"})

	db := openAuditDB(t, path)
	defer db.Close()
	var justifies int
	if err := db.QueryRow(`SELECT count(*) FROM watchdog_events WHERE event_type='resolution' AND conversation_id='conv-b' AND resolution='justify' AND reason='test override'`).Scan(&justifies); err != nil {
		t.Fatalf("query justify event: %v", err)
	}
	if justifies != 1 {
		t.Fatalf("justify resolution event count = %d, want 1", justifies)
	}
}

func openAuditDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open audit db for query: %v", err)
	}
	return db
}
