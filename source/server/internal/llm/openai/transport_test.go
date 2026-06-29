package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goopenai "github.com/sashabaranov/go-openai"
)

// clientTo builds a go-openai client whose transport is a normalizingDoer
// pointed at srv, with the given quirks.
func clientTo(srv *httptest.Server, q Quirks) *goopenai.Client {
	cfg := goopenai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL
	cfg.HTTPClient = &normalizingDoer{next: &http.Client{}, quirks: q}
	return goopenai.NewClientWithConfig(cfg)
}

func chatReq() goopenai.ChatCompletionRequest {
	return goopenai.ChatCompletionRequest{
		Model:    "m",
		Messages: []goopenai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	}
}

// TestNormalizeArrayError: a Gemini-style array error body yields a clean,
// parsed APIError message instead of go-openai's "unmarshal array" artifact.
func TestNormalizeArrayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `[{"error":{"message":"Cannot fetch content from the provided URL","type":"invalid_request_error","code":400}}]`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: true}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "unmarshal array") {
		t.Fatalf("error not normalized: %v", err)
	}
	if !strings.Contains(err.Error(), "Cannot fetch content") {
		t.Fatalf("real message lost: %v", err)
	}
}

// TestObjectErrorUnchanged: an already-object error body still parses cleanly.
func TestObjectErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad request here","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: true}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil || !strings.Contains(err.Error(), "bad request here") {
		t.Fatalf("expected clean object error, got %v", err)
	}
}

// TestRetryThenSuccess: 503 twice then 200 → one success, three attempts.
func TestRetryThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"high demand"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	q := Quirks{Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	resp, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq())
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected 3 attempts, got %d", hits)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestRetryExhausted: always 503 → error after exactly MaxAttempts attempts.
func TestRetryExhausted(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()

	q := Quirks{NormalizeErrors: true, Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	_, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected 3 attempts, got %d", hits)
	}
}

// TestRetryBodyResent: each attempt re-sends the full request body.
func TestRetryBodyResent(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	q := Quirks{Retry: RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, OnStatus: []int{503}}}
	if _, err := clientTo(srv, q).CreateChatCompletion(context.Background(), chatReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("body not resent identically: %#v", bodies)
	}
}

// TestRetryContextCancel: a cancelled context aborts the backoff promptly.
func TestRetryContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	q := Quirks{Retry: RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, OnStatus: []int{503}}}

	done := make(chan struct{})
	go func() {
		clientTo(srv, q).CreateChatCompletion(ctx, chatReq())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("context cancel did not abort backoff")
	}
}
