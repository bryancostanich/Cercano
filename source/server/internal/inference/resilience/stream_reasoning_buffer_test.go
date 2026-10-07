package resilience

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// This file pins the reasoning-holding contract of the stream reader:
// reasoning items are pre-commit content, like tool-call fragments. Delivered
// on arrival they latch the stream live, forcing a mid-stream failure to
// surface and leaking interrupted thinking to the consumer. Held, a
// retryable failure discards them with the rest of the dead attempt and
// re-serves the SAME request — no reasoning from a failed attempt ever
// reaches a consumer, so a retry can never concatenate interrupted thinking
// with the fresh attempt's output.

// Regression: the primary streams ONLY reasoning and then the transport
// resets. The old reader treated EventReasoning as committing content, so the
// reset surfaced instead of retrying. The engine must retry the SAME request
// and expose only the successful attempt's reasoning.
func TestStream_NetworkAfterReasoningOnlyRetriesSameProvider(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(_ context.Context, _ inference.Call) (inference.Stream, error) {
		primary.calls++
		if primary.calls == 1 {
			return &fakeStream{events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead-opaque"},
			}, err: networkErr("anthropic")}, nil
		}
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs_live", ReasoningData: "live-opaque"},
			{Type: llm.EventTextDelta, TextDelta: "the answer"},
			{Type: llm.EventMessageStop, StopReason: "end_turn"},
		}}, nil
	}
	p, events, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v — a reset after reasoning-only output must retry, not surface", err)
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
	if re := gateEvent(events, ActionRetry); re.Emitted || re.EmittedText || re.EmittedReasoning || re.EmittedToolCall {
		t.Errorf("retry gate = %+v, want the dead attempt recorded as fully undelivered", re)
	}
	var reasoningIDs []string
	for _, ev := range evs {
		if ev.Type == llm.EventReasoning {
			reasoningIDs = append(reasoningIDs, ev.ReasoningID)
		}
		if ev.ReasoningData == "dead-opaque" {
			t.Error("reasoning from the dead attempt leaked to the consumer")
		}
	}
	if len(reasoningIDs) != 1 || reasoningIDs[0] != "rs_live" {
		t.Fatalf("reasoning ids = %v, want only the retry's rs_live (dead attempt discarded)", reasoningIDs)
	}
}

// The dead attempt streamed reasoning AND unfinished tool-call data before
// going busy. All of it — interrupted thinking included — must be discarded
// with the dead attempt, never concatenated with the retry's output.
func TestStream_BusyAfterReasoningAndPartialToolDataRetriesDiscardsAll(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(_ context.Context, _ inference.Call) (inference.Stream, error) {
		primary.calls++
		if primary.calls == 1 {
			return &fakeStream{events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead-opaque"},
				{Type: llm.EventToolUseStart, ToolUseID: "call-dead", ToolName: "get_weather"},
				{Type: llm.EventToolUseInputDelta, TextDelta: `{"ci`},
			}, err: busyErr("anthropic", time.Millisecond)}, nil
		}
		return &fakeStream{events: append(toolOnlyEvents("call-live"), llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "tool_use"})}, nil
	}
	p, events, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}
	if primary.calls != 2 {
		t.Errorf("primary calls = %d, want exactly one same-provider retry", primary.calls)
	}
	if len(*slept) != 1 {
		t.Errorf("slept = %v, want exactly one retry wait", *slept)
	}
	if starts := toolStarts(evs); len(starts) != 1 || starts[0] != "call-live" {
		t.Fatalf("tool starts = %v, want only the retry's call-live", starts)
	}
	var reasoningIDs []string
	for _, ev := range evs {
		if ev.ReasoningID == "rs_dead" || ev.ReasoningData == "dead-opaque" || ev.ToolUseID == "call-dead" || ev.TextDelta == `{"ci` {
			t.Errorf("dead-attempt fragment leaked: %+v", ev)
		}
		if ev.Type == llm.EventReasoning {
			reasoningIDs = append(reasoningIDs, ev.ReasoningID)
		}
	}
	// The live attempt streams no reasoning item at all: none may leak
	// from the dead attempt (interrupted thinking discarded, not replayed).
	if len(reasoningIDs) != 0 {
		t.Errorf("reasoning ids = %v, want none — live attempt had none and dead attempt was discarded", reasoningIDs)
	}
	if re := gateEvent(events, ActionRetry); re.Emitted || re.EmittedReasoning || re.EmittedToolCall {
		t.Errorf("retry gate = %+v, want the dead attempt recorded as fully undelivered", re)
	}
}

// Successful reasoning-only output must stay preserved: a reasoning item
// buffered while the response completes is flushed once, in adapter order,
// with its opaque payload intact.
func TestStream_ReasoningOnlySuccessPreservesOrderAndPayload(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(_ context.Context, _ inference.Call) (inference.Stream, error) {
		primary.calls++
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs_1", ReasoningData: "opaque-1"},
			{Type: llm.EventReasoning, ReasoningID: "rs_2", ReasoningData: "opaque-2"},
			{Type: llm.EventMessageStop, StopReason: "end_turn"},
		}}, nil
	}
	p, events, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}
	if primary.calls != 1 || len(*slept) != 0 || len(*events) != 0 {
		t.Errorf("calls=%d slept=%v events=%v, want a clean single attempt with no gate decisions", primary.calls, *slept, *events)
	}
	wantTypes := []llm.StreamEventType{llm.EventMessageStart, llm.EventReasoning, llm.EventReasoning, llm.EventMessageStop}
	if len(evs) != len(wantTypes) {
		t.Fatalf("events = %+v, want %v in order", evs, wantTypes)
	}
	for i, want := range wantTypes {
		if evs[i].Type != want {
			t.Fatalf("evs[%d] = %+v, want %v (adapter order preserved)", i, evs[i], want)
		}
	}
	if evs[1].ReasoningID != "rs_1" || evs[1].ReasoningData != "opaque-1" || evs[2].ReasoningID != "rs_2" || evs[2].ReasoningData != "opaque-2" {
		t.Errorf("reasoning payloads damaged: %+v %+v", evs[1], evs[2])
	}
}

// Retry exhaustion after reasoning-only output: the surfaced busy error must
// carry no leaked framing, reasoning, or tool fragments from either dead
// attempt — only the narration.
func TestStream_RetryExhaustionAfterReasoningOnlySurfacesWithoutLeaking(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead-opaque"},
		}, err: busyErr("anthropic", time.Millisecond)}, nil
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
	for _, ev := range evs {
		if ev.Type == llm.EventReasoning || ev.Type == llm.EventMessageStart || ev.ReasoningData == "dead-opaque" {
			t.Errorf("event %+v from the dead attempts leaked to the consumer", ev)
		}
	}
	if len(*events) != 2 || (*events)[1].Action != ActionSurface {
		t.Errorf("events = %+v, want retry then surface", *events)
	}
	surface := (*events)[1]
	if surface.Emitted || surface.EmittedReasoning || surface.EmittedText || surface.EmittedToolCall {
		t.Errorf("surface gate = %+v, want nothing delivered", surface)
	}
}

// Cancellation after reasoning-only output: the reader never recovers from a
// user cancellation, and the dead attempt's reasoning stays discarded.
func TestStream_CancelAfterReasoningOnlyNeverRecovers(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	backup := &fakeProvider{name: "openai"}
	ctx, cancel := context.WithCancel(context.Background())
	primary.streamOverride = func(context.Context, inference.Call) (inference.Stream, error) {
		primary.calls++
		cancel() // the user kills the turn while reasoning is still buffered
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead-opaque"},
		}, err: networkErr("anthropic")}, nil
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
		t.Errorf("calls primary=%d backup=%d slept=%v — cancellation must recover nothing", primary.calls, backup.calls, *slept)
	}
	for _, ev := range evs {
		if ev.Type == llm.EventReasoning || ev.ReasoningData == "dead-opaque" {
			t.Errorf("event %+v from the dead attempt leaked to the consumer", ev)
		}
	}
	se := gateEvent(events, ActionSurface)
	if se.Reason != ReasonCancellation {
		t.Errorf("surface reason = %q, want %q", se.Reason, ReasonCancellation)
	}
}
