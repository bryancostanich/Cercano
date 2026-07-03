package legacymodels_test

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/legacymodels"
)

type mockEngine struct {
	name           string
	completeResult string
	completeError  error
	streamChunks   []string
	gotOpts        engine.GenOptions
}

func (m *mockEngine) Complete(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions) (engine.CompletionResult, error) {
	m.gotOpts = opts
	return engine.CompletionResult{Output: m.completeResult, InputTokens: 10, OutputTokens: 5}, m.completeError
}

func (m *mockEngine) CompleteStream(ctx context.Context, model, prompt, systemPrompt string, opts engine.GenOptions, onToken func(string)) (engine.CompletionResult, error) {
	var accumulated strings.Builder
	for _, chunk := range m.streamChunks {
		if onToken != nil {
			onToken(chunk)
		}
		accumulated.WriteString(chunk)
	}
	return engine.CompletionResult{Output: accumulated.String(), InputTokens: 10, OutputTokens: 5}, m.completeError
}

func (m *mockEngine) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return nil, nil
}

func (m *mockEngine) Name() string {
	return m.name
}

func (m *mockEngine) ChatWithTools(ctx context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	return engine.ChatResponse{}, nil
}

func TestOpenModelProvider_Process(t *testing.T) {
	eng := &mockEngine{
		name:           "mock",
		completeResult: "success",
	}
	provider := legacymodels.NewOpenModelProvider(eng, "test-model")

	resp, err := provider.Process(context.Background(), &agent.Request{Input: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "success" {
		t.Errorf("expected 'success', got %q", resp.Output)
	}
}

func TestOpenModelProvider_ProcessStream(t *testing.T) {
	eng := &mockEngine{
		name:         "mock",
		streamChunks: []string{"a", "b", "c"},
	}
	provider := legacymodels.NewOpenModelProvider(eng, "test-model")

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

func TestOpenModelProvider_SetModelName(t *testing.T) {
	eng := &mockEngine{name: "mock"}
	provider := legacymodels.NewOpenModelProvider(eng, "test-model")

	if provider.Name() != "test-model" {
		t.Errorf("expected 'test-model', got %q", provider.Name())
	}

	provider.SetModelName("new-model")
	if provider.Name() != "new-model" {
		t.Errorf("expected 'new-model', got %q", provider.Name())
	}
}

// Compaction summarization depends on Temperature surviving this hop: the
// summarizer pins greedy decoding for reproducibility, and a silently dropped
// override would reintroduce sampling variance without any test failing.
func TestOpenModelProvider_ForwardsTemperature(t *testing.T) {
	eng := &mockEngine{name: "mock", completeResult: "ok"}
	provider := legacymodels.NewOpenModelProvider(eng, "test-model")

	zero := 0.0
	if _, err := provider.Process(context.Background(), &agent.Request{Input: "x", Temperature: &zero}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.gotOpts.Temperature == nil || *eng.gotOpts.Temperature != 0 {
		t.Fatalf("engine temperature = %v, want pointer to 0", eng.gotOpts.Temperature)
	}

	if _, err := provider.Process(context.Background(), &agent.Request{Input: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.gotOpts.Temperature != nil {
		t.Fatalf("engine temperature = %v, want nil (engine default)", *eng.gotOpts.Temperature)
	}
}
