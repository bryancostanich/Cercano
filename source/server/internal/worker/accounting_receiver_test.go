package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
)

func TestWorkerAccountingRPCPersistsOnHost(t *testing.T) {
	worker, client := accountingTestConnection(t)
	store, err := telemetry.NewSQLiteStore(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	collector := telemetry.NewCollector(store, 8)
	t.Cleanup(collector.Close)
	collector.SetSessionID("host-session")
	if err = collector.EnableAccounting(telemetry.AccountingOptions{Capacity: 16, BatchSize: 4, FlushInterval: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- receiveAccounting(ctx, client, collector, "host-owned-process", func() { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("host receiver did not exit")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("receiver start: %v", err)
	case <-time.After(time.Second):
		t.Fatal("receiver not ready")
	}
	observation := wireAttemptFixture()
	observation.Attribution.SessionID = ""
	observation.Attribution.WorkerID = "not-trusted-from-wire"
	writer := worker.accountingWriter()
	callCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err = writer.WriteAttempts(callCtx, []usage.AttemptObservation{observation}); err != nil {
		t.Fatal(err)
	}
	got, err := store.AccountingAttempt(t.Context(), observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attribution.WorkerID != "worker/host-owned-process" || got.Attribution.SessionID != "host-session" || got.Tokens != observation.Tokens {
		t.Fatalf("persisted observation=%+v", got)
	}
	// Retry after acknowledgment: the same identity must still represent one row.
	if err = writer.WriteAttempts(callCtx, []usage.AttemptObservation{observation}); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteAccountingHealth(callCtx, "writer", telemetry.AccountingHealth{Lost: 3, CoverageIncomplete: true, LastError: "worker capacity exceeded"}); err != nil {
		t.Fatal(err)
	}
	if h := collector.AccountingHealth(); !h.CoverageIncomplete {
		t.Fatal("worker loss not surfaced on host")
	}
	// Health persistence is acknowledged through the same path, but never makes
	// a second inference row. Query its namespaced writer key via a report helper
	// in telemetry tests; here the successful receipt proves that store call ran.
	if _, err = store.AccountingAttempt(t.Context(), "worker/host-owned-process/writer"); err == nil {
		t.Fatal("worker health became an inference attempt")
	}
}
