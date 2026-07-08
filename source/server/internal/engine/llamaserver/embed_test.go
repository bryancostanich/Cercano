package llamaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/localruntime"
)

// TestEmbed_CallsEmbeddingsEndpoint drives Embed end to end: resolve the
// warm instance, POST /v1/embeddings, decode the vector.
func TestEmbed_CallsEmbeddingsEndpoint(t *testing.T) {
	var sawPath string
	var sawPayload embeddingsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&sawPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"embedding": [0.25, -0.5, 1.0]}]}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		models: []localruntime.ModelRecord{{
			ID:            "llama_server:nomic",
			DisplayName:   "nomic-embed-text-latest",
			Runtime:       runtimeName,
			Path:          "/models/nomic-embed-text-latest.gguf",
			SupportsEmbed: true,
		}},
		instances: []localruntime.InstanceRecord{{
			ID:       "inst-embed",
			Runtime:  runtimeName,
			ModelID:  "llama_server:nomic",
			State:    localruntime.StateRunning,
			Endpoint: server.URL,
		}},
	}
	eng := NewEngine(manager)

	vec, err := eng.Embed(context.Background(), "llama_server:nomic", "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if sawPath != "/v1/embeddings" {
		t.Errorf("path = %q, want /v1/embeddings", sawPath)
	}
	if sawPayload.Input != "hello world" {
		t.Errorf("input = %q", sawPayload.Input)
	}
	if len(vec) != 3 || vec[0] != 0.25 || vec[1] != -0.5 || vec[2] != 1.0 {
		t.Errorf("vector = %v", vec)
	}
	if manager.startCount != 0 {
		t.Errorf("startCount = %d — a running instance must be reused", manager.startCount)
	}
}

// TestEmbed_EmptyDataErrors: a well-formed but empty response is an error,
// not a nil vector.
func TestEmbed_EmptyDataErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()

	manager := &fakeRuntimeManager{
		instances: []localruntime.InstanceRecord{{
			ID:       "inst-embed",
			Runtime:  runtimeName,
			ModelID:  "m",
			State:    localruntime.StateRunning,
			Endpoint: server.URL,
		}},
	}
	eng := NewEngine(manager)
	if _, err := eng.Embed(context.Background(), "m", "text"); err == nil {
		t.Fatal("want error for empty embeddings data")
	}
}
