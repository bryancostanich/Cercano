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
	if c.Name() != "openai-responses" || !c.Capabilities().SupportsTools {
		t.Errorf("name/caps wrong")
	}
	// SupportsVision now reflects the configured flag, not a hardcoded true.
	if v := NewClient(Config{Model: "gpt-5", SupportsVision: true}); !v.Capabilities().SupportsVision {
		t.Error("SupportsVision:true config should report vision")
	}
	if nv := NewClient(Config{Model: "gpt-5"}); nv.Capabilities().SupportsVision {
		t.Error("unset SupportsVision should report no vision")
	}
}

func TestClientChat_RetriesWithoutUnsupportedTemperature(t *testing.T) {
	// gpt-5-family reasoning models reject the temperature parameter
	// ("Unsupported parameter: temperature") — same class as Anthropic's
	// temperature-deprecated rejection. The adapter must retry once without
	// it and remember the model, so a long compaction run doesn't pay a 400
	// per segment.
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(b), `"temperature"`) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"Unsupported parameter: temperature","type":"invalid_request_error"}}`)
			return
		}
		io.WriteString(w, `{"id":"resp_1","status":"completed","output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}
		],"usage":{"input_tokens":7,"output_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "k", Model: "gpt-5"})
	zero := 0.0
	req := llm.ChatRequest{
		Model: "gpt-5.5", Temperature: &zero,
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	}

	resp, err := c.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("expected retry without temperature to succeed, got: %v", err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "ok" {
		t.Fatalf("unexpected response: %+v", resp.Blocks)
	}
	if len(bodies) != 2 || strings.Contains(bodies[1], `"temperature"`) {
		t.Fatalf("expected rejected+temperature-free retry, got %d bodies: %v", len(bodies), bodies)
	}

	// The model is remembered: next call goes straight to temperature-free.
	bodies = nil
	if _, err := c.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || strings.Contains(bodies[0], `"temperature"`) {
		t.Fatalf("expected one temperature-free request, got %d: %v", len(bodies), bodies)
	}

	// An unrelated 400 surfaces unchanged.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"Invalid value for max_output_tokens","type":"invalid_request_error"}}`)
	}))
	defer srv2.Close()
	c2 := NewClient(Config{BaseURL: srv2.URL, APIKey: "k", Model: "gpt-5"})
	if _, err := c2.Chat(context.Background(), req); err == nil || !strings.Contains(err.Error(), "max_output_tokens") {
		t.Fatalf("unrelated 400 must surface unchanged, got: %v", err)
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
