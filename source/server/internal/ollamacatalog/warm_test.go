package ollamacatalog

import (
	"context"
	"testing"
	"time"
)

func TestEnsureTag(t *testing.T) {
	if got := EnsureTag("qwen2.5-coder"); got != "qwen2.5-coder:latest" {
		t.Errorf("bare name = %q", got)
	}
	if got := EnsureTag("qwen2.5-coder:7b"); got != "qwen2.5-coder:7b" {
		t.Errorf("tagged name = %q", got)
	}
}

func TestWarmMissingEstimates_ResolvesCatalog(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	m.SetWarmIntervals(time.Nanosecond, time.Hour)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	stop := make(chan struct{})
	warmed := m.WarmMissingEstimates(context.Background(), stop)
	if warmed != 1 {
		t.Fatalf("warmed = %d, want 1 (the library page lists testmodel)", warmed)
	}
	if _, ok := m.CachedEstimate("testmodel"); !ok {
		t.Fatal("estimate not cached after warm")
	}
	// Second pass: everything cached — no new work, no new fetches.
	blobsBefore := e.blobGets.Load()
	if warmed := m.WarmMissingEstimates(context.Background(), stop); warmed != 0 {
		t.Errorf("second pass warmed = %d, want 0", warmed)
	}
	if e.blobGets.Load() != blobsBefore {
		t.Errorf("second pass fetched blobs (%d -> %d)", blobsBefore, e.blobGets.Load())
	}
}

func TestWarmMissingEstimates_FailureBacksOff(t *testing.T) {
	e := newEstimateTestServer(t)
	// Point the manifest handler at a digest whose blob 404s by
	// simply shutting the blob route: easiest is to serve a manifest
	// with a bogus digest — but the test server always serves its
	// blob. Instead, break the manifest fetch by closing the server
	// after refresh caches the library page.
	m := newEstimateManager(t, e)
	m.SetWarmIntervals(time.Nanosecond, time.Hour)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	e.srv.Close() // all subsequent fetches fail
	stop := make(chan struct{})
	if warmed := m.WarmMissingEstimates(context.Background(), stop); warmed != 0 {
		t.Fatalf("warmed = %d against a dead server", warmed)
	}
	if m.shouldAttemptWarm(EnsureTag("testmodel")) {
		t.Fatal("failed ref should be inside the backoff window")
	}
	// A fresh pass must skip it entirely (no panic on dead server, no
	// attempt bookkeeping change).
	if warmed := m.WarmMissingEstimates(context.Background(), stop); warmed != 0 {
		t.Fatalf("backoff pass warmed = %d", warmed)
	}
}

func TestWarmMissingEstimates_StopInterrupts(t *testing.T) {
	e := newEstimateTestServer(t)
	m := newEstimateManager(t, e)
	m.SetWarmIntervals(time.Nanosecond, time.Hour)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	stop := make(chan struct{})
	close(stop)
	if warmed := m.WarmMissingEstimates(context.Background(), stop); warmed != 0 {
		t.Fatalf("closed stop channel should interrupt before any work, warmed = %d", warmed)
	}
}
