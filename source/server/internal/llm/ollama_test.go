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

func TestOllamaProvider_SetBaseURL(t *testing.T) {
	// Track which server receives the request
	server1Called := false
	server2Called := false

	handler1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server1Called = true
		json.NewEncoder(w).Encode(map[string]string{"response": "from-server1"})
	})
	handler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server2Called = true
		json.NewEncoder(w).Encode(map[string]string{"response": "from-server2"})
	})
	server1 := httptest.NewServer(handler1)
	defer server1.Close()
	server2 := httptest.NewServer(handler2)
	defer server2.Close()

	provider := NewOllamaProvider("test-model", server1.URL)

	// Verify initial BaseURL via GetBaseURL
	if provider.GetBaseURL() != server1.URL {
		t.Errorf("Expected initial BaseURL %q, got %q", server1.URL, provider.GetBaseURL())
	}

	// Process a request — should hit server1
	provider.Process(context.Background(), &agent.Request{Input: "test"})
	if !server1Called {
		t.Error("Expected server1 to be called with initial BaseURL")
	}

	// Switch BaseURL at runtime
	provider.SetBaseURL(server2.URL)

	// Verify GetBaseURL reflects the change
	if provider.GetBaseURL() != server2.URL {
		t.Errorf("Expected BaseURL %q after SetBaseURL, got %q", server2.URL, provider.GetBaseURL())
	}

	// Process another request — should hit server2
	provider.Process(context.Background(), &agent.Request{Input: "test2"})
	if !server2Called {
		t.Error("Expected server2 to be called after SetBaseURL")
	}
}

func TestOllamaProvider_SetBaseURL_AffectsStreaming(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"response": "stream-from-1", "done": true})
	}))
	defer server1.Close()
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"response": "stream-from-2", "done": true})
	}))
	defer server2.Close()

	provider := NewOllamaProvider("test-model", server1.URL)

	// Stream from server1
	resp1, err := provider.ProcessStream(context.Background(), &agent.Request{Input: "test"}, nil)
	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}
	if resp1.Output != "stream-from-1" {
		t.Errorf("Expected 'stream-from-1', got %q", resp1.Output)
	}

	// Switch and stream from server2
	provider.SetBaseURL(server2.URL)
	resp2, err := provider.ProcessStream(context.Background(), &agent.Request{Input: "test"}, nil)
	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}
	if resp2.Output != "stream-from-2" {
		t.Errorf("Expected 'stream-from-2', got %q", resp2.Output)
	}
}

func TestOllamaProvider_SetBaseURL_ConcurrentAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "ok"})
	}))
	defer server.Close()

	provider := NewOllamaProvider("test-model", server.URL)

	// Hammer SetBaseURL and Process concurrently to detect races
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			provider.SetBaseURL(server.URL)
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		provider.Process(context.Background(), &agent.Request{Input: "test"})
	}
	<-done

	// If we get here without a race detector panic, the test passes
}

func TestOllamaProvider_Fallback_LocalOnly(t *testing.T) {
	// When no remote is configured, there's no fallback — just the local endpoint.
	provider := NewOllamaProvider("test-model", "http://localhost:11434")

	if provider.GetActiveURL() != "http://localhost:11434" {
		t.Errorf("Expected activeURL 'http://localhost:11434', got %q", provider.GetActiveURL())
	}
	if provider.IsUsingFallback() {
		t.Error("Expected IsUsingFallback=false when no remote configured")
	}
}

func TestOllamaProvider_Fallback_SetRemote(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "from-remote"})
	}))
	defer server1.Close()

	provider := NewOllamaProvider("test-model", "http://localhost:11434")

	// Set a remote URL — primary becomes remote, fallback becomes localhost
	provider.SetBaseURL(server1.URL)

	if provider.GetActiveURL() != server1.URL {
		t.Errorf("Expected activeURL %q, got %q", server1.URL, provider.GetActiveURL())
	}
	if provider.IsUsingFallback() {
		t.Error("Expected IsUsingFallback=false right after setting remote")
	}

	// GetBaseURL should still return the primary (remote) URL
	if provider.GetBaseURL() != server1.URL {
		t.Errorf("Expected BaseURL %q, got %q", server1.URL, provider.GetBaseURL())
	}
}

func TestOllamaProvider_Fallback_SwitchToFallback(t *testing.T) {
	provider := NewOllamaProvider("test-model", "http://localhost:11434")
	provider.SetBaseURL("http://remote:11434")

	// Simulate switching to fallback
	provider.SwitchToFallback()

	if provider.GetActiveURL() != "http://localhost:11434" {
		t.Errorf("Expected activeURL to be fallback 'http://localhost:11434', got %q", provider.GetActiveURL())
	}
	if !provider.IsUsingFallback() {
		t.Error("Expected IsUsingFallback=true after SwitchToFallback")
	}
}

func TestOllamaProvider_Fallback_SwitchToPrimary(t *testing.T) {
	provider := NewOllamaProvider("test-model", "http://localhost:11434")
	provider.SetBaseURL("http://remote:11434")
	provider.SwitchToFallback()

	// Recover — switch back to primary
	provider.SwitchToPrimary()

	if provider.GetActiveURL() != "http://remote:11434" {
		t.Errorf("Expected activeURL 'http://remote:11434', got %q", provider.GetActiveURL())
	}
	if provider.IsUsingFallback() {
		t.Error("Expected IsUsingFallback=false after SwitchToPrimary")
	}
}

func TestOllamaProvider_Fallback_ProcessUsesActiveURL(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "from-remote"})
	}))
	defer remoteServer.Close()
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "from-local"})
	}))
	defer localServer.Close()

	// Start with local, then set remote as primary
	provider := NewOllamaProvider("test-model", localServer.URL)
	provider.SetBaseURL(remoteServer.URL)

	// Should hit remote
	resp, _ := provider.Process(context.Background(), &agent.Request{Input: "test"})
	if resp.Output != "from-remote" {
		t.Errorf("Expected 'from-remote', got %q", resp.Output)
	}

	// Switch to fallback (local)
	provider.SwitchToFallback()
	resp, _ = provider.Process(context.Background(), &agent.Request{Input: "test"})
	if resp.Output != "from-local" {
		t.Errorf("Expected 'from-local', got %q", resp.Output)
	}

	// Recover to primary (remote)
	provider.SwitchToPrimary()
	resp, _ = provider.Process(context.Background(), &agent.Request{Input: "test"})
	if resp.Output != "from-remote" {
		t.Errorf("Expected 'from-remote' after recovery, got %q", resp.Output)
	}
}

func TestOllamaProvider_ListModels(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/tags" {
			t.Errorf("Expected path /api/tags, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]interface{}{
				{
					"name":        "qwen3-coder:latest",
					"size":        4700000000,
					"modified_at": "2026-03-15T10:30:00Z",
				},
				{
					"name":        "nomic-embed-text:latest",
					"size":        274000000,
					"modified_at": "2026-03-10T08:00:00Z",
				},
			},
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	provider := NewOllamaProvider("test-model", server.URL)

	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("Expected 2 models, got %d", len(models))
	}

	if models[0].Name != "qwen3-coder:latest" {
		t.Errorf("Expected first model 'qwen3-coder:latest', got %q", models[0].Name)
	}
	if models[0].Size != 4700000000 {
		t.Errorf("Expected size 4700000000, got %d", models[0].Size)
	}
	if models[1].Name != "nomic-embed-text:latest" {
		t.Errorf("Expected second model 'nomic-embed-text:latest', got %q", models[1].Name)
	}
}

func TestOllamaProvider_ListModels_Error(t *testing.T) {
	// Server that returns an error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	provider := NewOllamaProvider("test-model", server.URL)

	_, err := provider.ListModels(context.Background())
	if err == nil {
		t.Error("Expected error for 500 response, got nil")
	}
}

func TestOllamaProvider_ListModels_Unreachable(t *testing.T) {
	provider := NewOllamaProvider("test-model", "http://localhost:1")

	_, err := provider.ListModels(context.Background())
	if err == nil {
		t.Error("Expected error for unreachable server, got nil")
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

func TestOllamaProvider_ImplementsStreamingModelProvider(t *testing.T) {
	var _ agent.StreamingModelProvider = &OllamaProvider{}
}

func TestOllamaProvider_ProcessStream(t *testing.T) {
	// Mock Ollama server returning newline-delimited JSON chunks
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		// Verify stream:true was requested
		if req["stream"] != true {
			t.Errorf("Expected stream:true, got %v", req["stream"])
		}

		// Write chunked responses (Ollama format: newline-delimited JSON)
		chunks := []map[string]interface{}{
			{"response": "Hello", "done": false},
			{"response": " ", "done": false},
			{"response": "world", "done": false},
			{"response": "!", "done": true},
		}

		for _, chunk := range chunks {
			json.NewEncoder(w).Encode(chunk)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	provider := NewOllamaProvider("test-model", server.URL)

	var tokens []string
	resp, err := provider.ProcessStream(context.Background(), &agent.Request{Input: "test"}, func(token string) {
		tokens = append(tokens, token)
	})

	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}

	// Verify tokens arrived in order
	expected := []string{"Hello", " ", "world", "!"}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d: %v", len(expected), len(tokens), tokens)
	}
	for i, tok := range expected {
		if tokens[i] != tok {
			t.Errorf("token %d: expected %q, got %q", i, tok, tokens[i])
		}
	}

	// Verify accumulated output
	if resp.Output != "Hello world!" {
		t.Errorf("expected accumulated output %q, got %q", "Hello world!", resp.Output)
	}
}

func TestOllamaProvider_ProcessStream_NilCallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunks := []map[string]interface{}{
			{"response": "Hello", "done": false},
			{"response": " world", "done": true},
		}
		for _, chunk := range chunks {
			json.NewEncoder(w).Encode(chunk)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	provider := NewOllamaProvider("test-model", server.URL)

	// nil onToken should not panic
	resp, err := provider.ProcessStream(context.Background(), &agent.Request{Input: "test"}, nil)

	if err != nil {
		t.Fatalf("ProcessStream with nil callback failed: %v", err)
	}
	if resp.Output != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", resp.Output)
	}
}
