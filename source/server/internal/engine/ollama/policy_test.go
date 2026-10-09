package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/modelpolicy"
)

func TestDirectOllamaRequestsRequirePolicy(t *testing.T) {
	var hits, checks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	eng := NewOllamaEngine(server.URL)
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
		checks.Add(1)
		if a.Provider != "ollama" || a.Endpoint != server.URL || a.Model != "blocked" || a.Placement != "local" {
			t.Errorf("wrong route: %+v", a)
		}
		return modelpolicy.Deny(a, "not approved")
	}))
	calls := []func() error{
		func() error { _, err := eng.Complete(ctx, "blocked", "prompt", "", engine.GenOptions{}); return err },
		func() error {
			_, err := eng.CompleteStream(ctx, "blocked", "prompt", "", engine.GenOptions{}, func(string) {})
			return err
		},
		func() error { _, err := eng.ChatWithTools(ctx, engine.ChatRequest{Model: "blocked"}); return err },
		func() error { _, err := eng.Embed(ctx, "blocked", "text"); return err },
	}
	for _, call := range calls {
		if err := call(); !modelpolicy.IsDenial(err) {
			t.Fatalf("denial lost: %v", err)
		}
	}
	if hits.Load() != 0 || checks.Load() != int32(len(calls)) {
		t.Fatalf("hits=%d checks=%d", hits.Load(), checks.Load())
	}
}
