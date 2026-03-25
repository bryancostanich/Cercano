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

func TestCollector_EmitAndDrain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	collector := NewCollector(store, 100)

	for i := 0; i < 5; i++ {
		e := NewEvent("cercano_summarize", "qwen3-coder")
		e.Complete(100, 50, false, "", "")
		collector.Emit(e)
	}

	collector.EmitCloudUsage(CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  10000,
		CloudOutputTokens: 2000,
		CloudProvider:     "anthropic",
		CloudModel:        "claude-opus-4-6",
	})

	collector.Close()

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.TotalRequests != 5 {
		t.Errorf("expected 5 requests, got %d", stats.TotalRequests)
	}
	if stats.TotalCloudInputTokens != 10000 {
		t.Errorf("expected 10000 cloud input tokens, got %d", stats.TotalCloudInputTokens)
	}
}

func TestCollector_NonBlocking(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Buffer of 1 — second emit should be dropped, not block
	collector := NewCollector(store, 1)

	// Fill the buffer
	e1 := NewEvent("cercano_summarize", "qwen3-coder")
	e1.Complete(100, 50, false, "", "")
	collector.Emit(e1)

	// This should not block even if buffer is full
	done := make(chan struct{})
	go func() {
		e2 := NewEvent("cercano_extract", "qwen3-coder")
		e2.Complete(200, 80, false, "", "")
		collector.Emit(e2)
		close(done)
	}()

	select {
	case <-done:
		// Good — didn't block
	case <-time.After(1 * time.Second):
		t.Fatal("Emit blocked when buffer was full")
	}

	collector.Close()
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

func TestStats_ComputeSavings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// 800 local tokens
	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(500, 300, false, "", "")
	store.RecordEvent(context.Background(), e)

	// 200 cloud tokens
	store.RecordCloudUsage(context.Background(), CloudUsageReport{
		Timestamp:         time.Now(),
		CloudInputTokens:  150,
		CloudOutputTokens: 50,
		CloudProvider:     "anthropic",
		CloudModel:        "claude-opus-4-6",
	})

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	// 800 local / (800 local + 200 cloud) = 80%
	if stats.LocalPercentage < 79.9 || stats.LocalPercentage > 80.1 {
		t.Errorf("expected ~80%% local, got %.1f%%", stats.LocalPercentage)
	}
}

func TestSQLiteStore_StatsByModel(t *testing.T) {
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
	for i := 0; i < 2; i++ {
		e := NewEvent("cercano_summarize", "gemma3:4b")
		e.Complete(80, 40, false, "", "")
		store.RecordEvent(context.Background(), e)
	}

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.ByModel) < 2 {
		t.Fatalf("expected at least 2 models, got %d", len(stats.ByModel))
	}

	found := false
	for _, ms := range stats.ByModel {
		if ms.Name == "qwen3-coder" && ms.Count == 3 {
			found = true
		}
	}
	if !found {
		t.Error("expected qwen3-coder with count 3")
	}
}

func TestSQLiteStore_RecordSession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-abc"
	if err := store.RecordSession(context.Background(), sessionID); err != nil {
		t.Fatalf("RecordSession failed: %v", err)
	}

	// Record events tagged with session
	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.SessionID = sessionID
	e.Complete(500, 100, false, "", "")
	if err := store.RecordEvent(context.Background(), e); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	e2 := NewEvent("cercano_extract", "qwen3-coder")
	e2.SessionID = sessionID
	e2.Complete(200, 50, false, "", "")
	if err := store.RecordEvent(context.Background(), e2); err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.BySession) != 1 {
		t.Fatalf("expected 1 session, got %d", len(stats.BySession))
	}
	if stats.BySession[0].Count != 2 {
		t.Errorf("expected 2 events in session, got %d", stats.BySession[0].Count)
	}
	if stats.BySession[0].InputTokens+stats.BySession[0].OutputTokens != 850 {
		t.Errorf("expected 850 total tokens, got %d", stats.BySession[0].InputTokens+stats.BySession[0].OutputTokens)
	}
}

func TestSQLiteStore_MultipleSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	store.RecordSession(context.Background(), "session-1")
	store.RecordSession(context.Background(), "session-2")

	// 3 events in session-1
	for i := 0; i < 3; i++ {
		e := NewEvent("cercano_summarize", "qwen3-coder")
		e.SessionID = "session-1"
		e.Complete(100, 50, false, "", "")
		store.RecordEvent(context.Background(), e)
	}

	// 1 event in session-2
	e := NewEvent("cercano_research", "qwen3-coder")
	e.SessionID = "session-2"
	e.Complete(500, 200, false, "", "")
	store.RecordEvent(context.Background(), e)

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.BySession) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(stats.BySession))
	}
	// Should be ordered by most recent first (session-2 was created after session-1)
}

func TestSQLiteStore_EventsWithoutSession(t *testing.T) {
	// Pre-existing events without session_id should still work
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Event with no session (legacy)
	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(100, 50, false, "", "")
	store.RecordEvent(context.Background(), e)

	// Event with session
	store.RecordSession(context.Background(), "session-1")
	e2 := NewEvent("cercano_extract", "qwen3-coder")
	e2.SessionID = "session-1"
	e2.Complete(200, 80, false, "", "")
	store.RecordEvent(context.Background(), e2)

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	// Total should include both
	if stats.TotalRequests != 2 {
		t.Errorf("expected 2 total requests, got %d", stats.TotalRequests)
	}
	// BySession should only show session-1 (legacy events excluded)
	if len(stats.BySession) != 1 {
		t.Fatalf("expected 1 session in BySession, got %d", len(stats.BySession))
	}
}

func TestCollector_SessionID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	collector := NewCollector(store, 100)
	collector.SetSessionID("collector-session-1")

	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(100, 50, false, "", "")
	collector.Emit(e)

	collector.Close()

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.BySession) != 1 {
		t.Fatalf("expected 1 session, got %d", len(stats.BySession))
	}
}

func TestSQLiteStore_StatsByDay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_telemetry.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	e := NewEvent("cercano_summarize", "qwen3-coder")
	e.Complete(100, 50, false, "", "")
	store.RecordEvent(context.Background(), e)

	stats, err := store.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if len(stats.ByDay) == 0 {
		t.Fatal("expected at least 1 day in ByDay")
	}
	if stats.ByDay[0].Count != 1 {
		t.Errorf("expected 1 event today, got %d", stats.ByDay[0].Count)
	}
}
