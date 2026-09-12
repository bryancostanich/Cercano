package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestChatQueryThinkingControlIsLocalAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, backend string
		disable, want bool
	}{
		{"local query", "llama_server", true, true},
		{"local default", "llama_server", false, false},
		{"cloud unchanged", "openai", true, false},
		{"DeepInfra unchanged", "deepinfra", true, false},
		{"other local unchanged", "ollama", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"1. go embed"},"finish_reason":"stop"}]}`))
			}))
			defer srv.Close()
			c := NewClient(Config{Backend: tc.backend, BaseURL: srv.URL + "/v1", Model: "model"})
			req := llm.ChatRequest{DisableThinking: tc.disable}
			req.Messages = []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "make a query"}}}}
			if _, err := c.Chat(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			raw, ok := body["chat_template_kwargs"]
			if ok != tc.want {
				t.Fatalf("chat_template_kwargs present=%v want=%v; body=%s", ok, tc.want, raw)
			}
			if tc.want {
				var kwargs map[string]any
				json.Unmarshal(raw, &kwargs)
				if kwargs["enable_thinking"] != false {
					t.Fatalf("kwargs=%v", kwargs)
				}
			}
		})
	}
}

func TestStreamingQueryThinkingDoesNotChangeDefaultRequest(t *testing.T) {
	c := NewClient(Config{Backend: "llama_server", Model: "model"})
	query := c.buildRequest(llm.ChatRequest{DisableThinking: true, MaxTokens: 4096}, true)
	if !query.Stream || query.ChatTemplateKwargs["enable_thinking"] != false || query.MaxTokens != 4096 {
		t.Fatalf("query=%+v", query)
	}
	normal := c.buildRequest(llm.ChatRequest{MaxTokens: 4096}, true)
	if normal.ChatTemplateKwargs != nil || normal.MaxTokens != 4096 {
		t.Fatalf("normal request changed: %+v", normal)
	}
}
