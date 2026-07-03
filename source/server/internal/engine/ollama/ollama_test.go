package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cercano/source/server/internal/engine"
)

func TestOllamaEngine_SetBaseURL(t *testing.T) {
	eng := NewOllamaEngine("http://initial")
	if eng.GetActiveURL() != "http://initial" {
		t.Fatalf("expected initial URL, got %s", eng.GetActiveURL())
	}

	eng.SetBaseURL("http://updated")
	if eng.GetActiveURL() != "http://updated" {
		t.Fatalf("expected updated URL, got %s", eng.GetActiveURL())
	}
}

func TestOllamaEngine_FallbackLogic(t *testing.T) {
	eng := NewOllamaEngine("http://primary")
	eng.fallbackURL = "http://fallback"

	if eng.IsUsingFallback() {
		t.Fatal("expected usingFallback to be false initially")
	}

	eng.SwitchToFallback()
	if !eng.IsUsingFallback() {
		t.Fatal("expected usingFallback to be true")
	}
	if eng.GetActiveURL() != "http://fallback" {
		t.Fatalf("expected active URL to be fallback, got %s", eng.GetActiveURL())
	}

	eng.SwitchToPrimary()
	if eng.IsUsingFallback() {
		t.Fatal("expected usingFallback to be false after switching back")
	}
	if eng.GetActiveURL() != "http://primary" {
		t.Fatalf("expected active URL to be primary, got %s", eng.GetActiveURL())
	}
}

func TestOllamaEngine_HealthMonitor(t *testing.T) {
	var primaryRequests, fallbackRequests int

	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primarySrv.Close()

	fallbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer fallbackSrv.Close()

	eng := NewOllamaEngine(primarySrv.URL)
	eng.fallbackURL = fallbackSrv.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng.StartHealthMonitor(ctx, 10*time.Millisecond, 2)

	// Wait for health monitor to fail twice and switch
	time.Sleep(100 * time.Millisecond)

	if !eng.IsUsingFallback() {
		t.Fatal("expected engine to switch to fallback after failures")
	}

	// Now make primary healthy
	primarySrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wait for health monitor to recover
	time.Sleep(100 * time.Millisecond)

	if eng.IsUsingFallback() {
		t.Fatal("expected engine to switch back to primary after recovery")
	}
}

func TestOllamaEngine_ListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tagsResponse{
			Models: []engine.ModelInfo{
				{Name: "model1", Size: 100},
				{Name: "model2", Size: 200},
			},
		})
	}))
	defer srv.Close()

	eng := NewOllamaEngine(srv.URL)
	models, err := eng.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "model1" {
		t.Fatalf("expected model1, got %s", models[0].Name)
	}
}

func TestOllamaEngine_CompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")

		responses := []generateResponse{
			{Response: "Hello", Done: false},
			{Response: " ", Done: false},
			{Response: "World", Done: true, PromptEvalCount: 50, EvalCount: 20},
		}

		encoder := json.NewEncoder(w)
		for _, resp := range responses {
			encoder.Encode(resp)
		}
	}))
	defer srv.Close()

	eng := NewOllamaEngine(srv.URL)

	var streamed []string
	result, err := eng.CompleteStream(context.Background(), "test-model", "prompt", "", engine.GenOptions{}, func(token string) {
		streamed = append(streamed, token)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output != "Hello World" {
		t.Fatalf("expected result 'Hello World', got %q", result.Output)
	}

	if len(streamed) != 3 {
		t.Fatalf("expected 3 streamed tokens, got %d", len(streamed))
	}

	if result.InputTokens != 50 {
		t.Errorf("expected 50 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 20 {
		t.Errorf("expected 20 output tokens, got %d", result.OutputTokens)
	}
}

func TestOllamaEngine_TemperatureOption(t *testing.T) {
	var sawOptions map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Options map[string]interface{} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawOptions = payload.Options
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(generateResponse{Response: "ok", Done: true})
	}))
	defer srv.Close()

	eng := NewOllamaEngine(srv.URL)

	if _, err := eng.Complete(context.Background(), "m", "p", "", engine.Greedy()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	temp, ok := sawOptions["temperature"]
	if !ok {
		t.Fatal("greedy request must carry temperature in options")
	}
	if temp != 0.0 {
		t.Fatalf("greedy temperature = %v, want 0", temp)
	}

	if _, err := eng.Complete(context.Background(), "m", "p", "", engine.GenOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := sawOptions["temperature"]; ok {
		t.Fatal("default request must not override the server's temperature")
	}
}
