package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// mockStreamingProvider implements StreamingModelProvider for testing.
type mockStreamingProvider struct {
	name   string
	tokens []string
}

func (m *mockStreamingProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: strings.Join(m.tokens, "")}, nil
}
func (m *mockStreamingProvider) Name() string { return m.name }
func (m *mockStreamingProvider) ProcessStream(ctx context.Context, req *Request, onToken TokenFunc) (*Response, error) {
	var accumulated strings.Builder
	for _, tok := range m.tokens {
		accumulated.WriteString(tok)
		if onToken != nil {
			onToken(tok)
		}
	}
	return &Response{Output: accumulated.String()}, nil
}

func TestStreamingModelProvider_InterfaceSatisfaction(t *testing.T) {
	var _ StreamingModelProvider = &mockStreamingProvider{}
}

func TestStreamingModelProvider_TokenOrdering(t *testing.T) {
	provider := &mockStreamingProvider{
		name:   "test",
		tokens: []string{"Hello", " ", "world", "!"},
	}

	var received []string
	resp, err := provider.ProcessStream(context.Background(), &Request{Input: "hi"}, func(token string) {
		received = append(received, token)
	})

	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}
	if len(received) != 4 {
		t.Fatalf("expected 4 tokens, got %d", len(received))
	}
	for i, expected := range []string{"Hello", " ", "world", "!"} {
		if received[i] != expected {
			t.Errorf("token %d: expected %q, got %q", i, expected, received[i])
		}
	}
	if resp.Output != "Hello world!" {
		t.Errorf("expected accumulated output %q, got %q", "Hello world!", resp.Output)
	}
}

func TestMapEventToProgress_NilEvent(t *testing.T) {
	if got := MapEventToProgress(nil); got != "" {
		t.Errorf("expected empty string for nil event, got %q", got)
	}
}

func TestMapEventToProgress_GeneratorAuthor(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "generator"
	ev.LLMResponse.Content = genai.NewContentFromText("some code", genai.RoleModel)

	if got := MapEventToProgress(ev); got != "Generating code..." {
		t.Errorf("expected %q, got %q", "Generating code...", got)
	}
}

func TestMapEventToProgress_ValidatorSuccess(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "validator"
	ev.Actions.Escalate = true

	if got := MapEventToProgress(ev); got != "Validation passed." {
		t.Errorf("expected %q, got %q", "Validation passed.", got)
	}
}

func TestMapEventToProgress_ValidatorFailure(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "validator"
	ev.Actions.Escalate = false

	if got := MapEventToProgress(ev); got != "Validation failed. Retrying..." {
		t.Errorf("expected %q, got %q", "Validation failed. Retrying...", got)
	}
}

func TestMapEventToProgress_UnknownAuthor(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "unknown_agent"

	if got := MapEventToProgress(ev); got != "" {
		t.Errorf("expected empty string for unknown author, got %q", got)
	}
}
