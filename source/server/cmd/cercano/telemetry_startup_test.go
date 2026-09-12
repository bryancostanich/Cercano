package main

import (
	"context"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/telemetry"
)

func TestAgentTelemetryLivesUntilShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	collector, shutdown, err := startAgentTelemetry(path, "test-session")
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	collector.Emit(telemetry.NewEvent("after-startup", "fake"))
	shutdown()
	shutdown() // ownership cleanup is idempotent
	store, err := telemetry.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 1 {
		t.Fatalf("post-startup usage persisted=%d; want 1", stats.TotalRequests)
	}
}

func TestAgentTelemetryOpenFailure(t *testing.T) {
	collector, shutdown, err := startAgentTelemetry(t.TempDir(), "test-session")
	if err == nil || collector != nil || shutdown != nil {
		t.Fatalf("expected failed open without live collector: %v", err)
	}
}
