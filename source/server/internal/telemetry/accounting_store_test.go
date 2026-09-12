package telemetry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func accountingFixture(id string) usage.AttemptObservation {
	return usage.AttemptObservation{ID: id, Revision: 1, Provider: "fake", Model: "fake", Attribution: usage.Attribution{Source: "main"}, StartedAt: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Outcome: usage.Started}
}
func accountingTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.InitializeAccounting(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestAccountingStoreIdempotencyAndPresence(t *testing.T) {
	s := accountingTestStore(t)
	ctx := t.Context()
	start := accountingFixture("attempt")
	final := start
	final.Revision = 3
	final.Outcome = usage.Completed
	final.EndedAt = final.StartedAt.Add(time.Second)
	final.Tokens = llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(0)}
	late := start
	late.Revision = 4
	for _, batch := range [][]usage.AttemptObservation{{start}, {final}, {final, start, late}} {
		if err := s.WriteAttempts(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.AccountingAttempt(ctx, start.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 3 || got.Outcome != usage.Completed || got.Tokens.Output != llm.ReportedTokens(0) || got.Tokens.CacheRead.Known {
		t.Fatalf("stored=%+v", got)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM inference_attempts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
func TestAccountingStoreLegacyIsolationAndCutover(t *testing.T) {
	s := accountingTestStore(t)
	ctx := t.Context()
	if err := s.RecordEvent(ctx, NewEvent("legacy", "fake")); err != nil {
		t.Fatal(err)
	}
	before, err := s.TrackingSince(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeAccounting(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.TrackingSince(ctx)
	if err != nil || !before.Equal(after) {
		t.Fatalf("cutover changed: %v %v %v", before, after, err)
	}
	var legacy, current int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM inference_attempts`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if legacy != 1 || current != 0 {
		t.Fatalf("legacy=%d current=%d", legacy, current)
	}
}
func TestAccountingStoreAtomicValidation(t *testing.T) {
	s := accountingTestStore(t)
	good := accountingFixture("good")
	bad := good
	bad.ID = "bad"
	bad.Tokens.Input = llm.TokenCount{Known: true, Value: -1}
	if err := s.WriteAttempts(t.Context(), []usage.AttemptObservation{good, bad}); err == nil {
		t.Fatal("invalid count accepted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inference_attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial batch count=%d err=%v", count, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WriteAttempts(ctx, []usage.AttemptObservation{good}); err == nil {
		t.Fatal("canceled write succeeded")
	}
}
func TestAccountingStoreUTCAndIndexes(t *testing.T) {
	s := accountingTestStore(t)
	a := accountingFixture("tz")
	a.StartedAt = a.StartedAt.In(time.FixedZone("offset", 3600))
	if err := s.WriteAttempts(t.Context(), []usage.AttemptObservation{a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AccountingAttempt(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(a.StartedAt) || got.StartedAt.Location() != time.UTC {
		t.Fatalf("time=%v", got.StartedAt)
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM inference_attempts WHERE provider=? AND model=? AND started_at>=? AND started_at<?`, "fake", "fake", 0, time.Now().UnixMicro())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		indexed = indexed || strings.Contains(detail, "USING INDEX inference_attempts_provider_model_time")
	}
	if !indexed {
		t.Fatal("attribution/range query did not use intended index")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountingStoreRejectsFutureSchema(t *testing.T) {
	s := accountingTestStore(t)
	if _, err := s.db.Exec(`UPDATE accounting_metadata SET value='999' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeAccounting(t.Context()); err == nil {
		t.Fatal("future schema silently accepted")
	}
	var version string
	if err := s.db.QueryRow(`SELECT value FROM accounting_metadata WHERE key='schema_version'`).Scan(&version); err != nil || version != "999" {
		t.Fatalf("version modified: %q %v", version, err)
	}
}
