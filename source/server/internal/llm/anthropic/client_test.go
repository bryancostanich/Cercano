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

	// Headers are gated on Route=="meridian": Meridian uses them to pick its
	// OpenCode adapter (looser SDK turn cap). Direct route does not emit them.
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Model: "claude", Route: "meridian"})
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

// Even with a session id in ctx, the direct route must not emit opencode-*
// identification headers — those are a Meridian-specific borrowed identity.
func TestClient_Chat_NoOpencodeHeaders_OnDirectRoute(t *testing.T) {
	var seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Route: "direct"})
	ctx := WithSessionID(t.Context(), "conv-direct-1")
	if _, err := c.Chat(ctx, ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if seenSession != "" {
		t.Errorf("direct route must not send x-opencode-session, got %q", seenSession)
	}
}

// On the meridian route, a call with NO session in ctx must still get a
// session header — a fresh random one per request. Headerless requests fall
// through to Meridian's content-fingerprint session matching (sha256 of cwd +
// first user message), which collides across concurrent conversations with
// templated prompts and cross-delivers their turns. A random id gives the
// stray call an isolated lineage instead.
func TestClient_Chat_MeridianRoute_MintsSessionWhenUnset(t *testing.T) {
	var sessions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Route: "meridian"})
	for i := 0; i < 2; i++ {
		if _, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if len(sessions) != 2 || sessions[0] == "" || sessions[1] == "" {
		t.Fatalf("expected a minted session header on every unstamped meridian request, got %v", sessions)
	}
	if sessions[0] == sessions[1] {
		t.Errorf("minted session ids must be unique per request, got %q twice", sessions[0])
	}
}

// The provider-neutral llm.WithSessionID must reach the wire the same way the
// adapter-local helper does — callers outside this package (dispatch, server)
// stamp through it.
func TestClient_Chat_SessionHeader_FromLLMContextKey(t *testing.T) {
	var seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Route: "meridian"})
	ctx := llm.WithSessionID(t.Context(), "llm-scoped-77")
	if _, err := c.Chat(ctx, ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if seenSession != "llm-scoped-77" {
		t.Errorf("session header: got %q want llm-scoped-77", seenSession)
	}
}

// A call marked as an independent session (dispatch subagent / one-shot) must
// emit x-meridian-source with a subagent- prefix on the meridian route, which
// tells Meridian's adapter to skip lineage matching entirely — a second layer
// of isolation on top of the unique session id, so a subagent can never be
// mistaken for a continuation of the parent conversation.
func TestClient_Chat_IndependentSession_EmitsMeridianSource(t *testing.T) {
	var seenSource, seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSource = r.Header.Get("x-meridian-source")
		seenSession = r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Route: "meridian"})
	ctx := llm.WithSessionID(t.Context(), "sub-42")
	ctx = llm.WithIndependentSession(ctx)
	if _, err := c.Chat(ctx, ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if seenSource != "subagent-sub-42" {
		t.Errorf("x-meridian-source: got %q want subagent-sub-42", seenSource)
	}
	if seenSession != "sub-42" {
		t.Errorf("session header should still ride alongside: got %q", seenSession)
	}
}

// A normal conversational turn must NOT emit x-meridian-source — it wants
// Meridian's lineage matching, not the skip.
func TestClient_Chat_ConversationalTurn_NoMeridianSource(t *testing.T) {
	var seenSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSource = r.Header.Get("x-meridian-source")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, APIKey: "dummy", Route: "meridian"})
	ctx := llm.WithSessionID(t.Context(), "conv-1")
	if _, err := c.Chat(ctx, ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if seenSource != "" {
		t.Errorf("conversational turn must not emit x-meridian-source, got %q", seenSource)
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
