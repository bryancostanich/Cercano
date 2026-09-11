package anthropic

import (
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type streamAuthTokens struct{}

func (streamAuthTokens) Token(context.Context) (string, error) { return "synthetic-token", nil }
func (streamAuthTokens) CredentialProfile() string             { return "work" }
func TestInBandAuthenticationPreservesProfileAndHidesPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"secret-token-reflected\"}}\n\n"))
	}))
	defer server.Close()
	client := NewClient(Config{Route: RouteSubscription, BaseURL: server.URL, TokenSource: streamAuthTokens{}})
	reader, err := client.StreamChat(context.Background(), llm.ChatRequest{Model: "test", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	_, _, err = reader.Next()
	var credential *llm.CredentialError
	if llm.ClassOf(err) != llm.ErrLoginRequired || !errors.As(err, &credential) || credential.Profile != "work" || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("in-band auth not actionable/safe: %v", err)
	}
}
