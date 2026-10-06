package resilience

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// This file pins the tool-only buffering contract of the stream reader:
// tool-call events are HELD until the response completes (or commits with
// real content), so a retryable error that kills the stream after tool-only
// output discards the pending fragments and re-serves the SAME request under
// the existing retry/failover policy. Delivered text/reasoning still close
// the gate; ordering of the flushed tool events must be exact.

// toolOnlyEvents returns a complete, ordered tool-call response fragment.
func toolOnlyEvents(id string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: id, ToolName: "get_weather"},
		{Type: llm.EventToolUseInputDelta, TextDelta: `{"city":"SF"}`},
		{Type: llm.EventToolUseStop},
	}
}

func toolStarts(evs []llm.StreamEvent) []string {
	var ids []string
	for _, ev := range evs {
		if ev.Type == llm.EventToolUseStart {
			ids = append(ids, ev.ToolUseID)
		}
	}
	return ids
}

// gateEvent returns the first recorded engine decision of the given action.
func gateEvent(events *[]Event, action Action) Event {
	for _, ev := range *events {
		if ev.Action == action {
			return ev
		}
	}
	return Event{}
}

// Regression: the primary streams a COMPLETE tool call and then the transport
// resets. The old reader latched `emitted` on the first tool_use_start, so the
// reset surfaced to the user instead of retrying — the partially shown tool
// indicator would have been leaked. The engine must retry the SAME provider
// once and expose only the successful attempt's tool call.
func TestStream_NetworkAfterCompleteToolCallRetriesSameProvider(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		if primary.calls == 1 {
			return &fakeStream{events: toolOnlyEvents("call-dead"), err: networkErr("anthropic")}, nil
		}
		events := append(toolOnlyEvents("call-live"),
			llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "tool_use"})
		return &fakeStream{events: events}, nil
	}
	p, events, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v — a reset after tool-only output must retry, not surface", err)
	}
	if primary.calls != 2 {
		t.Errorf("primary calls = %d, want exactly one same-provider retry", primary.calls)
	}
	if len(*slept) != 1 {
		t.Errorf("slept = %v, want exactly one retry wait", *slept)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry || (*events)[0].Class != llm.ErrNetwork {
		t.Errorf("events = %+v, want one narrated network retry", *events)
	}
	if got := toolStarts(evs); len(got) != 1 || got[0] != "call-live" {
		t.Fatalf("tool_use_start ids = %v, want only the retry's call-live (dead fragments discarded)", got)
	}
}

// A busy reset can arrive after only PART of the tool input streamed. The
// buffered fragments must be discarded the same way.
func TestStream_BusyAfterPartialToolDeltaRetriesSameProvider(t *testing.T) {
	primary := &fakeProvider{name: "openai"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		if primary.calls == 1 {
			return &fakeStream{events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventToolUseStart, ToolUseID: "call-dead", ToolName: "get_weather"},
				{Type: llm.EventToolUseInputDelta, TextDelta: `{"ci`},
			}, err: busyErr("openai", time.Millisecond)}, nil
		}
		events := append(toolOnlyEvents("call-live"),
			llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "tool_use"})
		return &fakeStream{events: events}, nil
	}
	p, events, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v — busy after a partial tool fragment must retry, not surface", err)
	}
	if primary.calls != 2 || len(*slept) != 1 {
		t.Errorf("calls=%d sleeps=%v, want one retry after one wait", primary.calls, *slept)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry || (*events)[0].Class != llm.ErrBusy {
		t.Errorf("events = %+v, want one narrated busy retry", *events)
	}
	if got := toolStarts(evs); len(got) != 1 || got[0] != "call-live" {
		t.Fatalf("tool_use_start ids = %v, want only the retry's call-live", got)
	}
	for _, ev := range evs {
		if ev.TextDelta == `{"ci` {
			t.Error("partial input fragment from the dead attempt leaked to the consumer")
		}
	}
}

// When the error is ultimately surfaced (retry exhausted, no backup), the
// buffered tool fragments must be discarded — never delivered — so nothing
// leaks alongside the surfaced error.
func TestStream_RetryExhaustionAfterToolOnlyOutputSurfacesWithoutLeaking(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		return &fakeStream{events: toolOnlyEvents("call-dead"), err: busyErr("anthropic", time.Millisecond)}, nil
	}
	p, events, _ := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err == nil || llm.ClassOf(err) != llm.ErrBusy {
		t.Fatalf("want surfaced busy after retry exhaustion, got evs=%+v err=%v", evs, err)
	}
	if primary.calls != 2 {
		t.Errorf("primary calls = %d, want the one allowed retry then surface", primary.calls)
	}
	if got := toolStarts(evs); len(got) != 0 {
		t.Errorf("tool_use_start ids = %v, want none — surfaced errors must not leak tool fragments", got)
	}
	for _, ev := range evs {
		if ev.Type == llm.EventToolUseInputDelta || ev.Type == llm.EventToolUseStop ||
			ev.Type == llm.EventMessageStart {
			t.Errorf("event %+v from the dead attempts leaked to the consumer", ev)
		}
	}
	if len(*events) != 2 || (*events)[1].Action != ActionSurface {
		t.Errorf("events = %+v, want retry then surface", *events)
	}
}

// Context cancellation stays a hard stop even with tool events buffered: no
// retry, no failover, surfaced as cancellation.
func TestStream_CancelAfterToolOnlyOutputNeverRecovers(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	backup := &fakeProvider{name: "openai"}
	ctx, cancel := context.WithCancel(context.Background())
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		cancel() // the user kills the turn while tool events are still buffered
		return &fakeStream{events: toolOnlyEvents("call-dead"), err: networkErr("anthropic")}, nil
	}
	p, events, slept := build(primary, backup)

	r, err := p.StreamChat(ctx, inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err == nil {
		t.Fatal("cancelled stream must surface an error")
	}
	if primary.calls != 1 || backup.calls != 0 || len(*slept) != 0 {
		t.Errorf("calls primary=%d backup=%d sleeps=%v — cancellation must recover nothing", primary.calls, backup.calls, *slept)
	}
	if got := toolStarts(evs); len(got) != 0 {
		t.Errorf("tool_use_start ids = %v, want none leaked on cancellation", got)
	}
	se := gateEvent(events, ActionSurface)
	if se.Reason != ReasonCancellation {
		t.Errorf("surface reason = %q, want %q", se.Reason, ReasonCancellation)
	}
}

// A successful tool-only stream must come out in exactly the adapter's order
// once the buffer flushes at completion.
func TestStream_ToolOnlySuccessPreservesEventOrder(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		events := append(toolOnlyEvents("call-1"),
			llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "tool_use"})
		return &fakeStream{events: events}, nil
	}
	p, events, _ := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}
	wantTypes := []llm.StreamEventType{
		llm.EventMessageStart, llm.EventToolUseStart, llm.EventToolUseInputDelta,
		llm.EventToolUseStop, llm.EventMessageStop,
	}
	if len(evs) != len(wantTypes) {
		t.Fatalf("events = %+v, want %d events in order", evs, len(wantTypes))
	}
	for i, want := range wantTypes {
		if evs[i].Type != want {
			t.Errorf("evs[%d] = %s, want %s", i, evs[i].Type, want)
		}
	}
	if evs[len(evs)-1].StopReason != "tool_use" {
		t.Errorf("message_stop stop_reason = %q, want tool_use", evs[len(evs)-1].StopReason)
	}
	if len(*events) != 0 {
		t.Errorf("a successful stream must emit no gate events, got %+v", *events)
	}
	if primary.calls != 1 {
		t.Errorf("primary calls = %d, want 1", primary.calls)
	}
}

// Mixed response: tool calls stream FIRST, then text. The buffered tool events
// must flush ahead of the text in order, and the delivered text closes the
// retry gate — a later error surfaces, it does not retry.
func TestStream_ToolThenTextCommitsAndBlocksRetry(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	backup := &fakeProvider{name: "openai"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolUseID: "call-1", ToolName: "get_weather"},
			{Type: llm.EventToolUseInputDelta, TextDelta: `{}`},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventTextDelta, TextDelta: "checking the weather"},
		}, err: networkErr("anthropic")}, nil
	}
	p, events, slept := build(primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err == nil {
		t.Fatal("delivered text must close the retry gate — wanted surfaced network error")
	}
	if primary.calls != 1 || backup.calls != 0 || len(*slept) != 0 {
		t.Errorf("calls primary=%d backup=%d sleeps=%v, want no recovery after text", primary.calls, backup.calls, *slept)
	}
	if len(evs) != 5 {
		t.Fatalf("events = %+v, want the 4 pre-text events + text delta, nothing else", evs)
	}
	wantTypes := []llm.StreamEventType{
		llm.EventMessageStart, llm.EventToolUseStart, llm.EventToolUseInputDelta,
		llm.EventToolUseStop, llm.EventTextDelta,
	}
	for i, want := range wantTypes {
		if evs[i].Type != want {
			t.Errorf("evs[%d] = %s, want %s (buffered tools must flush ahead of the text)", i, evs[i].Type, want)
		}
	}
	se := gateEvent(events, ActionSurface)
	if se.Reason != ReasonContentEmitted || !se.Emitted || !se.EmittedText || !se.EmittedToolCall {
		t.Errorf("surface gate = %+v, want content_emitted with text + tool kinds", se)
	}
}

// Mixed response with reasoning: buffered tool events flush ahead of the
// reasoning event, and delivered reasoning still closes the gate.
func TestStream_ToolThenReasoningCommitsAndBlocksRetry(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	backup := &fakeProvider{name: "openai"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolUseID: "call-1", ToolName: "get_weather"},
			{Type: llm.EventToolUseInputDelta, TextDelta: `{}`},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventReasoning, ReasoningID: "rs_1", ReasoningData: "opaque"},
		}, err: networkErr("anthropic")}, nil
	}
	p, events, slept := build(primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err == nil {
		t.Fatal("delivered reasoning must close the retry gate — wanted surfaced network error")
	}
	if primary.calls != 1 || backup.calls != 0 || len(*slept) != 0 {
		t.Errorf("calls primary=%d backup=%d sleeps=%v, want no recovery after reasoning", primary.calls, backup.calls, *slept)
	}
	if len(evs) != 5 || evs[4].Type != llm.EventReasoning {
		t.Fatalf("events = %+v, want buffered tools then the reasoning event", evs)
	}
	if evs[1].ToolUseID != "call-1" {
		t.Errorf("evs[1] = %+v, want the buffered tool_use_start flushed first", evs[1])
	}
	se := gateEvent(events, ActionSurface)
	if se.Reason != ReasonContentEmitted || !se.Emitted || !se.EmittedReasoning || !se.EmittedToolCall {
		t.Errorf("surface gate = %+v, want content_emitted with reasoning + tool kinds", se)
	}
}
