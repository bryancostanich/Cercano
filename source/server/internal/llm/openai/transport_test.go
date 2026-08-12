package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestNormalizeStringError: a `{"error":"<string>"}` body (llama-server's
// "Compute error" shape) is reshaped so go-openai types it into an APIError and
// the real message survives instead of go-openai's "unmarshal string" artifact.
func TestNormalizeStringError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, `{"error":"Compute error"}`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: true}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Compute error") {
		t.Fatalf("real message lost after reshape: %v", err)
	}
	// The reshape must preserve the HTTP 500 in the surfaced error.
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("status 500 lost after reshape: %v", err)
	}
}

// TestNormalizeStringError_Disabled: with NormalizeErrors off, the body is left
// untouched (no reshape happens).
func TestNormalizeStringError_Disabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, `{"error":"Compute error"}`)
	}))
	defer srv.Close()

	_, err := clientTo(srv, Quirks{NormalizeErrors: false}).CreateChatCompletion(context.Background(), chatReq())
	if err == nil {
		t.Fatal("expected an error even with normalization disabled")
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
