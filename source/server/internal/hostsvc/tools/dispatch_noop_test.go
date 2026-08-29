package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/failurelog"
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
	logPath := installTestFailureLog(t, svc)
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
	logData := readFailureLog(t, logPath)
	for _, want := range []string{"\"event\":\"dispatch.degraded\"", "\"error_class\":\"suspicious_noop\"", "\"conversation_id\":\"parent\""} {
		if !strings.Contains(logData, want) {
			t.Fatalf("failure log missing %s: %s", want, logData)
		}
	}
	if strings.Contains(logData, "Edit the file.") {
		t.Fatalf("failure log included dispatch task text: %s", logData)
	}
}

func TestRunAgenticDispatch_ReadOnlyLowSignalStillSucceeds(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	logPath := installTestFailureLog(t, svc)
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
	logData := readFailureLog(t, logPath)
	for _, want := range []string{"\"event\":\"dispatch.degraded\"", "\"error_class\":\"low_signal\"", "\"conversation_id\":\"parent\""} {
		if !strings.Contains(logData, want) {
			t.Fatalf("failure log missing %s: %s", want, logData)
		}
	}
	if strings.Contains(logData, "Inspect the repo.") {
		t.Fatalf("failure log included dispatch task text: %s", logData)
	}
}

func installTestFailureLog(t *testing.T, svc *Service) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "failures.jsonl")
	w, err := failurelog.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	svc.SetFailureLog(w)
	return path
}

func readFailureLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failure log: %v", err)
	}
	return string(data)
}
