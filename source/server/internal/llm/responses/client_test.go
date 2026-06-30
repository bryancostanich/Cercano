package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClientChat(t *testing.T) {
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("path = %q, want .../responses", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_1","status":"completed","output":[
			{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}
		],"usage":{"input_tokens":7,"output_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "gpt-5"})
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// request shape: store=false, include set, instructions carried.
	if gotBody.Store != false || len(gotBody.Include) != 1 || gotBody.Include[0] != "reasoning.encrypted_content" {
		t.Errorf("request store/include wrong: %+v", gotBody)
	}
	if gotBody.Instructions != "sys" || gotBody.Model != "gpt-5" {
		t.Errorf("request instructions/model wrong: %+v", gotBody)
	}
	// response mapping: reasoning + text blocks, usage.
	if len(resp.Blocks) != 2 || resp.Blocks[0].Type != llm.BlockReasoning || resp.Blocks[1].Text != "hello" {
		t.Fatalf("blocks = %+v", resp.Blocks)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 3 {
		t.Errorf("usage = %d/%d", resp.InputTokens, resp.OutputTokens)
	}
	if c.Name() != "openai-responses" || !c.Capabilities().SupportsVision || !c.Capabilities().SupportsTools {
		t.Errorf("name/caps wrong")
	}
}

func TestClientChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad model","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "x"})
	_, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("expected a readable API error, got %v", err)
	}
}
