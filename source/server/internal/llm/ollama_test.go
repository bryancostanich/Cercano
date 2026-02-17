package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/router"
)

func TestOllamaProvider_Process(t *testing.T) {
	// Mock Ollama Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/api/generate" {
			t.Errorf("Expected path /api/generate, got %s", r.URL.Path)
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		if req["model"] != "test-model" {
			t.Errorf("Expected model 'test-model', got %v", req["model"])
		}

		resp := map[string]string{"response": "Test Response"}
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	// Test Subject
	provider := NewOllamaProvider("test-model", server.URL)

	req := &router.Request{Input: "Hello"}
	resp, err := provider.Process(context.Background(), req)

	if err != nil {
		t.Fatalf("Process failed: %v", err) // Currently returns nil, nil so this might pass?
	}
	
	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.Output != "Test Response" {
		t.Errorf("Expected 'Test Response', got '%s'", resp.Output)
	}
}
