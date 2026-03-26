package engine_test

import (
	"context"
	"testing"

	"cercano/source/server/internal/engine"
)

type mockEngine struct {
	name string
}

func (m *mockEngine) Complete(ctx context.Context, model, prompt, systemPrompt string) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, nil
}

func (m *mockEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, nil
}

func (m *mockEngine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return nil, nil
}

func (m *mockEngine) Name() string {
	return m.name
}

type mockEmbedder struct {
	name string
}

func (m *mockEmbedder) Embed(ctx context.Context, model, text string) ([]float64, error) {
	return nil, nil
}

func (m *mockEmbedder) Name() string {
	return m.name
}

func TestEngineRegistry(t *testing.T) {
	registry := engine.NewEngineRegistry()

	// Register Engines
	e1 := &mockEngine{name: "engine1"}
	e2 := &mockEngine{name: "engine2"}
	registry.RegisterEngine(e1)
	registry.RegisterEngine(e2)

	// Register Embedders
	emb1 := &mockEmbedder{name: "emb1"}
	registry.RegisterEmbedder(emb1)

	// GetEngine success
	gotE1, err := registry.GetEngine("engine1")
	if err != nil {
		t.Fatalf("expected to find engine1, got error: %v", err)
	}
	if gotE1.Name() != "engine1" {
		t.Errorf("expected engine1, got %v", gotE1.Name())
	}

	// GetEngine missing
	_, err = registry.GetEngine("missing")
	if err == nil {
		t.Errorf("expected error getting missing engine")
	}

	// ListEngines
	engines := registry.ListEngines()
	if len(engines) != 2 {
		t.Errorf("expected 2 engines, got %d", len(engines))
	}

	// GetEmbedder success
	gotEmb1, err := registry.GetEmbedder("emb1")
	if err != nil {
		t.Fatalf("expected to find emb1, got error: %v", err)
	}
	if gotEmb1.Name() != "emb1" {
		t.Errorf("expected emb1, got %v", gotEmb1.Name())
	}

	// GetEmbedder missing
	_, err = registry.GetEmbedder("missing")
	if err == nil {
		t.Errorf("expected error getting missing embedder")
	}

	// ListEmbedders
	embedders := registry.ListEmbedders()
	if len(embedders) != 1 {
		t.Errorf("expected 1 embedder, got %d", len(embedders))
	}
}
