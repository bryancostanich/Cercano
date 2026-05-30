package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/engine"
)

func TestChatWithTools_PlainTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("got path %s, want /api/chat", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"messages"`) {
			t.Errorf("request body missing messages: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi there"},"prompt_eval_count":10,"eval_count":5,"done":true}`))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	resp, err := e.ChatWithTools(context.Background(), engine.ChatRequest{
		Model:    "qwen3-coder",
		Messages: []engine.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi there" {
		t.Errorf("Content = %q, want %q", resp.Content, "hi there")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.InputTokens != 10 || resp.OutputTokens != 5 {
		t.Errorf("token counts = %d/%d, want 10/5", resp.InputTokens, resp.OutputTokens)
	}
}

func TestChatWithTools_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [
					{"function": {"name": "read_file", "arguments": {"path":"/x"}}}
				]
			},
			"done": true
		}`))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	resp, err := e.ChatWithTools(context.Background(), engine.ChatRequest{
		Model:    "qwen3-coder",
		Messages: []engine.ChatMessage{{Role: "user", Content: "read /x"}},
		Tools:    []engine.ToolSchemaJSON{{Type: "function", Function: engine.ToolFunctionJSON{Name: "read_file"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("tool name = %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("expected synthetic ID when Ollama omits it")
	}
	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Function.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != "/x" {
		t.Errorf("args = %v, want path=/x", args)
	}
}

func TestChatWithTools_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	e := NewOllamaEngine(srv.URL)
	_, err := e.ChatWithTools(context.Background(), engine.ChatRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestChatWithTools_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := NewOllamaEngine(srv.URL)
	_, err := e.ChatWithTools(ctx, engine.ChatRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
