package responses

import (
	"context"
	"net/http"
	"testing"
)

type stubTokens struct {
	access, account string
	err             error
}

func (s stubTokens) Token(ctx context.Context) (string, string, error) {
	return s.access, s.account, s.err
}

func TestAuthorizeStaticKey(t *testing.T) {
	c := &Client{apiKey: "sk-123"}
	req, _ := http.NewRequest(http.MethodPost, "http://x/responses", nil)
	if err := c.authorize(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-123" {
		t.Errorf("authorization: got %q", got)
	}
	if req.Header.Get("ChatGPT-Account-Id") != "" {
		t.Error("static key path must not set ChatGPT-Account-Id")
	}
}

func TestAuthorizeTokenSource(t *testing.T) {
	c := &Client{tokens: stubTokens{access: "tok", account: "acct-1"}}
	req, _ := http.NewRequest(http.MethodPost, "http://x/responses", nil)
	if err := c.authorize(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("authorization: got %q", got)
	}
	if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct-1" {
		t.Errorf("account id: got %q", got)
	}
	if got := req.Header.Get("originator"); got != "cercano" {
		t.Errorf("originator: got %q", got)
	}
}

func TestChatGPTRoutePinsCodexBaseURL(t *testing.T) {
	c := NewClient(Config{Route: RouteChatGPT, BaseURL: "https://ignored.example", TokenSource: stubTokens{}})
	if c.baseURL != CodexBaseURL {
		t.Errorf("base url: got %q want %q", c.baseURL, CodexBaseURL)
	}
}
