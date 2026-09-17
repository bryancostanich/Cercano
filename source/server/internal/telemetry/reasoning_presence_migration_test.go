package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func presenceAttempt(id string, tokens llm.TokenUsage) usage.AttemptObservation {
	start := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	return usage.AttemptObservation{
		ID: id, Revision: 2,
		Attribution: usage.Attribution{OperationID: "op", ConversationID: "conv", SessionID: "sess", WorkerID: "w", Source: "main"},
		Provider:    "deepinfra", Model: "m", Profile: "p", Destination: "primary",
		StartedAt: start, EndedAt: start.Add(time.Second), Outcome: usage.Completed, Tokens: tokens,
	}
}

// A database created before the presence columns existed must be migrated
// additively: old rows stay NULL (unknown), new rows round-trip evidence.
func TestAccountingMigrationAddsReasoningPresence(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InitializeAccounting(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-presence population by dropping the new columns.
	for _, stmt := range []string{
		`ALTER TABLE inference_attempts DROP COLUMN reasoning_chunks`,
		`ALTER TABLE inference_attempts DROP COLUMN reasoning_bytes`,
	} {
		if _, err = store.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.InitializeAccounting(ctx); err != nil {
		t.Fatalf("re-initialize on legacy schema: %v", err)
	}
	if err = store.InitializeAccounting(ctx); err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}

	legacy := presenceAttempt("legacy", llm.TokenUsage{Input: llm.ReportedTokens(10), Output: llm.ReportedTokens(4)})
	measured := presenceAttempt("measured", llm.TokenUsage{
		Input: llm.ReportedTokens(10), Output: llm.ReportedTokens(4),
		ReasoningChunks: llm.ReportedTokens(0), ReasoningBytes: llm.ReportedTokens(0),
	})
	nonzero := presenceAttempt("nonzero", llm.TokenUsage{ReasoningChunks: llm.ReportedTokens(731), ReasoningBytes: llm.ReportedTokens(8383)})
	if err = store.WriteAttempts(ctx, []usage.AttemptObservation{legacy, measured, nonzero}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []usage.AttemptObservation{legacy, measured, nonzero} {
		got, err := store.AccountingAttempt(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Tokens != want.Tokens {
			t.Fatalf("%s: got %+v want %+v", want.ID, got.Tokens, want.Tokens)
		}
	}
	// Confirmed-zero must be distinguishable from unknown after round-trip.
	got, _ := store.AccountingAttempt(ctx, "legacy")
	if got.Tokens.ReasoningChunks.Known {
		t.Fatal("legacy row invented presence evidence")
	}
	got, _ = store.AccountingAttempt(ctx, "measured")
	if !got.Tokens.ReasoningChunks.Known || got.Tokens.ReasoningChunks.Value != 0 {
		t.Fatal("confirmed-zero presence lost")
	}

	bad := presenceAttempt("bad", llm.TokenUsage{ReasoningBytes: llm.TokenCount{Known: true, Value: -1}})
	if err = store.WriteAttempts(ctx, []usage.AttemptObservation{bad}); err == nil {
		t.Fatal("negative presence accepted")
	}
}
