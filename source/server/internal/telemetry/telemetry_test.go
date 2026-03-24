package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	e := NewEvent("cercano_summarize", "qwen3-coder")
	if e.ToolName != "cercano_summarize" {
		t.Errorf("expected tool cercano_summarize, got %q", e.ToolName)
	}
	if e.Model != "qwen3-coder" {
		t.Errorf("expected model qwen3-coder, got %q", e.Model)
	}
	if e.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestEvent_Complete(t *testing.T) {
	e := NewEvent("cercano_extract", "gemma3:4b")
	time.Sleep(5 * time.Millisecond)
	e.Complete(100, 50, false, "", "")

	if e.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", e.InputTokens)
	}
	if e.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", e.OutputTokens)
	}
	if e.DurationMs < 5 {
		t.Errorf("expected duration >= 5ms, got %d", e.DurationMs)
	}
	if e.WasEscalated {
		t.Error("expected was_escalated=false")
	}
}

func TestEvent_Complete_Escalated(t *testing.T) {
	e := NewEvent("cercano_local", "qwen3-coder")
	e.Complete(200, 100, true, "anthropic", "claude-opus-4-6")

	if !e.WasEscalated {
		t.Error("expected was_escalated=true")
	}
	if e.CloudProvider != "anthropic" {
		t.Errorf("expected cloud provider anthropic, got %q", e.CloudProvider)
	}
	if e.CloudModel != "claude-opus-4-6" {
		t.Errorf("expected cloud model claude-opus-4-6, got %q", e.CloudModel)
	}
}

func TestCloudUsageReport(t *testing.T) {
	r := CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  15000,
		CloudOutputTokens: 3000,
		CloudProvider:     "anthropic",
		CloudModel:        "claude-opus-4-6",
	}
	if r.CloudInputTokens != 15000 {
		t.Errorf("expected 15000, got %d", r.CloudInputTokens)
	}
}

func TestSQLiteStore_RecordAndQuery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Record an event
	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(500, 100, false, "", "")
	if err := store.RecordEvent(context.Background(), e); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// Record another
	e2 := NewEvent("cercano_extract", "gemma3:4b")
	e2.Complete(200, 50, false, "", "")
	if err := store.RecordEvent(context.Background(), e2); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// Query totals
	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalRequests != 2 {
		t.Errorf("expected 2 total requests, got %d", stats.TotalRequests)
	}
	if stats.TotalInputTokens != 700 {
		t.Errorf("expected 700 input tokens, got %d", stats.TotalInputTokens)
	}
	if stats.TotalOutputTokens != 150 {
		t.Errorf("expected 150 output tokens, got %d", stats.TotalOutputTokens)
	}
	if stats.LocalTokensSaved != 850 {
		t.Errorf("expected 850 local tokens saved, got %d", stats.LocalTokensSaved)
	}
}

func TestSQLiteStore_RecordCloudUsage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	r := CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  15000,
		CloudOutputTokens: 3000,
		CloudProvider:     "anthropic",
		CloudModel:        "claude-opus-4-6",
	}
	if err := store.RecordCloudUsage(context.Background(), r); err != nil {
		t.Fatalf("RecordCloudUsage failed: %v", err)
	}

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalCloudInputTokens != 15000 {
		t.Errorf("expected 15000 cloud input tokens, got %d", stats.TotalCloudInputTokens)
	}
	if stats.TotalCloudOutputTokens != 3000 {
		t.Errorf("expected 3000 cloud output tokens, got %d", stats.TotalCloudOutputTokens)
	}
}

func TestSQLiteStore_StatsEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalRequests != 0 {
		t.Errorf("expected 0 requests, got %d", stats.TotalRequests)
	}
}

func TestSQLiteStore_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	dbPath := filepath.Join(dir, "telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected database file to be created")
	}
}

func TestSQLiteStore_StatsByTool(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 3; i++ {
		e := NewEvent("cercano_summarize", "qwen3-coder")
		e.Complete(100, 50, false, "", "")
		store.RecordEvent(context.Background(), e)
	}
	e := NewEvent("cercano_extract", "qwen3-coder")
	e.Complete(200, 80, false, "", "")
	store.RecordEvent(context.Background(), e)

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.ByTool) < 2 {
		t.Fatalf("expected at least 2 tools, got %d", len(stats.ByTool))
	}

	found := false
	for _, ts := range stats.ByTool {
		if ts.Name == "cercano_summarize" && ts.Count == 3 {
			found = true
		}
	}
	if !found {
		t.Error("expected cercano_summarize with count 3")
	}
}
