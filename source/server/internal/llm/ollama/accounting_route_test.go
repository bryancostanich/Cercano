package ollama

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

func TestAccountingPreservesSelectedDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"model":"served","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":7}` + "\n"))
	}))
	defer server.Close()
	var mu sync.Mutex
	var last usage.AttemptObservation
	ctx := usage.WithAttempts(t.Context(), func(a usage.AttemptObservation) bool { mu.Lock(); last = a; mu.Unlock(); return true }, usage.Attribution{})
	ctx = usage.WithAttemptProfile(ctx, "selected-profile", "primary")
	client := NewClient(Config{BaseURL: server.URL, Model: "requested"})
	reader, err := client.StreamChat(ctx, llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for {
		_, ok, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if last.Profile != "selected-profile" || last.Destination != "primary" || last.Model != "served" {
		t.Fatalf("selected route overwritten: %+v", last)
	}
}
