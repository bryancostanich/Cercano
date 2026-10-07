package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// This file pins the retry-gate decision LOG: every recovery gate — open or
// closed — records its action, the precise reason it closed, and the failed
// request's conversation/iteration identity through OnEvent. The retry
// semantics themselves (call counts, sleeps, failover steps) are asserted
// unchanged alongside every reason.

func invalidRequestErr(provider string) error {
	return &llm.Error{Class: llm.ErrInvalidRequest, Provider: provider,
		StatusCode: 400, Err: errors.New("bad request")}
}

// findGate returns the first event with the given action.
func findGate(t *testing.T, events *[]Event, action Action) Event {
	t.Helper()
	for _, ev := range *events {
		if ev.Action == action {
			return ev
		}
	}
	t.Fatalf("no %s event in %+v", action, *events)
	return Event{}
}

func TestGateLogChatRetryAttemptedCarriesRequestIdentity(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{busyErr("primary", time.Millisecond), nil}}
	p, events, sleeps := build(primary, nil)
	resp, err := p.Chat(context.Background(), inference.Call{ConversationID: "c1", RequestID: "c1:2"})
	if err != nil || resp.Model != "primary" || primary.calls != 2 || len(*sleeps) != 1 {
		t.Fatalf("retry semantics changed: resp=%v err=%v calls=%d sleeps=%v", resp, err, primary.calls, *sleeps)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %+v, want one retry event", *events)
	}
	ev := (*events)[0]
	if ev.Action != ActionRetry || ev.Reason != "" || ev.Stage != "chat" {
		t.Fatalf("retry gate event = %+v, want attempted retry with empty reason", ev)
	}
	if ev.ConversationID != "c1" || ev.RequestID != "c1:2" {
		t.Fatalf("retry gate event not correlated: %+v", ev)
	}
	if ev.Emitted || ev.EmittedText || ev.EmittedReasoning || ev.EmittedToolCall {
		t.Fatalf("chat gate event must never report emitted content: %+v", ev)
	}
}

func TestGateLogChatFailoverCarriesRetrySkipReason(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{busyErr("primary", time.Millisecond), busyErr("primary", time.Millisecond)}}
	backup := &fakeProvider{name: "backup", outcome: []error{nil}}
	p, events, _ := build(primary, backup)
	if _, err := p.Chat(context.Background(), inference.Call{}); err != nil {
		t.Fatal(err)
	}
	if primary.calls != 2 || backup.calls != 1 {
		t.Fatalf("retry semantics changed: primary=%d backup=%d", primary.calls, backup.calls)
	}
	if len(*events) != 2 {
		t.Fatalf("events = %+v, want retry then failover", *events)
	}
	if (*events)[0].Action != ActionRetry || (*events)[0].Reason != "" {
		t.Fatalf("retry event = %+v", (*events)[0])
	}
	fe := findGate(t, events, ActionFailover)
	if fe.Reason != ReasonRetryLimit {
		t.Fatalf("failover reason = %q, want %q (the one busy retry was already spent)", fe.Reason, ReasonRetryLimit)
	}
}

func TestGateLogChatQuotaFailoverNamesNonretryableRetryGate(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{quotaErr("primary")}}
	backup := &fakeProvider{name: "backup", outcome: []error{nil}}
	p, events, _ := build(primary, backup)
	if _, err := p.Chat(context.Background(), inference.Call{}); err != nil {
		t.Fatal(err)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("retry semantics changed: primary=%d backup=%d", primary.calls, backup.calls)
	}
	fe := findGate(t, events, ActionFailover)
	if fe.Reason != ReasonNonretryable {
		t.Fatalf("quota failover reason = %q, want %q", fe.Reason, ReasonNonretryable)
	}
}

func TestGateLogChatSurfaceJoinsRetryAndFailoverSkipReasons(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{invalidRequestErr("primary")}}
	backup := &fakeProvider{name: "backup"}
	p, events, sleeps := build(primary, backup)
	if _, err := p.Chat(context.Background(), inference.Call{}); err == nil {
		t.Fatal("expected surfaced error")
	}
	if primary.calls != 1 || backup.calls != 0 || len(*sleeps) != 0 {
		t.Fatalf("retry semantics changed: primary=%d backup=%d sleeps=%v", primary.calls, backup.calls, *sleeps)
	}
	se := findGate(t, events, ActionSurface)
	if se.Reason != ReasonNonretryable+","+ReasonNotFailoverable {
		t.Fatalf("surface reason = %q, want %q,%q", se.Reason, ReasonNonretryable, ReasonNotFailoverable)
	}
}

func TestGateLogChatNoBackupSurfaceNamesBothGates(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{busyErr("primary", time.Millisecond), busyErr("primary", time.Millisecond)}}
	p, events, _ := build(primary, nil)
	if _, err := p.Chat(context.Background(), inference.Call{}); err == nil {
		t.Fatal("expected surfaced error")
	}
	if primary.calls != 2 || len(*events) != 2 {
		t.Fatalf("retry semantics changed: primary=%d events=%+v", primary.calls, *events)
	}
	se := findGate(t, events, ActionSurface)
	if se.Reason != ReasonRetryLimit+","+ReasonNoBackup {
		t.Fatalf("surface reason = %q, want %q,%q", se.Reason, ReasonRetryLimit, ReasonNoBackup)
	}
}

func TestGateLogChatCancellationSurfacesWithReason(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{quotaErr("primary")}}
	backup := &fakeProvider{name: "backup"}
	p, events, sleeps := build(primary, backup)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Chat(ctx, inference.Call{}); err == nil {
		t.Fatal("expected surfaced error")
	}
	if primary.calls != 1 || backup.calls != 0 || len(*sleeps) != 0 {
		t.Fatalf("retry semantics changed: primary=%d backup=%d sleeps=%v", primary.calls, backup.calls, *sleeps)
	}
	se := findGate(t, events, ActionSurface)
	if se.Reason != ReasonCancellation {
		t.Fatalf("surface reason = %q, want %q", se.Reason, ReasonCancellation)
	}
}

func TestGateLogStreamCommittedFailureNamesContentEmitted(t *testing.T) {
	stream := &fakeStream{events: []llm.StreamEvent{{Type: llm.EventTextDelta, TextDelta: "partial"}}, err: quotaErr("primary")}
	primary := &fakeProvider{name: "primary", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) { return stream, nil }}
	backup := &fakeProvider{name: "backup"}
	p, events, sleeps := build(primary, backup)
	r, err := p.StreamChat(context.Background(), inference.Call{ConversationID: "c9", RequestID: "c9:4"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := collectStream(t, r); err == nil {
		t.Fatal("expected surfaced error")
	}
	if backup.calls != 0 || len(*sleeps) != 0 {
		t.Fatalf("retry semantics changed: backup=%d sleeps=%v", backup.calls, *sleeps)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %+v, want the stream_live gate only", *events)
	}
	ev := (*events)[0]
	if ev.Action != ActionSurface || ev.Stage != "stream_live" || ev.Reason != ReasonContentEmitted {
		t.Fatalf("committed-stream gate = %+v", ev)
	}
	if !ev.Emitted || !ev.EmittedText || ev.EmittedReasoning || ev.EmittedToolCall {
		t.Fatalf("emitted kinds = %+v, want text only", ev)
	}
	if ev.ConversationID != "c9" || ev.RequestID != "c9:4" {
		t.Fatalf("gate event not correlated: %+v", ev)
	}
}

// Held pre-commit events become DELIVERED kinds only once the stream
// commits: this response ends cleanly (message_stop flushes the buffered
// reasoning + tool events) and the failure arrives on the NEXT read, so the
// committed-stream gate must name the reasoning + tool kinds, no text.
func TestGateLogStreamEmittedReasoningAndToolCallKinds(t *testing.T) {
	stream := &fakeStream{events: []llm.StreamEvent{
		{Type: llm.EventReasoning},
		{Type: llm.EventToolUseStart, ToolName: "noop"},
		{Type: llm.EventMessageStop},
	}, err: networkErr("primary")}
	primary := &fakeProvider{name: "primary", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) { return stream, nil }}
	p, events, _ := build(primary, nil)
	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := collectStream(t, r); err == nil {
		t.Fatal("expected surfaced error")
	}
	ev := findGate(t, events, ActionSurface)
	if !ev.EmittedReasoning || !ev.EmittedToolCall || ev.EmittedText {
		t.Fatalf("emitted kinds = %+v, want reasoning + tool call, no text", ev)
	}
}

func TestGateLogStreamFailoverNamesRetryGateThenCascade(t *testing.T) {
	primary := &fakeProvider{name: "primary", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) {
		return nil, quotaErr("primary")
	}}
	backup := &fakeProvider{name: "backup", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) {
		return nil, quotaErr("backup")
	}}
	p, events, _ := build(primary, backup)
	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := collectStream(t, r); err == nil {
		t.Fatal("expected surfaced error")
	}
	fe := findGate(t, events, ActionFailover)
	if fe.Reason != ReasonNonretryable {
		t.Fatalf("stream failover reason = %q, want %q", fe.Reason, ReasonNonretryable)
	}
	se := findGate(t, events, ActionSurface)
	if se.Reason != ReasonAlreadyFailedOver || se.Stage != "stream_dial" {
		t.Fatalf("cascade gate = %+v, want %q at stream_dial", se, ReasonAlreadyFailedOver)
	}
}

func TestGateLogStreamRetryCorrelatesViaSessionContext(t *testing.T) {
	attempts := 0
	stream := &fakeStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}}}
	primary := &fakeProvider{name: "primary", streamOverride: func(context.Context, inference.Call) (inference.Stream, error) {
		attempts++
		if attempts == 1 {
			return nil, busyErr("primary", time.Millisecond)
		}
		return stream, nil
	}}
	p, events, sleeps := build(primary, nil)
	ctx := llm.WithSessionID(context.Background(), "sess-42")
	r, err := p.StreamChat(ctx, inference.Call{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := collectStream(t, r); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(*sleeps) != 1 {
		t.Fatalf("retry semantics changed: attempts=%d sleeps=%v", attempts, *sleeps)
	}
	ev := findGate(t, events, ActionRetry)
	if ev.Reason != "" || ev.ConversationID != "sess-42" {
		t.Fatalf("stream retry gate = %+v, want attempted retry correlated by session id", ev)
	}
}
