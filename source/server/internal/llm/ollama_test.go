package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/agent"
)

func TestOllamaProvider_SetModelName(t *testing.T) {
	// Capture the model name sent in each request
	var receivedModel string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req["model"].(string)
		json.NewEncoder(w).Encode(map[string]string{"response": "ok"})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	provider := NewOllamaProvider("qwen3-coder", server.URL)

	// Verify initial model name
	if provider.Name() != "qwen3-coder" {
		t.Errorf("Expected name 'qwen3-coder', got '%s'", provider.Name())
	}

	// Process a request with the initial model
	provider.Process(context.Background(), &agent.Request{Input: "test"})
	if receivedModel != "qwen3-coder" {
		t.Errorf("Expected request model 'qwen3-coder', got '%s'", receivedModel)
	}

	// Change model at runtime
	provider.SetModelName("GLM-4.7-Flash")

	// Verify Name() reflects the change
	if provider.Name() != "GLM-4.7-Flash" {
		t.Errorf("Expected name 'GLM-4.7-Flash', got '%s'", provider.Name())
	}

	// Verify subsequent requests use the new model
	provider.Process(context.Background(), &agent.Request{Input: "test2"})
	if receivedModel != "GLM-4.7-Flash" {
		t.Errorf("Expected request model 'GLM-4.7-Flash', got '%s'", receivedModel)
	}
}

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

	req := &agent.Request{Input: "Hello"}
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
