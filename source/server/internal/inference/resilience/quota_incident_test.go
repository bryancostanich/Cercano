package resilience

// Regression for the 2026-07-16 quota incident: a credits-exhausted Claude
// subscription answered 429 with a quota-scale Retry-After, the Anthropic
// SDK's built-in retry honored it VERBATIM and slept inside the request, and
// the failover composite — which only reacts to errors — never saw anything.
// Turns hung 8–11 minutes with no narration until the user flipped profiles
// by hand.
//
// This test runs the REAL anthropic adapter (SDK included) against a stub API
// returning that exact wire shape, wrapped in the resilience engine with a
// backup. It pins the fixed behavior end to end:
//
//   - the quota 429 surfaces immediately (no hidden SDK sleep, one request),
//   - the engine narrates ("anthropic quota reached — switching to …"),
//   - the backup serves the turn,
//   - all within a wall-clock bound that a single honored Retry-After would
//     blow by two orders of magnitude.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	anthllm "cercano/source/server/internal/llm/anthropic"

	"cercano/source/server/internal/llm"
)

func TestQuotaIncident_ImmediateNarratedFailover(t *testing.T) {
	var primaryHits atomic.Int32
	quotaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.Header().Set("Retry-After", "3600") // quota resets in an hour
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"You have exceeded your usage limit."}}`))
	}))
	defer quotaSrv.Close()

	primary := anthllm.NewClient(anthllm.Config{APIKey: "k", BaseURL: quotaSrv.URL})
	backup := &fakeProvider{name: "openai"}

	var events []Event
	engine := New(primary, Options{
		Backup:         backup,
		BackupModelFor: func(string) string { return "gpt-5.5" },
		OnEvent:        func(e Event) { events = append(events, e) },
	})

	start := time.Now()
	rdr, err := engine.StreamChat(context.Background(), inference.Call{
		Model:    "claude-opus-4-8",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, err := collectStream(t, rdr)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}

	// The turn completed on the backup, narrated, without a hidden sleep.
	if elapsed > 3*time.Second {
		t.Fatalf("turn took %v — a Retry-After sleep leaked back in", elapsed)
	}
	if got := primaryHits.Load(); got != 1 {
		t.Errorf("primary requests = %d, want exactly 1 (SDK retries must stay off)", got)
	}
	if len(evs) < 2 || evs[0].Type != llm.EventNotice {
		t.Fatalf("events = %+v, want notice then backup content", evs)
	}
	if evs[0].Notice != "anthropic quota reached — switching to openai" {
		t.Errorf("notice = %q", evs[0].Notice)
	}
	if evs[1].TextDelta != "from openai" {
		t.Errorf("content = %+v, want the backup's text", evs[1])
	}
	if len(events) != 1 || events[0].Action != ActionFailover || events[0].Class != llm.ErrQuota {
		t.Errorf("engine events = %+v, want one quota failover", events)
	}
	if backup.models[0] != "gpt-5.5" {
		t.Errorf("backup saw model %q, want the rewritten gpt-5.5", backup.models[0])
	}
}
