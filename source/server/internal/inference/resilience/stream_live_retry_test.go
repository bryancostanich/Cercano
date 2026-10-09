package resilience

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// liveRetry scripts a provider whose first stream dies with a retryable
// error AFTER visible text has been emitted mid-stream. The retry may then
// succeed, fail again mid-stream, or fail to dial. Requests are recorded so
// tests can assert the SAME request is re-served.
type liveRetry struct {
	mu        sync.Mutex
	name      string
	calls     int
	models    []string
	dialErr   func(call int) error // error returned by StreamChat dial for call N (1-based)
	stream    func(call int) (inference.Stream, error)
}

func (p *liveRetry) Name() string                         { return p.name }
func (p *liveRetry) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *liveRetry) Chat(context.Context, inference.Call) (inference.Result, error) {
	return inference.Result{}, errors.New("unused")
}
func (p *liveRetry) StreamChat(_ context.Context, req inference.Call) (inference.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.models = append(p.models, req.Model)
	if p.dialErr != nil {
		if err := p.dialErr(p.calls); err != nil {
			return nil, err
		}
	}
	return p.stream(p.calls)
}

func (p *liveRetry) snapshot() (calls int, models []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.models...)
}

// liveEngine wires a liveRetry primary with an event recorder and an
// order-observing sleep seam, plus an optional backup that must never fire.
func liveEngine(t *testing.T, primary *liveRetry, backup *fakeProvider) (*Provider, *[]Event, *[]string, *[]time.Duration) {
	t.Helper()
	var events []Event
	var order []string
	var slept []time.Duration
	opts := Options{OnEvent: func(e Event) {
		events = append(events, e)
		order = append(order, "event")
	}}
	if backup != nil {
		opts.Backup = backup
		opts.BackupModelFor = func(string) string { return "backup-model" }
	}
	p := New(primary, opts)
	p.sleep = func(_ context.Context, d time.Duration) bool {
		slept = append(slept, d)
		order = append(order, "sleep")
		return true
	}
	return p, &events, &order, &slept
}

func drain(t *testing.T, r inference.Stream) ([]llm.StreamEvent, []string, error) {
	t.Helper()
	var evs []llm.StreamEvent
	var notices []string
	for {
		ev, ok, err := r.Next()
		if err != nil {
			return evs, notices, err
		}
		if !ok {
			return evs, notices, nil
		}
		if ev.Type == llm.EventNotice {
			notices = append(notices, ev.Notice)
		} else {
			evs = append(evs, ev)
		}
	}
}

// The restarted attempt may open cleanly and then produce NOTHING — no
// framing, no content, no error — before ending. The synthetic message_start
// watch must still reset the consumer's accumulation at end-of-stream, or
// llm.CollectStream's terminal flush would persist the interrupted
// attempt's partial state (text, tool fragments, usage, route) as a
// successful response.
func TestStream_LiveRestartSilentEmptyRetryStillInjectsStartAndPersistsCleanResponse(t *testing.T) {
	script := func() *liveRetry {
		return &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
			if call == 1 {
				return &fakeStream{
					events: []llm.StreamEvent{
						{Type: llm.EventMessageStart},
						{Type: llm.EventTextDelta, TextDelta: "interrupted answer"},
					},
					err: networkErr("anthropic"),
				}, nil
			}
			// The retried attempt: zero frames, no error — a clean, silent EOF.
			return &fakeStream{}, nil
		}}
	}

	// Probe 1: the synthetic message_start must be delivered ahead of the
	// end-of-stream signal.
	primary := script()
	p, events, order, slept := liveEngine(t, primary, &fakeProvider{name: "openai"})
	p.primaryModelFor = func(string) string { return "anthropic-model" }
	r, err := p.StreamChat(context.Background(), inference.Call{Model: "anthropic-model"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "restarting the reply") {
		t.Fatalf("notices = %v, want the mid-answer restart notice", notices)
	}
	if !containsEvent(evs, llm.EventMessageStart, 2) {
		t.Fatalf("events = %v, want the fresh attempt's synthetic message_start delivered ahead of the end", eventTypes(evs))
	}
	primary.snapshot()
	calls, models := primary.snapshot()
	if calls != 2 || len(models) != 2 || models[0] != models[1] {
		t.Fatalf("calls = %d models = %v, want the same request served twice", calls, models)
	}
	if len(*slept) != 1 {
		t.Fatalf("slept = %v, want exactly one restart wait", *slept)
	}
	if len(*order) != 2 || (*order)[0] != "event" || (*order)[1] != "sleep" {
		t.Fatalf("order = %v, want the notice event ahead of the wait", *order)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry {
		t.Fatalf("events = %+v, want one retry gate", *events)
	}

	// Probe 2: end to end through llm.CollectStream — the persisted response
	// must be clean: no interrupted text, no error.
	p2, _, _, _ := liveEngine(t, script(), &fakeProvider{name: "openai"})
	p2.primaryModelFor = func(string) string { return "anthropic-model" }
	r2, err := p2.StreamChat(context.Background(), inference.Call{Model: "anthropic-model"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	out, err := llm.CollectStream(context.Background(), r2, nil, nil)
	if err != nil {
		t.Fatalf("CollectStream: %v", err)
	}
	for _, b := range out.Blocks {
		if b.Type == llm.BlockText && strings.Contains(b.Text, "interrupted answer") {
			t.Fatalf("interrupted text contaminated the persisted response: %q", b.Text)
		}
	}
	if len(out.Blocks) != 0 {
		t.Fatalf("persisted blocks = %+v, want none — the silent retry contributed nothing", out.Blocks)
	}
}

func containsEvent(evs []llm.StreamEvent, typ llm.StreamEventType, atLeast int) bool {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n >= atLeast
}

// Network error mid-stream AFTER visible text: same provider, same request,
// one restart with a user-visible interruption notice, no backup failover.
func TestStream_NetworkAfterVisibleTextRestartsSameProvider(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			return &fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart},
					{Type: llm.EventTextDelta, TextDelta: "interrupted answer"},
				},
				err: networkErr("anthropic"),
			}, nil
		}
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: "restarted"},
			{Type: llm.EventMessageStop},
		}}, nil
	}}
	backup := &fakeProvider{name: "openai"}
	p, events, order, slept := liveEngine(t, primary, backup)
	p.primaryModelFor = func(string) string { return "anthropic-model" }

	r, err := p.StreamChat(context.Background(), inference.Call{Model: "anthropic-model"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}

	// Visible stream: the interrupted text stays, a restart notice appears,
	// then the fresh answer arrives — no dead-attempt leakage, no duplicate
	// framing between attempts beyond the second message_start.
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "interrupted answerrestarted" {
		t.Errorf("visible text = %q, want %q", got, "interrupted answerrestarted")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "restarting the reply") {
		t.Errorf("notices = %+v, want one restart notice", notices)
	}

	// Engine telemetry: exactly one retry of the SAME provider with
	// Emitted/EmittedText true; the surface reason is content_emitted (the
	// gate that used to block recovery).
	ev := (*events)[0]
	if len(*events) != 1 || ev.Action != ActionRetry || ev.Class != llm.ErrNetwork ||
		!ev.Emitted || !ev.EmittedText || ev.Reason != ReasonContentEmitted {
		t.Errorf("events = %+v, want single retry with content_emitted gate", *events)
	}
	if backup.calls != 0 {
		t.Errorf("backup calls = %d, want 0 (live restart never fails over)", backup.calls)
	}
	if calls, models := primary.snapshot(); calls != 2 || len(models) != 2 || models[0] != models[1] {
		t.Errorf("primary calls=%d models=%v, want 2 identical same-request calls", calls, models)
	}
	if len(*slept) != 1 {
		t.Errorf("waits = %d, want 1", len(*slept))
	}
	if (*order)[0] != "event" || (*order)[1] != "sleep" {
		t.Errorf("order = %v, want event before sleep", *order)
	}
}

// Retryable busy error mid-stream AFTER visible text: restart with the notice
// decided before the retry wait elapses.
func TestStream_BusyErrorFrameAfterVisibleTextRestartsSameProvider(t *testing.T) {
	primary := &liveRetry{name: "openai", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			return &fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart},
					{Type: llm.EventTextDelta, TextDelta: "half"},
				},
				err: busyErr("openai", 2*time.Second),
			}, nil
		}
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: " full"},
			{Type: llm.EventMessageStop},
		}}, nil
	}}
	p, events, order, slept := liveEngine(t, primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "half full" {
		t.Errorf("visible text = %q, want %q", got, "half full")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "restarting the reply") {
		t.Errorf("notices = %+v, want one restart notice", notices)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry || (*events)[0].Class != llm.ErrBusy {
		t.Errorf("events = %+v, want single busy retry", *events)
	}
	if len(*slept) != 1 || (*slept)[0] != 2*time.Second {
		t.Errorf("waits = %+v, want one busy-retry wait honoring Retry-After", *slept)
	}
	if len(*order) != 2 || (*order)[0] != "event" || (*order)[1] != "sleep" {
		t.Errorf("order = %v, want notice decision before the wait", *order)
	}
}

// The live restart may itself die mid-stream: the original retryable error
// surfaces (not the second one), with no further restart and no backup
// failover. Both attempts' interrupted text remains visible.
func TestStream_LiveRestartExhaustionSurfacesWithoutFailover(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "cut"},
			},
			err: networkErr("anthropic"),
		}, nil
	}}
	backup := &fakeProvider{name: "openai"}
	p, events, _, slept := liveEngine(t, primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("second mid-stream failure must surface, not succeed")
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "cutcut" {
		t.Errorf("visible text = %q, want %q", got, "cutcut")
	}
	if len(notices) != 1 {
		t.Errorf("notices = %+v, want exactly one restart notice", notices)
	}
	if backup.calls != 0 {
		t.Errorf("backup calls = %d, want 0 (restart exhaustion never fails over)", backup.calls)
	}
	if calls, _ := primary.snapshot(); calls != 2 {
		t.Errorf("primary calls = %d, want 2 (original + restart, no loop)", calls)
	}
	// One retry event, then the surfaced failure is logged once with the
	// retry-limit reason (no recovery left) and the emitted-text flag.
	if len(*events) != 2 {
		t.Fatalf("events = %+v, want retry + surface log", *events)
	}
	if (*events)[0].Action != ActionRetry {
		t.Errorf("events[0] = %+v, want retry", (*events)[0])
	}
	if (*events)[1].Action != ActionSurface || (*events)[1].Reason != ReasonRetryLimit+","+ReasonContentEmitted ||
		!(*events)[1].Emitted || !(*events)[1].EmittedText {
		t.Errorf("events[1] = %+v, want post-retry surface with emitted text", (*events)[1])
	}
	if len(*slept) != 1 {
		t.Errorf("waits = %d, want 1 (only the first restart waits)", len(*slept))
	}
}

// A completed answer (message_stop delivered) followed by a transport error
// must NOT restart — the turn is already complete. The error still surfaces
// (swallowing it was never in scope): a completed message does not silence a
// broken transport, and the caller's collector/persist layer records a
// failure for the completed-but-unsettled stream.
func TestStream_CompletedAnswerThenTransportErrorNeverRestarts(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "done answer"},
				{Type: llm.EventMessageStop},
			},
			err: networkErr("anthropic"), // trailing error after message_stop
		}, nil
	}}
	p, events, _, slept := liveEngine(t, primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("trailing transport error after message_stop must surface")
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "done answer" {
		t.Errorf("visible text = %q, want only the completed answer", got)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %+v, want none after completion", notices)
	}
	if calls, _ := primary.snapshot(); calls != 1 {
		t.Errorf("primary calls = %d, want 1 (never restart a finished message)", calls)
	}
	if len(*slept) != 0 {
		t.Errorf("waits = %d, want 0", len(*slept))
	}
	if len(*events) != 1 || (*events)[0].Action != ActionSurface ||
		(*events)[0].Reason != ReasonContentEmitted || !(*events)[0].EmittedText {
		t.Errorf("events = %+v, want single content_emitted surface log, no restart", *events)
	}
}

// Context cancellation after visible output never restarts.
func TestStream_CancelAfterVisibleTextNeverRestarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			// The stream tears the context down alongside its transport
			// error — a cancelled caller must never restart.
			return &cancelingStream{fakeStream: fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart},
					{Type: llm.EventTextDelta, TextDelta: "partial"},
				},
				err: context.Canceled,
			}, cancel: cancel}, nil
		}
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: "should never appear"},
			{Type: llm.EventMessageStop},
		}}, nil
	}}
	p, events, _, slept := liveEngine(t, primary, nil)

	r, err := p.StreamChat(ctx, inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want surfaced context cancellation", err)
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "partial" {
		t.Errorf("visible text = %q, want only the interrupted output", got)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %+v, want none on cancellation", notices)
	}
	if calls, _ := primary.snapshot(); calls != 1 {
		t.Errorf("primary calls = %d, want 1 (cancellation never restarts)", calls)
	}
	if len(*slept) != 0 {
		t.Errorf("waits = %d, want 0", len(*slept))
	}
	if len(*events) != 1 || !strings.Contains((*events)[0].Reason, ReasonCancellation) ||
		!strings.Contains((*events)[0].Reason, ReasonContentEmitted) {
		t.Errorf("events = %+v, want single cancellation/content_emitted surface log", *events)
	}
}

// cancelingStream cancels the caller's context right before its transport
// error surfaces: the death is simultaneous with the cancellation.
type cancelingStream struct {
	fakeStream
	cancel context.CancelFunc
}

func (s *cancelingStream) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) == 0 && s.err != nil {
		s.cancel()
	}
	return s.fakeStream.Next()
}

// Regression: a textless response (tool-only or reasoning-only) completes via
// message_stop as its FIRST committing event — it flushes the held framing and
// buffered events and is delivered to the caller. A retryable transport error
// arriving AFTER that delivered message_stop must never restart: the message
// completed, so the turn is settled and existing behavior (surface, no
// recovery) must hold even when message_stop never passed through the
// live-event path that usually latches it.
func TestStream_MessageStopFirstCommitThenRetryableErrorNeverRestarts(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventReasoning, ReasoningID: "rs_1"},
				{Type: llm.EventToolUseStart, ToolUseID: "call-1", ToolName: "read_file"},
				{Type: llm.EventToolUseStop},
				{Type: llm.EventMessageStop, StopReason: "tool_use"},
			},
			err: networkErr("anthropic"), // trailing error after message_stop
		}, nil
	}}
	p, events, _, slept := liveEngine(t, primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("trailing retryable error after a completed message must surface")
	}
	if len(evs) != 5 {
		t.Errorf("events = %d (%v), want the completed message's 5 events only", len(evs), eventTypes(evs))
	}
	if len(notices) != 0 {
		t.Errorf("notices = %+v, want none — a completed message never restarts", notices)
	}
	if calls, _ := primary.snapshot(); calls != 1 {
		t.Errorf("primary calls = %d, want 1 (never restart a finished message)", calls)
	}
	if len(*slept) != 0 {
		t.Errorf("waits = %d, want 0", len(*slept))
	}
	if len(*events) != 1 || (*events)[0].Action != ActionSurface ||
		(*events)[0].Reason != ReasonContentEmitted {
		t.Errorf("events = %+v, want single content_emitted surface log, no restart", *events)
	}
}

// A mid-answer failure on the BACKUP (after a pre-content failover) must not
// trigger a live restart or any further provider switch: the message was
// already restarted once across providers, and a second switch or a re-dial
// could contradict the visible text. The error surfaces; calls stay at 1/1.
func TestStream_MidAnswerFailureAfterFailoverNeverRestartsOrSwitches(t *testing.T) {
	// Primary dies pre-content with a failover-worthy quota error.
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic")}}
	// Backup streams visible text, then dies mid-answer with a retryable
	// network error — tempting a restart, but the failover is spent.
	backup := &liveRetry{name: "openai", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "backup partial"},
			},
			err: networkErr("openai"),
		}, nil
	}}
	var events []Event
	p := New(primary, Options{
		Backup:         backup,
		BackupModelFor: func(string) string { return "m" },
		OnEvent:       func(e Event) { events = append(events, e) },
	})

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("backup mid-answer failure must surface after failover")
	}
	if len(evs) != 2 || evs[0].Type != llm.EventMessageStart || evs[1].TextDelta != "backup partial" {
		t.Errorf("events = %v, want the backup's delivered frames only", eventTypes(evs))
	}
	var restartNotices []string
	for _, n := range notices {
		if strings.Contains(n, "restarting the reply") {
			restartNotices = append(restartNotices, n)
		}
	}
	if len(restartNotices) != 0 {
		t.Errorf("restart notices = %+v, want none after failover", restartNotices)
	}
	if calls, _ := backup.snapshot(); primary.calls != 1 || calls != 1 {
		t.Errorf("calls primary=%d backup=%d, want 1/1 (no restart, no provider switch)", primary.calls, calls)
	}
	// The gate log names the closed gates. After failover the reader runs
	// in its committed-stream mode, so the skip reason is the spent
	// failover gate (log-only; the delivered-text record stays on the
	// emitted-kind flags).
	var surface []Event
	for _, ev := range events {
		if ev.Action == ActionSurface {
			surface = append(surface, ev)
		}
	}
	if len(surface) != 1 || surface[0].Reason != ReasonAlreadyFailedOver {
		t.Errorf("surface events = %+v, want one with already_failed_over", surface)
	}
	if !surface[0].EmittedText {
		t.Errorf("surface event must record delivered text: %+v", surface[0])
	}
}

// If the restart dial itself fails retryably, the original mid-stream error
// surfaces. The restart notice is still delivered first, and a configured
// backup must NOT be consulted: visible text already flowed, so a fresh
// backup answer would contradict the interrupted text.
func TestStream_LiveRestartDialFailureSurfaces(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "cut"},
			},
			err: networkErr("anthropic"),
		}, nil
	}}
	primary.dialErr = func(call int) error {
		if call >= 2 {
			return networkErr("anthropic")
		}
		return nil
	}
	backup := &fakeProvider{name: "openai"}
	p, events, _, slept := liveEngine(t, primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("dial failure after live restart must surface")
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "cut" {
		t.Errorf("visible text = %q, want only the interrupted output", got)
	}
	if len(notices) != 1 {
		t.Errorf("notices = %+v, want the single restart notice", notices)
	}
	if calls, _ := primary.snapshot(); calls != 2 {
		t.Errorf("primary calls = %d, want 2 (original + one failed restart)", calls)
	}
	if backup.calls != 0 {
		t.Errorf("backup calls = %d, want 0 (a failed live-restart dial never fails over)", backup.calls)
	}
	if len(*slept) != 1 {
		t.Errorf("waits = %d, want 1", len(*slept))
	}
	if len(*events) != 2 || (*events)[1].Action != ActionSurface ||
		(*events)[1].Reason != ReasonRetryLimit+","+ReasonContentEmitted {
		t.Errorf("events = %+v, want retry then retry-limit/content_emitted surface", *events)
	}
}

// Non-retryable errors (e.g. quota) after visible text keep surfacing with the
// content_emitted gate — the live-restart policy must not weaken that.
func TestStream_NonretryableAfterVisibleTextStillSurfaces(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		return &fakeStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "partial"},
			},
			err: quotaErr("anthropic"),
		}, nil
	}}
	backup := &fakeProvider{name: "openai"}
	p, events, _, slept := liveEngine(t, primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	evs, notices, err := drain(t, r)
	if err == nil {
		t.Fatal("non-retryable mid-stream failure must surface")
	}
	var visible strings.Builder
	for _, ev := range evs {
		if ev.Type == llm.EventTextDelta {
			visible.WriteString(ev.TextDelta)
		}
	}
	if got := visible.String(); got != "partial" {
		t.Errorf("visible text = %q, want only the interrupted output", got)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %+v, want none for non-retryable failure", notices)
	}
	if calls, _ := primary.snapshot(); calls != 1 || backup.calls != 0 {
		t.Errorf("calls primary=%d backup=%d, want 1/0 (mid-answer never fails over)", calls, backup.calls)
	}
	if len(*slept) != 0 {
		t.Errorf("waits = %d, want 0", len(*slept))
	}
	if len(*events) != 1 || (*events)[0].Reason != ReasonNonretryable+","+ReasonContentEmitted || !(*events)[0].EmittedText {
		t.Errorf("events = %+v, want single nonretryable/content_emitted surface log", *events)
	}
}

// End-to-end through llm.CollectStream: the persisted response must reflect
// ONLY the successful attempt (fresh message_start resets accumulation and
// usage), while the display callback still shows both interrupted and final
// text and the restart notice.
func TestStream_LiveRestartKeepsCollectorResponseClean(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			return &fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart, InputTokens: 100},
					{Type: llm.EventTextDelta, TextDelta: "dead attempt text "},
					{Type: llm.EventToolUseStart, ToolUseID: "toolu_dead", ToolName: "read_file"},
					{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a.txt"}`},
				},
				err: networkErr("anthropic"),
			}, nil
		}
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart, InputTokens: 5},
			{Type: llm.EventTextDelta, TextDelta: "live answer"},
			{Type: llm.EventMessageStop, OutputTokens: 9},
		}}, nil
	}}
	backup := &fakeProvider{name: "openai"}
	p, _, _, _ := liveEngine(t, primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	var shown strings.Builder
	var notices []string
	resp, err := llm.CollectStream(context.Background(), r, func(s string) { shown.WriteString(s) }, func(n string) { notices = append(notices, n) })
	if err != nil {
		t.Fatalf("CollectStream: %v", err)
	}

	// Display keeps the interrupted text plus the restart notice plus the
	// final answer — the user sees why the reply restarted.
	if got := shown.String(); got != "dead attempt text live answer" {
		t.Errorf("displayed text = %q, want both attempts' visible text", got)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "restarting the reply") {
		t.Errorf("notices = %+v, want one restart notice", notices)
	}

	// The persisted response carries ONLY the successful attempt: its text,
	// its tokens, no dead-attempt tool fragment, no duplicated concatenation.
	var text strings.Builder
	var tools int
	for _, blk := range resp.Blocks {
		switch blk.Type {
		case llm.BlockText:
			text.WriteString(blk.Text)
		case llm.BlockToolUse:
			tools++
			if blk.ToolUseID == "toolu_dead" {
				t.Errorf("dead attempt tool %q leaked into the collected response", blk.ToolUseID)
			}
		}
	}
	if got := text.String(); got != "live answer" {
		t.Errorf("collected text = %q, want only the successful attempt's text", got)
	}
	if tools != 0 {
		t.Errorf("tool blocks = %d, want 0 (dead fragment must not survive)", tools)
	}
	if resp.InputTokens != 5 || resp.OutputTokens != 9 {
		t.Errorf("tokens = %d/%d, want 5/9 (fresh message_start resets usage)", resp.InputTokens, resp.OutputTokens)
	}
	if calls, models := primary.snapshot(); calls != 2 || len(models) != 2 || models[0] != models[1] {
		t.Errorf("primary calls=%d models=%v, want 2 identical same-request calls", calls, models)
	}
	if backup.calls != 0 {
		t.Errorf("backup calls = %d, want 0", backup.calls)
	}
}

// The synthetic-start contract: a provider whose retried attempt does NOT
// re-emit message_start still gets exactly one injected message_start before
// its first content frame, so llm.CollectStream's restart reset fires even
// without provider framing — the collected response stays clean (only the
// successful attempt's text and tokens; no dead fragments, no concatenation).
func TestStream_LiveRestartWithoutFreshMessageStartStillResetsCollector(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			return &fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart, InputTokens: 100},
					{Type: llm.EventTextDelta, TextDelta: "dead text "},
					{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead thinking"},
					{Type: llm.EventToolUseStart, ToolUseID: "toolu_dead", ToolName: "read_file"},
					{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a.txt"}`},
				},
				err: networkErr("anthropic"),
			}, nil
		}
		// Fresh attempt with NO message_start frame at all.
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventTextDelta, TextDelta: "live answer"},
			{Type: llm.EventToolUseStart, ToolUseID: "toolu_live", ToolName: "read_file", ToolInputRaw: json.RawMessage(`{"path":"b.txt"}`)},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, OutputTokens: 9},
		}}, nil
	}}
	p, _, _, _ := liveEngine(t, primary, &fakeProvider{name: "openai"})

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	// The raw event stream must show exactly one synthetic message_start,
	// injected before the fresh attempt's first content frame.
	var types []llm.StreamEventType
	for {
		ev, ok, err := r.Next()
		if err != nil {
			t.Fatalf("stream ended in error: %v", err)
		}
		if !ok {
			break
		}
		if ev.Type != llm.EventNotice {
			types = append(types, ev.Type)
		}
	}
	want := []llm.StreamEventType{
		llm.EventMessageStart, llm.EventTextDelta, llm.EventReasoning,
		llm.EventToolUseStart, llm.EventToolUseInputDelta,
		llm.EventMessageStart, // synthetic: fresh attempt had no framing
		llm.EventTextDelta, llm.EventToolUseStart, llm.EventToolUseStop,
		llm.EventMessageStop,
	}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("event types = %v, want %v (exactly one synthetic message_start before fresh content)", types, want)
	}
}

// The synthetic-start contract, end to end through the collector: without any
// provider framing on the retry, the injected message_start still forces
// CollectStream to discard the interrupted attempt's partial message — the
// collected response carries only the successful attempt's text, tools, and
// usage.
func TestStream_LiveRestartWithoutFreshMessageStartCollectorClean(t *testing.T) {
	primary := &liveRetry{name: "anthropic", stream: func(call int) (inference.Stream, error) {
		if call == 1 {
			return &fakeStream{
				events: []llm.StreamEvent{
					{Type: llm.EventMessageStart, InputTokens: 100},
					{Type: llm.EventTextDelta, TextDelta: "dead text "},
					{Type: llm.EventReasoning, ReasoningID: "rs_dead", ReasoningData: "dead thinking"},
					{Type: llm.EventToolUseStart, ToolUseID: "toolu_dead", ToolName: "read_file"},
					{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a.txt"}`},
				},
				err: networkErr("anthropic"),
			}, nil
		}
		// Fresh attempt with NO message_start frame at all.
		return &fakeStream{events: []llm.StreamEvent{
			{Type: llm.EventTextDelta, TextDelta: "live answer"},
			{Type: llm.EventMessageStop, OutputTokens: 9},
		}}, nil
	}}
	p, _, _, _ := liveEngine(t, primary, &fakeProvider{name: "openai"})

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	resp, err := llm.CollectStream(context.Background(), r, nil, nil)
	if err != nil {
		t.Fatalf("CollectStream: %v", err)
	}
	var text strings.Builder
	var tools, reasoning int
	for _, blk := range resp.Blocks {
		switch blk.Type {
		case llm.BlockText:
			text.WriteString(blk.Text)
		case llm.BlockToolUse:
			tools++
		case llm.BlockReasoning:
			reasoning++
		}
	}
	if got := text.String(); got != "live answer" {
		t.Errorf("collected text = %q, want only the successful attempt's text (synthetic start must reset)", got)
	}
	if tools != 0 || reasoning != 0 {
		t.Errorf("tool blocks = %d, reasoning = %d, want 0/0 (dead fragments must not survive)", tools, reasoning)
	}
	if resp.InputTokens != 0 || resp.OutputTokens != 9 {
		t.Errorf("tokens = %d/%d, want 0/9 (synthetic start resets usage)", resp.InputTokens, resp.OutputTokens)
	}
}
