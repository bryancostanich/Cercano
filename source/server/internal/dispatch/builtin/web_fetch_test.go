package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetch_HTMLExtracted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Hello</h1><p>World</p></body></html>`))
	}))
	defer srv.Close()
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("got %q, want it to contain Hello and World", got)
	}
}

func TestWebFetch_Non200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestWebFetch_MalformedURL(t *testing.T) {
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": "::not-a-url"})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error on malformed URL")
	}
}

func TestWebFetch_BadArgs(t *testing.T) {
	tool := NewWebFetch()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
