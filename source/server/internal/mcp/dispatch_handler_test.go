package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/adk/session"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type dispatchFakeEngine struct {
	responses []engine.ChatResponse
}

func (d *dispatchFakeEngine) Complete(context.Context, string, string, string) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (d *dispatchFakeEngine) CompleteStream(context.Context, string, string, string, func(string)) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (d *dispatchFakeEngine) ListModels(context.Context) ([]engine.ModelInfo, error) {
	return nil, nil
}
func (d *dispatchFakeEngine) Name() string { return "fake" }
func (d *dispatchFakeEngine) ChatWithTools(_ context.Context, _ engine.ChatRequest) (engine.ChatResponse, error) {
	if len(d.responses) == 0 {
		return engine.ChatResponse{}, errors.New("no scripted response")
	}
	r := d.responses[0]
	d.responses = d.responses[1:]
	return r, nil
}

func newServerWithDispatch(t *testing.T, eng engine.InferenceEngine) *Server {
	t.Helper()
	mock := &mockAgentClient{}
	s := NewServer(mock)
	loop := dispatch.NewLoop(eng, capabilities.NewRegistry(capabilities.Services{}), []string{}, "qwen3-coder", 50)
	store := dispatch.NewStore(session.InMemoryService(), 100)
	s.SetDispatch(loop, store)
	return s
}

func TestHandleDispatch_TextOnlyResponse(t *testing.T) {
	eng := &dispatchFakeEngine{responses: []engine.ChatResponse{{Content: "hello back"}}}
	s := newServerWithDispatch(t, eng)

	resp, _, err := s.handleDispatch(context.Background(), &gomcp.CallToolRequest{}, DispatchRequest{
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("nil result")
	}
	if resp.IsError {
		t.Fatalf("unexpected error result: %+v", resp.Content)
	}
	body := contentText(t, resp)
	if !strings.Contains(body, "hello back") {
		t.Errorf("result = %q, want it to contain 'hello back'", body)
	}
	if !strings.Contains(body, `"turns": 1`) {
		t.Errorf("result = %q, want it to contain 'turns: 1' summary", body)
	}
	if !strings.Contains(body, `"cancelled": false`) {
		t.Errorf("result = %q, want it to contain cancelled:false", body)
	}
}

func TestHandleDispatch_NotConfiguredReturnsError(t *testing.T) {
	mock := &mockAgentClient{}
	s := NewServer(mock) // no SetDispatch
	resp, _, err := s.handleDispatch(context.Background(), &gomcp.CallToolRequest{}, DispatchRequest{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.IsError {
		t.Fatalf("expected error result, got %+v", resp)
	}
	if !strings.Contains(contentText(t, resp), "not configured") {
		t.Errorf("expected 'not configured' message, got %q", contentText(t, resp))
	}
}

func TestHandleDispatch_PersistsHistoryAcrossCalls(t *testing.T) {
	eng := &dispatchFakeEngine{responses: []engine.ChatResponse{
		{Content: "first reply"},
		{Content: "second reply"},
	}}
	s := newServerWithDispatch(t, eng)

	_, _, err := s.handleDispatch(context.Background(), &gomcp.CallToolRequest{}, DispatchRequest{
		Prompt:         "first turn",
		ConversationID: "smoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	hist, err := s.dispatchStore.Load(context.Background(), "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("after call 1, hist len = %d, want 2 (user + assistant)", len(hist))
	}

	_, _, err = s.handleDispatch(context.Background(), &gomcp.CallToolRequest{}, DispatchRequest{
		Prompt:         "second turn",
		ConversationID: "smoke",
	})
	if err != nil {
		t.Fatal(err)
	}
	hist, _ = s.dispatchStore.Load(context.Background(), "smoke")
	if len(hist) != 4 {
		t.Fatalf("after call 2, hist len = %d, want 4 (2 user + 2 assistant)", len(hist))
	}
}

func contentText(t *testing.T, r *gomcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// helps the linter recognize json/_ used in test file imports
var _ = json.Marshal
