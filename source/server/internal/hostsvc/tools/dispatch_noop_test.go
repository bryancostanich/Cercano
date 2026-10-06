package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/conversation"
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

// TestRunAgenticDispatch_EmitsDoneLastAfterWarnings records the sub-agent
// progress-event sequence for a dispatch that trips BOTH the suspicious-noop
// validation and dispatch-history persistence write failures. The terminal
// event must be "done": emitted after the suspicious-noop error and after the
// "Dispatch evidence incomplete" warning, so the parent/UI closes the
// sub-agent tab on the true final state instead of before the warnings land.
func TestRunAgenticDispatch_EmitsDoneLastAfterWarnings(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	logPath := installTestFailureLog(t, svc)
	svc.SetRegistry(regWith(permStub{"Edit", agenttools.PermW}, permStub{"Glob", agenttools.PermR}))

	// Every dispatch-history write fails: the deferred epilogue must emit the
	// "Dispatch evidence incomplete" warning and log dispatch_persistence_failed.
	svc.dispatchEventSink = func(context.Context, conversation.DispatchEvent) error {
		return errors.New("db down")
	}
	// Persistence itself must still run: the row is created (worker-style
	// proxy) and turns are recorded even though the evidence sink fails.
	var persistedTurns int
	svc.SetEnsureSubagent(func(context.Context, string, string, string, string, []string) error { return nil })
	svc.persistTurn = func(context.Context, string, llm.Message) { persistedTurns++ }

	var events []agenttools.ProgressEvent
	res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Edit the file.",
		Tools:          []string{"Edit", "Glob"},
		MaxIterations:  1,
		ConversationID: "parent",
		Emit:           func(ev agenttools.ProgressEvent) { events = append(events, ev) },
	}, inference.Selection{Provider: textOnlyProvider{}}, "test-model")
	if err == nil || !strings.Contains(err.Error(), "sub-agent failed validation") {
		t.Fatalf("expected suspicious no-op validation error, got %v", err)
	}
	if !res.Suspicious {
		t.Fatalf("result should preserve suspicion fields for diagnostics: %+v", res)
	}
	if !strings.Contains(res.Text, "[Dispatch evidence incomplete: 4 database writes failed.]") {
		t.Fatalf("result text missing persistence warning, got %q", res.Text)
	}
	if persistedTurns < 2 { // user task + assistant turn
		t.Fatalf("persistence should still record turns despite evidence-sink failure, got %d", persistedTurns)
	}

	var kinds []string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != "done" {
		t.Fatalf("done must be the last emitted event, got %v", kinds)
	}
	doneIdx := len(kinds) - 1
	var suspIdx, warnIdx = -1, -1
	for i, ev := range events {
		if ev.Kind != "error" {
			continue
		}
		if strings.HasPrefix(ev.Text, "sub-agent failed validation:") && suspIdx == -1 {
			suspIdx = i
		}
		if strings.Contains(ev.Text, "Dispatch evidence incomplete") && warnIdx == -1 {
			warnIdx = i
		}
	}
	if suspIdx == -1 || suspIdx >= doneIdx {
		t.Fatalf("suspicious-noop error event must precede done: susp=%d done=%d in %v", suspIdx, doneIdx, kinds)
	}
	if warnIdx == -1 || warnIdx >= doneIdx {
		t.Fatalf("persistence warning event must precede done: warn=%d done=%d in %v", warnIdx, doneIdx, kinds)
	}
	if done := events[doneIdx]; !strings.Contains(done.Text, "sub-agent done: conv=") {
		t.Fatalf("terminal event should be a done event, got %+v", done)
	} else if !strings.Contains(done.Text, "suspicious") {
		t.Fatalf("terminal done should carry the suspicious outcome, got %q", done.Text)
	}

	logData := readFailureLog(t, logPath)
	for _, want := range []string{
		"\"error_class\":\"suspicious_noop\"",
		"\"error_class\":\"dispatch_persistence_failed\"",
		"\"failed_writes\":4",
	} {
		if !strings.Contains(logData, want) {
			t.Fatalf("failure log missing %s: %s", want, logData)
		}
	}
}

// TestRunAgenticDispatch_EmitsDoneLastOnCleanSuccess pins the healthy path:
// a read-only, persisted-success dispatch still ends its event sequence on
// "done" (started → prompt → token → done), with nothing emitted after it.
func TestRunAgenticDispatch_EmitsDoneLastOnCleanSuccess(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	svc.SetRegistry(regWith(permStub{"Glob", agenttools.PermR}))

	var events []agenttools.ProgressEvent
	res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Inspect the repo.",
		Tools:          []string{"Glob"},
		MaxIterations:  1,
		ConversationID: "parent",
		Emit:           func(ev agenttools.ProgressEvent) { events = append(events, ev) },
	}, inference.Selection{Provider: textOnlyProvider{}}, "test-model")
	if err != nil {
		t.Fatalf("clean read-only dispatch should succeed, got %v", err)
	}
	if res.Suspicious || res.Text != "Done." {
		t.Fatalf("unexpected result: %+v", res)
	}
	var kinds []string
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"started", "prompt", "token", "done"}
	if len(kinds) != len(want) || kinds[len(kinds)-1] != "done" {
		t.Fatalf("clean dispatch should end on done, got %v", kinds)
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

type failingBudgetProvider struct{}

func (p failingBudgetProvider) Name() string { return "openai-responses" }
func (p failingBudgetProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p failingBudgetProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, &llm.Error{Class: llm.ErrContextOverflow, Provider: "openai-responses", Err: errors.New("context overflow")}
}
func (p failingBudgetProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, &llm.Error{Class: llm.ErrContextOverflow, Provider: "openai-responses", Err: errors.New("context overflow")}
}

func TestRunAgenticDispatch_ToolLoopFailureLogsRequestBudget(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	logPath := installTestFailureLog(t, svc)
	svc.SetRegistry(regWith(permStub{"Glob", agenttools.PermR}))
	svc.SetContextWindowResolver(func(string, bool) int { return 32768 })

	_, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Inspect a secret task.",
		Tools:          []string{"Glob"},
		MaxIterations:  1,
		ConversationID: "parent-budget",
	}, inference.Selection{Provider: failingBudgetProvider{}, IsCloud: true}, "gpt-5")
	if err == nil {
		t.Fatal("expected dispatch tool loop failure")
	}

	logData := readFailureLog(t, logPath)
	for _, want := range []string{
		`"event":"dispatch.tool_loop_failed"`,
		`"conversation_id":"parent-budget"`,
		`"provider":"openai-responses"`,
		`"error_class":"context_overflow"`,
		`"system_tokens":`,
		`"message_tokens":`,
		`"tool_schema_tokens":`,
		`"output_reserve":`,
		`"estimated_total_request_tokens":`,
		`"context_window":32768`,
	} {
		if !strings.Contains(logData, want) {
			t.Fatalf("failure log missing %s: %s", want, logData)
		}
	}
	if strings.Contains(logData, "Inspect a secret task") {
		t.Fatalf("failure log included dispatch task text: %s", logData)
	}
}
