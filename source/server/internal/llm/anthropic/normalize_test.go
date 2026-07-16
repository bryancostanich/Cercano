package anthropic

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

// fixture spins up a stub API that always answers with the given status,
// headers, and body, and returns a Client pointed at it plus the request
// counter (to pin "SDK retries are off": every failure = exactly one request).
func fixture(t *testing.T, status int, header map[string]string, body string) (*Client, *atomic.Int32) {
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
	return NewClient(Config{APIKey: "k", BaseURL: srv.URL}), &hits
}

func chatErr(t *testing.T, c *Client) error {
	t.Helper()
	_, err := c.Chat(context.Background(), llm.ChatRequest{
		Model:    "claude-opus-4-8",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	return err
}

func TestNormalize_QuotaScale429(t *testing.T) {
	c, hits := fixture(t, 429, map[string]string{"Retry-After": "3600"},
		`{"type":"error","error":{"type":"rate_limit_error","message":"usage limit reached"}}`)
	err := chatErr(t, c)
	var ne *llm.Error
	if !errors.As(err, &ne) {
		t.Fatalf("want *llm.Error, got %T: %v", err, err)
	}
	if ne.Class != llm.ErrQuota {
		t.Errorf("class = %q, want quota", ne.Class)
	}
	if ne.RetryAfter != time.Hour {
		t.Errorf("RetryAfter = %v, want 1h", ne.RetryAfter)
	}
	if ne.StatusCode != 429 || ne.Provider != "anthropic" {
		t.Errorf("got %+v", ne)
	}
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want 1 (SDK retries must be off)", hits.Load())
	}
}

func TestNormalize_TransientClasses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		want   llm.ErrorClass
	}{
		{"short retry-after 429", 429, map[string]string{"Retry-After": "2"},
			`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`, llm.ErrBusy},
		{"usage-limit 429 without Retry-After is still quota", 429, nil,
			`{"type":"error","error":{"type":"rate_limit_error","message":"You have exceeded your usage limit."}}`, llm.ErrQuota},
		{"bare 429", 429, nil,
			`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`, llm.ErrBusy},
		{"529 overloaded", 529, nil,
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, llm.ErrBusy},
		{"500", 500, nil,
			`{"type":"error","error":{"type":"api_error","message":"internal"}}`, llm.ErrBusy},
		{"401", 401, nil,
			`{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`, llm.ErrAuth},
		{"403", 403, nil,
			`{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`, llm.ErrAuth},
		{"generic 400", 400, nil,
			`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens required"}}`, llm.ErrInvalidRequest},
		{"credit-balance 400", 400, nil,
			`{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`, llm.ErrQuota},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, hits := fixture(t, tc.status, tc.header, tc.body)
			err := chatErr(t, c)
			if got := llm.ClassOf(err); got != tc.want {
				t.Errorf("class = %q, want %q (err: %v)", got, tc.want, err)
			}
			if hits.Load() != 1 {
				t.Errorf("requests = %d, want 1 (SDK retries must be off)", hits.Load())
			}
		})
	}
}

func TestNormalize_NetworkError(t *testing.T) {
	// Unroutable per RFC 5737; connection fails at the transport layer.
	c := NewClient(Config{APIKey: "k", BaseURL: "http://192.0.2.1:1"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Chat(ctx, llm.ChatRequest{Model: "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil {
		t.Skip("environment routed the black-hole address")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Skip("transport hung until deadline; class check not meaningful")
	}
	if got := llm.ClassOf(err); got != llm.ErrNetwork {
		t.Errorf("class = %q, want network (err: %v)", got, err)
	}
}

func TestNormalize_StreamFirstReadCarriesClass(t *testing.T) {
	c, hits := fixture(t, 429, map[string]string{"Retry-After": "3600"},
		`{"type":"error","error":{"type":"rate_limit_error","message":"usage limit reached"}}`)
	r, err := c.StreamChat(context.Background(), llm.ChatRequest{Model: "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err != nil {
		// Dial-time normalization is also acceptable.
		if got := llm.ClassOf(err); got != llm.ErrQuota {
			t.Fatalf("dial class = %q, want quota", got)
		}
		return
	}
	defer r.Close()
	_, ok, err := r.Next()
	if ok || err == nil {
		t.Fatalf("want first-read failure, got ok=%v err=%v", ok, err)
	}
	if got := llm.ClassOf(err); got != llm.ErrQuota {
		t.Errorf("class = %q, want quota (err: %v)", got, err)
	}
	if hits.Load() != 1 {
		t.Errorf("requests = %d, want 1 (SDK retries must be off)", hits.Load())
	}
}

func TestNormalize_ContextCancelPassesThrough(t *testing.T) {
	c, _ := fixture(t, 200, nil, `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Chat(ctx, llm.ChatRequest{Model: "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}}})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation must stay reachable, got %v", err)
	}
	if llm.ClassOf(err) != llm.ErrUnknown {
		t.Errorf("cancellation must NOT be normalized into a provider class")
	}
}
