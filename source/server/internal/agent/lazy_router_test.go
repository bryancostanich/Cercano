package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// stubProvider is a minimal ModelProvider used only to populate the map that
// LazyRouter returns before the real router is built.
type stubProvider struct{ name string }

func (s *stubProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{}, nil
}
func (s *stubProvider) Name() string { return s.name }

func TestLazyRouter_DoesNotBuildUntilUsed(t *testing.T) {
	var builds int32
	factory := func() (*SmartRouter, error) {
		atomic.AddInt32(&builds, 1)
		return nil, errors.New("factory should not have been called")
	}
	local := &stubProvider{name: "LocalModel"}
	cloud := &stubProvider{name: "CloudModel"}

	lr := NewLazyRouter(factory, local, cloud)

	// GetModelProviders must NOT trigger construction — the DirectLocal bypass
	// relies on reading providers without the router ever being built.
	providers := lr.GetModelProviders()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
	if providers["LocalModel"] != local {
		t.Error("LocalModel provider missing or wrong")
	}
	if providers["CloudModel"] != cloud {
		t.Error("CloudModel provider missing or wrong")
	}

	if atomic.LoadInt32(&builds) != 0 {
		t.Errorf("expected factory to not be called, was called %d times", builds)
	}
}

func TestLazyRouter_ClassifyIntent_BuildsOnce(t *testing.T) {
	var builds int32
	factory := func() (*SmartRouter, error) {
		atomic.AddInt32(&builds, 1)
		// Return the same kind of error the real factory throws when
		// nomic-embed-text is missing on the Ollama host.
		return nil, errors.New("failed to get embedding for prototype 'foo': ollama error: {\"error\":\"model \\\"nomic-embed-text\\\" not found, try pulling it first\"}")
	}
	lr := NewLazyRouter(factory, &stubProvider{name: "LocalModel"}, nil)

	// First call triggers the factory and surfaces the wrapped error.
	_, err := lr.ClassifyIntent(&Request{Input: "test"})
	if err == nil {
		t.Fatal("expected error on first ClassifyIntent")
	}
	if !strings.Contains(err.Error(), "agent-mode routing requires") {
		t.Errorf("error not wrapped with remediation guidance: %v", err)
	}

	// Subsequent calls must return the cached error without re-invoking factory.
	for i := 0; i < 3; i++ {
		if _, err := lr.ClassifyIntent(&Request{Input: "test"}); err == nil {
			t.Errorf("expected error on retry %d", i)
		}
	}

	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Errorf("factory should have been called exactly once, got %d", got)
	}
}

func TestLazyRouter_SetCloudProvider_BeforeBuild(t *testing.T) {
	factory := func() (*SmartRouter, error) {
		return nil, errors.New("not built yet")
	}
	lr := NewLazyRouter(factory, &stubProvider{name: "LocalModel"}, nil)

	newCloud := &stubProvider{name: "NewCloud"}
	lr.SetCloudProvider(newCloud)

	// GetModelProviders must reflect the pending cloud provider even though
	// the real router hasn't been built.
	providers := lr.GetModelProviders()
	if providers["CloudModel"] != newCloud {
		t.Errorf("expected pending CloudModel to be exposed, got %v", providers["CloudModel"])
	}
}

func TestLazyRouter_WrapInitError_MissingEmbeddingModel(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{
			name:    "ollama missing model",
			raw:     `ollama error: {"error":"model "nomic-embed-text" not found, try pulling it first"}`,
			wantSub: "ollama pull nomic-embed-text",
		},
		{
			name:    "connection refused",
			raw:     "dial tcp 127.0.0.1:11434: connect: connection refused",
			wantSub: "reachable Ollama instance",
		},
		{
			name:    "no such host",
			raw:     "dial tcp: lookup ollama.example.com: no such host",
			wantSub: "reachable Ollama instance",
		},
		{
			name:    "generic fallback",
			raw:     "something unexpected happened",
			wantSub: "agent-mode routing unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := wrapRouterInitError(errors.New(tc.raw))
			if !strings.Contains(wrapped.Error(), tc.wantSub) {
				t.Errorf("expected %q in %q", tc.wantSub, wrapped.Error())
			}
			// Underlying error must remain wrapped so callers can inspect.
			if !strings.Contains(wrapped.Error(), tc.raw) {
				t.Errorf("original error %q not preserved in %q", tc.raw, wrapped.Error())
			}
		})
	}
}
