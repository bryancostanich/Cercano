package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

func oaFixture(t *testing.T, status int, body string) (*Client, *atomic.Int32) {
	t.Helper()
	return oaFixtureBackend(t, "openai", status, body)
}

func oaFixtureBackend(t *testing.T, backend string, status int, body string) (*Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{APIKey: "k", BaseURL: srv.URL + "/v1", Model: "gpt-5.5", Backend: backend}), &hits
}

func oaChatErr(t *testing.T, c *Client) error {
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
		body   string
		want   llm.ErrorClass
	}{
		{"insufficient_quota 429", 429,
			`{"error":{"message":"You exceeded your current quota.","type":"insufficient_quota","code":"insufficient_quota"}}`, llm.ErrQuota},
		{"transient 429", 429,
			`{"error":{"message":"Rate limit reached for gpt-5.5.","type":"requests"}}`, llm.ErrBusy},
		{"500", 500,
			`{"error":{"message":"The server had an error.","type":"server_error"}}`, llm.ErrBusy},
		{"401", 401,
			`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error"}}`, llm.ErrAuth},
		{"404 model", 404,
			`{"error":{"message":"The model does not exist.","type":"invalid_request_error"}}`, llm.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, hits := oaFixture(t, tc.status, tc.body)
			err := oaChatErr(t, c)
			if got := llm.ClassOf(err); got != tc.want {
				t.Errorf("class = %q, want %q (err: %v)", got, tc.want, err)
			}
			if hits.Load() != 1 {
				t.Errorf("requests = %d, want 1 (transport retries must be gone)", hits.Load())
			}
		})
	}
}

func TestNormalize_MistralRSNoResponseIsInvalidRequest(t *testing.T) {
	body := `{"message":"No response received from the model."}`
	c, _ := oaFixtureBackend(t, "mistralrs", http.StatusInternalServerError, body)
	if got := llm.ClassOf(oaChatErr(t, c)); got != llm.ErrInvalidRequest {
		t.Fatalf("mistralrs class = %q, want %q", got, llm.ErrInvalidRequest)
	}

	// The quirk must stay backend-scoped: a generic OpenAI 500 is transient
	// busy and remains eligible for the normal bounded retry/failover policy.
	c, _ = oaFixtureBackend(t, "openai", http.StatusInternalServerError, body)
	if got := llm.ClassOf(oaChatErr(t, c)); got != llm.ErrBusy {
		t.Fatalf("openai class = %q, want %q", got, llm.ErrBusy)
	}
}

func TestNormalize_VendorErrorStaysReachable(t *testing.T) {
	c, _ := oaFixture(t, 429,
		`{"error":{"message":"You exceeded your current quota.","code":"insufficient_quota"}}`)
	err := oaChatErr(t, c)
	var ne *llm.Error
	if !errors.As(err, &ne) {
		t.Fatalf("want *llm.Error, got %T", err)
	}
	if ne.Provider != "openai" || ne.StatusCode != 429 {
		t.Errorf("got %+v", ne)
	}
	var ae *goopenai.APIError
	if !errors.As(err, &ae) {
		t.Error("vendor error must stay reachable through the normalized wrapper")
	}
}
