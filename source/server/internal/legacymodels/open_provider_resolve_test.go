package legacymodels_test

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/legacymodels"
)

// capturingEngine records the model name Complete/CompleteStream were called
// with, so we can assert the provider resolved it before dispatch.
type capturingEngine struct {
	gotModel string
}

func (e *capturingEngine) Complete(_ context.Context, model, _, _ string, _ engine.GenOptions) (engine.CompletionResult, error) {
	e.gotModel = model
	return engine.CompletionResult{Output: "ok"}, nil
}
func (e *capturingEngine) CompleteStream(_ context.Context, model, _, _ string, _ engine.GenOptions, _ func(string)) (engine.CompletionResult, error) {
	e.gotModel = model
	return engine.CompletionResult{Output: "ok"}, nil
}
func (e *capturingEngine) ListModels(context.Context) ([]engine.ModelInfo, error) { return nil, nil }
func (e *capturingEngine) Name() string                                           { return "capturing" }
func (e *capturingEngine) ChatWithTools(context.Context, engine.ChatRequest) (engine.ChatResponse, error) {
	return engine.ChatResponse{}, nil
}

// TestResolverCanonicalizesModelName proves the Phase-3 wiring: with a resolver
// installed, the configured name is mapped to the canonical present model
// before it reaches the engine.
func TestResolverCanonicalizesModelName(t *testing.T) {
	eng := &capturingEngine{}
	p := legacymodels.NewOpenModelProvider(eng, "qwen3-coder")
	p.SetResolver(func(_ context.Context, requested string) (string, error) {
		if requested != "qwen3-coder" {
			t.Errorf("resolver got %q, want qwen3-coder", requested)
		}
		return "ollama:qwen3-coder-canonical", nil
	})

	if _, err := p.Process(context.Background(), &agent.Request{Input: "hi"}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if eng.gotModel != "ollama:qwen3-coder-canonical" {
		t.Errorf("engine got model %q, want the resolved canonical name", eng.gotModel)
	}
}

// TestResolverErrorDegradesInsteadOfDispatch proves the core bug fix: when the
// model can't be resolved to a present model, the provider returns the error
// and NEVER dispatches an unresolved name to the engine (which would fail at
// load — the compaction "not downloaded" hard-fail).
func TestResolverErrorDegradesInsteadOfDispatch(t *testing.T) {
	eng := &capturingEngine{}
	p := legacymodels.NewOpenModelProvider(eng, "qwen3-coder")
	sentinel := errors.New("model resolved but not downloaded")
	p.SetResolver(func(context.Context, string) (string, error) {
		return "", sentinel
	})

	_, err := p.Process(context.Background(), &agent.Request{Input: "hi"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Process err = %v, want the resolver error", err)
	}
	if eng.gotModel != "" {
		t.Errorf("engine should NOT have been called on resolve failure, got model %q", eng.gotModel)
	}

	// Same guarantee on the streaming path.
	_, err = p.ProcessStream(context.Background(), &agent.Request{Input: "hi"}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("ProcessStream err = %v, want the resolver error", err)
	}
}

// TestNoResolverIsPassthrough preserves legacy behavior: without a resolver the
// configured name reaches the engine unchanged.
func TestNoResolverIsPassthrough(t *testing.T) {
	eng := &capturingEngine{}
	p := legacymodels.NewOpenModelProvider(eng, "raw-model-name")
	if _, err := p.Process(context.Background(), &agent.Request{Input: "hi"}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if eng.gotModel != "raw-model-name" {
		t.Errorf("without resolver, engine should get raw name; got %q", eng.gotModel)
	}
}

// TestResolverSeesModelOverride proves a per-request override (research uses a
// different model) is what gets resolved, not the configured default.
func TestResolverSeesModelOverride(t *testing.T) {
	eng := &capturingEngine{}
	p := legacymodels.NewOpenModelProvider(eng, "default-model")
	p.SetResolver(func(_ context.Context, requested string) (string, error) {
		if requested != "override-model" {
			t.Errorf("resolver got %q, want the override", requested)
		}
		return "resolved:" + requested, nil
	})
	if _, err := p.Process(context.Background(), &agent.Request{Input: "hi", ModelOverride: "override-model"}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if eng.gotModel != "resolved:override-model" {
		t.Errorf("engine got %q, want resolved override", eng.gotModel)
	}
}
