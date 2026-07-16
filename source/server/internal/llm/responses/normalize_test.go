package responses

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
)

func respFixture(t *testing.T, status int, header map[string]string, body string) (*Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "gpt-5.5"}), &hits
}

func respChatErr(t *testing.T, c *Client) error {
	t.Helper()
	_, err := c.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	return err
}

func TestNormalize_Classes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		want   llm.ErrorClass
	}{
		{"insufficient_quota 429", 429, nil,
			`{"error":{"message":"You exceeded your current quota.","type":"insufficient_quota","code":"insufficient_quota"}}`, llm.ErrQuota},
		{"codex usage-limit detail", 429, nil,
			`{"detail":"You've hit your usage limit."}`, llm.ErrQuota},
		{"quota-scale retry-after 429", 429, map[string]string{"Retry-After": "1800"},
			`{"error":{"message":"Rate limit reached.","type":"requests"}}`, llm.ErrQuota},
		{"transient 429", 429, map[string]string{"Retry-After": "1"},
			`{"error":{"message":"Rate limit reached.","type":"requests"}}`, llm.ErrBusy},
		{"bare 429", 429, nil,
			`{"error":{"message":"Rate limit reached.","type":"requests"}}`, llm.ErrBusy},
		{"503", 503, nil,
			`{"error":{"message":"The engine is currently overloaded.","type":"server_error"}}`, llm.ErrBusy},
		{"401", 401, nil,
			`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error"}}`, llm.ErrAuth},
		{"400", 400, nil,
			`{"error":{"message":"Invalid value for input.","type":"invalid_request_error"}}`, llm.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, hits := respFixture(t, tc.status, tc.header, tc.body)
			err := respChatErr(t, c)
			if got := llm.ClassOf(err); got != tc.want {
				t.Errorf("class = %q, want %q (err: %v)", got, tc.want, err)
			}
			if hits.Load() != 1 {
				t.Errorf("requests = %d, want 1 (transport retries must be gone)", hits.Load())
			}
		})
	}
}

func TestNormalize_RetryAfterSurfaced(t *testing.T) {
	c, _ := respFixture(t, 429, map[string]string{"Retry-After": "1800"},
		`{"error":{"message":"Rate limit reached."}}`)
	err := respChatErr(t, c)
	var ne *llm.Error
	if !errors.As(err, &ne) {
		t.Fatalf("want *llm.Error, got %T", err)
	}
	if ne.RetryAfter != 30*time.Minute {
		t.Errorf("RetryAfter = %v, want 30m", ne.RetryAfter)
	}
	if ne.Provider != "openai-responses" {
		t.Errorf("provider = %q", ne.Provider)
	}
}

func TestNormalize_TransportErrorIsNetwork(t *testing.T) {
	c := NewClient(Config{APIKey: "k", BaseURL: "http://127.0.0.1:1", Model: "gpt-5.5"})
	_, err := c.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err == nil {
		t.Skip("port 1 unexpectedly reachable")
	}
	if got := llm.ClassOf(err); got != llm.ErrNetwork {
		t.Errorf("class = %q, want network (err: %v)", got, err)
	}
}

func TestNormalize_AuthTokenFailureIsAuthClass(t *testing.T) {
	c := NewClient(Config{Model: "gpt-5.5", Route: RouteChatGPT,
		TokenSource: failingTokens{}})
	_, err := c.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if got := llm.ClassOf(err); got != llm.ErrAuth {
		t.Errorf("class = %q, want auth (err: %v)", got, err)
	}
}

type failingTokens struct{}

func (failingTokens) Token(context.Context) (string, string, error) {
	return "", "", errors.New("refresh token rejected")
}
