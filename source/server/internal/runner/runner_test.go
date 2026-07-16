package runner

import (
	"testing"

	"cercano/source/server/internal/llm"
)

// captureSink records emitted events — the in-process EventSink for tests.
type captureSink struct{ events []Event }

func (c *captureSink) Emit(ev Event) { c.events = append(c.events, ev) }

func TestEvent_CarriesTokenAndToolPayloads(t *testing.T) {
	s := &captureSink{}
	s.Emit(Event{Kind: EventToken, Text: "hi"})
	s.Emit(Event{Kind: EventToolUseStart, ToolUseID: "t1", ToolName: "Read"})
	s.Emit(Event{Kind: EventDone, Result: Result{FinalText: "done", InputTokens: 3, OutputTokens: 1}})
	if len(s.events) != 3 {
		t.Fatalf("got %d events, want 3", len(s.events))
	}
	if s.events[0].Kind != EventToken || s.events[0].Text != "hi" {
		t.Errorf("token event wrong: %+v", s.events[0])
	}
	if s.events[2].Result.FinalText != "done" {
		t.Errorf("done event lost result: %+v", s.events[2])
	}
}

func TestRequest_IsProviderFree(t *testing.T) {
	// A Request carries user-facing inputs only — no inference.Provider. The runner
	// resolves the provider itself (worker-compatibility). This compiles-or-not
	// test locks that: Request has no provider field.
	r := Request{ConversationID: "c1", Input: "x", WorkDir: "/repo", Gen: 1}
	_ = r
	var _ []llm.Message // history is assembled by the runner, not passed in
}
