package llm_test

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
)

type mockEngine struct {
	name           string
	completeResult string
	completeError  error
	streamChunks   []string
}

func (m *mockEngine) Complete(ctx context.Context, model, prompt, systemPrompt string) (string, error) {
	return m.completeResult, m.completeError
}

func (m *mockEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (string, error) {
	var accumulated strings.Builder
	for _, chunk := range m.streamChunks {
		if onToken != nil {
			onToken(chunk)
		}
		accumulated.WriteString(chunk)
	}
	return accumulated.String(), m.completeError
}

func (m *mockEngine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return nil, nil
}

func (m *mockEngine) Name() string {
	return m.name
}

func TestLocalModelProvider_Process(t *testing.T) {
	eng := &mockEngine{
		name:           "mock",
		completeResult: "success",
	}
	provider := llm.NewLocalModelProvider(eng, "test-model")

	resp, err := provider.Process(context.Background(), &agent.Request{Input: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "success" {
		t.Errorf("expected 'success', got %q", resp.Output)
	}
}

func TestLocalModelProvider_ProcessStream(t *testing.T) {
	eng := &mockEngine{
		name:         "mock",
		streamChunks: []string{"a", "b", "c"},
	}
	provider := llm.NewLocalModelProvider(eng, "test-model")

	var tokens []string
	resp, err := provider.ProcessStream(context.Background(), &agent.Request{Input: "hello"}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "abc" {
		t.Errorf("expected 'abc', got %q", resp.Output)
	}
	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(tokens))
	}
}

func TestLocalModelProvider_SetModelName(t *testing.T) {
	eng := &mockEngine{name: "mock"}
	provider := llm.NewLocalModelProvider(eng, "test-model")

	if provider.Name() != "test-model" {
		t.Errorf("expected 'test-model', got %q", provider.Name())
	}

	provider.SetModelName("new-model")
	if provider.Name() != "new-model" {
		t.Errorf("expected 'new-model', got %q", provider.Name())
	}
}
