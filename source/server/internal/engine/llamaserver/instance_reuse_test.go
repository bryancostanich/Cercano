package llamaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/localruntime"
)

// TestChat_OllamaStyleNameReusesWarmInstance is the regression test for the
// runaway-spawn incident: the provider's resolveModel learned the Ollama
// ":latest" alias (phi4-mini:latest → phi4-mini-latest.gguf) but the engine's
// warm-instance lookup didn't, so every request missed the running instance
// and Manager.Start spawned another llama-server — 75 copies of a 2 GB model
// wired into GPU memory hung the machine. The engine must find the warm
// instance through the same alias the provider resolves.
func TestChat_OllamaStyleNameReusesWarmInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:          "llama_server:0123456789ab",
			DisplayName: "phi4-mini-latest",
			Runtime:     runtimeName,
			Path:        "/models/phi4-mini-latest.gguf",
		}},
		instances: []localruntime.InstanceRecord{{
			ID:       "inst",
			Runtime:  runtimeName,
			ModelID:  "llama_server:0123456789ab",
			State:    localruntime.StateRunning,
			Endpoint: server.URL,
		}},
	}
	eng := NewEngine(manager)

	for _, requested := range []string{
		"phi4-mini:latest",          // ollama tag form — the incident's config value
		"phi4-mini",                 // bare name
		"phi4-mini-latest",          // display name
		"llama_server:0123456789ab", // hash ID
		"phi4-mini-latest.gguf",     // filename
	} {
		if _, err := eng.Complete(context.Background(), requested, "hi", "", engine.GenOptions{}); err != nil {
			t.Fatalf("Complete(%q) returned error: %v", requested, err)
		}
	}
	if manager.startCount != 0 {
		t.Fatalf("engine started %d new llama-server(s) for a model that already had a running instance — every spawn wires the full model into RAM", manager.startCount)
	}
}
