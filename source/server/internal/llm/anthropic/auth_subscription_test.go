package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// stubTokens is a fixed TokenSource for tests.
type stubTokens struct {
	tok string
	err error
}

func (s stubTokens) Token(ctx context.Context) (string, error) { return s.tok, s.err }

func msgReplyServer(t *testing.T, capture func(r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"m_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
}

// The subscription route must authenticate with a Bearer token, add the oauth
// beta header, and strip the SDK's placeholder x-api-key so only the Bearer
// credential reaches Anthropic.
func TestSubscriptionAuth_SetsBearerAndBeta_StripsApiKey(t *testing.T) {
	var auth, beta, apiKey string
	srv := msgReplyServer(t, func(r *http.Request) {
		auth = r.Header.Get("Authorization")
		beta = r.Header.Get("anthropic-beta")
		apiKey = r.Header.Get("x-api-key")
	})
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Route: "subscription", TokenSource: stubTokens{tok: "tok-123"}})
	if _, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", auth)
	}
	if beta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want oauth-2025-04-20", beta)
	}
	if apiKey != "" {
		t.Errorf("x-api-key must be stripped on the subscription route, got %q", apiKey)
	}
}

// The Claude Code identity block must lead the system prompt, ahead of the
// caller's own system text — Anthropic gates subscription access on it.
func TestSubscriptionAuth_PrependsIdentitySystemBlock(t *testing.T) {
	var body string
	srv := msgReplyServer(t, func(r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	})
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Route: "subscription", TokenSource: stubTokens{tok: "t"}})
	_, err := c.Chat(t.Context(), ChatRequest{
		Model: "claude", MaxTokens: 10, System: "be terse",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	idIdx := strings.Index(body, "Claude Code")
	sysIdx := strings.Index(body, "be terse")
	if idIdx < 0 || sysIdx < 0 {
		t.Fatalf("system prompt missing identity or caller text: %s", body)
	}
	if idIdx > sysIdx {
		t.Errorf("identity block must come first; got identity@%d after system@%d", idIdx, sysIdx)
	}
}

// A subscription client with no token source fails the call rather than
// sending an unauthenticated request.
func TestSubscriptionAuth_NilTokenSource_Errors(t *testing.T) {
	srv := msgReplyServer(t, nil)
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Route: "subscription"})
	if _, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10}); err == nil ||
		!strings.Contains(err.Error(), "subscription") {
		t.Fatalf("want a subscription token-source error, got %v", err)
	}
}

// A token-source failure (e.g. expired with no refresh) surfaces as a call
// error, not a silent unauthenticated request.
func TestSubscriptionAuth_TokenError_Propagates(t *testing.T) {
	srv := msgReplyServer(t, nil)
	defer srv.Close()
	c := NewClient(Config{BaseURL: srv.URL, Route: "subscription", TokenSource: stubTokens{err: io.ErrUnexpectedEOF}})
	if _, err := c.Chat(t.Context(), ChatRequest{Model: "claude", MaxTokens: 10}); err == nil ||
		!strings.Contains(err.Error(), "subscription token") {
		t.Fatalf("want a propagated subscription token error, got %v", err)
	}
}

// Direct and meridian routes must not gain the subscription identity block.
func TestSystemPrefix_OnlyOnSubscription(t *testing.T) {
	if got := systemPrefixForRoute("subscription"); got != claudeCodeIdentity {
		t.Errorf("subscription prefix = %q, want the identity", got)
	}
	for _, r := range []string{"", "direct", "meridian"} {
		if got := systemPrefixForRoute(r); got != "" {
			t.Errorf("route %q prefix = %q, want empty", r, got)
		}
	}
}
