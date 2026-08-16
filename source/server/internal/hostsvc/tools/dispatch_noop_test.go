package tools

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

type textOnlyProvider struct{}

func (p textOnlyProvider) Name() string { return "text-only" }
func (p textOnlyProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p textOnlyProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p textOnlyProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return &sliceStream{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "Done."},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}}, nil
}

type sliceStream struct {
	events []llm.StreamEvent
	idx    int
}

func (s *sliceStream) Next() (llm.StreamEvent, bool, error) {
	if s.idx >= len(s.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, true, nil
}
func (s *sliceStream) Close() error { return nil }

func TestRunAgenticDispatch_WriteGrantNoMutatingCallReturnsError(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	svc.SetRegistry(regWith(permStub{"Edit", agenttools.PermW}, permStub{"Glob", agenttools.PermR}))

	res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Edit the file.",
		Tools:          []string{"Edit", "Glob"},
		MaxIterations:  1,
		ConversationID: "parent",
	}, inference.Selection{Provider: textOnlyProvider{}}, "test-model")
	if err == nil {
		t.Fatal("expected suspicious write/execute no-op dispatch to return an error")
	}
	if !strings.Contains(err.Error(), "sub-agent failed validation") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !res.Suspicious {
		t.Fatalf("result should preserve suspicion fields for diagnostics: %+v", res)
	}
	if !strings.Contains(res.SuspicionReason, "Edit") {
		t.Fatalf("suspicion reason should name unused write tool, got %q", res.SuspicionReason)
	}
}

func TestRunAgenticDispatch_ReadOnlyLowSignalStillSucceeds(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	svc.SetRegistry(regWith(permStub{"Glob", agenttools.PermR}))

	res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Inspect the repo.",
		Tools:          []string{"Glob"},
		MaxIterations:  1,
		ConversationID: "parent",
	}, inference.Selection{Provider: textOnlyProvider{}}, "test-model")
	if err != nil {
		t.Fatalf("read-only low-signal dispatch should remain advisory/successful, got %v", err)
	}
	if res.Suspicious {
		t.Fatalf("read-only low-signal dispatch should not be suspicious: %+v", res)
	}
	if res.Text != "Done." {
		t.Fatalf("result text = %q", res.Text)
	}
}
