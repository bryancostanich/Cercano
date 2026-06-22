package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestClient_NewClient_SetsBaseURLAndUA(t *testing.T) {
	var seenURL string
	var seenUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.Path
		seenUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{
		BaseURL:   srv.URL,
		APIKey:    "dummy",
		Model:     "claude-opus-4-7",
		UserAgent: "claude-cli/test",
	})
	caps := c.Capabilities()
	if !caps.SupportsTools || !caps.SupportsParallelTools {
		t.Errorf("expected tool support, got %+v", caps)
	}
	_, err := c.Chat(t.Context(), simpleReq())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seenURL, "/v1/messages") {
		t.Errorf("expected /v1/messages, got %q", seenURL)
	}
	if seenUA != "claude-cli/test" {
		t.Errorf("expected custom UA, got %q", seenUA)
	}
}

func simpleReq() ChatRequest {
	return ChatRequest{Model: "claude-opus-4-7", MaxTokens: 10}
}

func TestClient_Chat_SendsMessagesAndTools(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"got it"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	resp, err := c.Chat(t.Context(), ChatRequest{
		Model:     "claude",
		MaxTokens: 100,
		System:    "you are concise",
		Messages: []llm.Message{{
			Role:   llm.RoleUser,
			Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}},
		}},
		Tools: []llm.Tool{{
			Name:        "ping",
			Description: "ping",
			Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := string(seenBody)
	if !strings.Contains(body, `"system"`) || !strings.Contains(body, "you are concise") {
		t.Errorf("system not sent: %s", body)
	}
	if !strings.Contains(body, `"tools"`) || !strings.Contains(body, "ping") {
		t.Errorf("tools not sent: %s", body)
	}
	if !strings.Contains(body, `"hi"`) {
		t.Errorf("user message not sent: %s", body)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != llm.BlockText || resp.Blocks[0].Text != "got it" {
		t.Errorf("blocks not converted: %+v", resp.Blocks)
	}
}

func TestClient_Chat_SessionHeaders(t *testing.T) {
	var seenSession, seenRequest, seenMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("x-opencode-session")
		seenRequest = r.Header.Get("x-opencode-request")
		seenMode = r.Header.Get("x-opencode-agent-mode")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude"})
	ctx := WithSessionID(t.Context(), "conv-abc-123")
	_, err := c.Chat(ctx, ChatRequest{Model: "claude", MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}

	if seenSession != "conv-abc-123" {
		t.Errorf("session header: got %q want conv-abc-123", seenSession)
	}
	if seenRequest == "" || !strings.HasPrefix(seenRequest, "msg-") {
		t.Errorf("request header missing or wrong shape: %q", seenRequest)
	}
	if seenMode != "primary" {
		t.Errorf("agent-mode: got %q want primary", seenMode)
	}
}

func TestClient_Chat_NoSessionHeader_WhenUnset(t *testing.T) {
	var seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy"})
	_, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if seenSession != "" {
		t.Errorf("expected no session header without context value, got %q", seenSession)
	}
}
