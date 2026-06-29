package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClientChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x"})
	resp, err := c.Chat(context.Background(), llm.ChatRequest{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hello" || resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if c.Name() != "openai" || !c.Capabilities().SupportsTools || !c.Capabilities().SupportsVision {
		t.Errorf("name/caps wrong")
	}
}

func TestNewClientResolvesQuirks(t *testing.T) {
	c := NewClient(Config{Backend: "gemini", Model: "gemini-2.5-flash"})
	if !reflect.DeepEqual(c.quirks, quirksFor("gemini")) {
		t.Errorf("client quirks = %+v, want %+v", c.quirks, quirksFor("gemini"))
	}
}

func TestNewClientDefaultQuirks(t *testing.T) {
	c := NewClient(Config{}) // empty backend → defensive default
	if !c.quirks.ImagesAsBase64 || !c.quirks.NormalizeErrors {
		t.Errorf("empty backend should get defensive quirks, got %+v", c.quirks)
	}
}
