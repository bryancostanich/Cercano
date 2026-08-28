package resilience

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// fakeProvider scripts a sequence of outcomes: each Chat/StreamChat call
// consumes the next entry. An entry with err != nil fails; otherwise it
// succeeds (Chat returns a result naming the provider; StreamChat returns a
// two-event stream: text delta "from <name>" + message stop).
type fakeProvider struct {
	name           string
	outcome        []error
	calls          int
	models         []string // Model field of each observed request
	streamOverride func(context.Context, inference.Call) (inference.Stream, error)
}

func (f *fakeProvider) next() error {
	var err error
	if f.calls < len(f.outcome) {
		err = f.outcome[f.calls]
	}
	f.calls++
	return err
}

func (f *fakeProvider) Name() string                         { return f.name }
func (f *fakeProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }

func (f *fakeProvider) Chat(_ context.Context, req inference.Call) (inference.Result, error) {
	f.models = append(f.models, req.Model)
	if err := f.next(); err != nil {
		return inference.Result{}, err
	}
	return inference.Result{Model: f.name, Blocks: []llm.Block{{Type: llm.BlockText, Text: "from " + f.name}}}, nil
}

func (f *fakeProvider) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	if f.streamOverride != nil {
		return f.streamOverride(ctx, req)
	}
	f.models = append(f.models, req.Model)
	if err := f.next(); err != nil {
		return nil, err
	}
	return &fakeStream{events: []llm.StreamEvent{
		{Type: llm.EventTextDelta, TextDelta: "from " + f.name},
		{Type: llm.EventMessageStop},
	}}, nil
}

type fakeStream struct {
	events []llm.StreamEvent
	err    error // returned after events drain, instead of clean end
	closed bool
}

func (s *fakeStream) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) > 0 {
		ev := s.events[0]
		s.events = s.events[1:]
		return ev, true, nil
	}
	if s.err != nil {
		return llm.StreamEvent{}, false, s.err
	}
	return llm.StreamEvent{}, false, nil
}

func (s *fakeStream) Close() error { s.closed = true; return nil }

func busyErr(provider string, retryAfter time.Duration) error {
	return &llm.Error{Class: llm.ErrBusy, Provider: provider, StatusCode: 429,
		RetryAfter: retryAfter, Err: errors.New("busy")}
}

func quotaErr(provider string) error {
	return &llm.Error{Class: llm.ErrQuota, Provider: provider, StatusCode: 429,
		RetryAfter: time.Hour, Err: errors.New("usage limit reached")}
}

func networkErr(provider string) error {
	return &llm.Error{Class: llm.ErrNetwork, Provider: provider,
		Err: errors.New("read tcp: connection reset by peer")}
}

// build wires an engine with instant sleeps and an event recorder.
func build(primary, backup *fakeProvider) (*Provider, *[]Event, *[]time.Duration) {
	var events []Event
	var slept []time.Duration
	opts := Options{OnEvent: func(e Event) { events = append(events, e) }}
	if backup != nil {
		opts.Backup = backup
		opts.BackupModelFor = func(tier string) string {
			if tier == "" {
				return "backup-default"
			}
			return "backup-" + tier
		}
	}
	p := New(primary, opts)
	p.sleep = func(_ context.Context, d time.Duration) bool { slept = append(slept, d); return true }
	return p, &events, &slept
}

func collectStream(t *testing.T, r inference.Stream) ([]llm.StreamEvent, error) {
	t.Helper()
	var evs []llm.StreamEvent
	for {
		ev, ok, err := r.Next()
		if err != nil {
			return evs, err
		}
		if !ok {
			return evs, nil
		}
		evs = append(evs, ev)
	}
}

func TestChat_BusyRetriesOnceThenSucceeds(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{busyErr("anthropic", time.Second), nil}}
	backup := &fakeProvider{name: "openai"}
	p, events, slept := build(primary, backup)

	res, err := p.Chat(context.Background(), inference.Call{Model: "claude"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Model != "anthropic" {
		t.Errorf("served by %q, want primary", res.Model)
	}
	if primary.calls != 2 || backup.calls != 0 {
		t.Errorf("calls primary=%d backup=%d, want 2/0", primary.calls, backup.calls)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry {
		t.Errorf("events = %+v, want one retry", *events)
	}
	if len(*slept) != 1 || (*slept)[0] != time.Second {
		t.Errorf("slept %v, want the server's 1s Retry-After", *slept)
	}
}

func TestChat_NetworkRetriesOnceThenSucceeds(t *testing.T) {
	// A transport reset is transient: the engine must retry the SAME provider
	// once before any failover — the regression this fix targets, where a reset
	// jumped straight to the backup tier.
	primary := &fakeProvider{name: "anthropic", outcome: []error{networkErr("anthropic"), nil}}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)

	res, err := p.Chat(context.Background(), inference.Call{Model: "claude"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Model != "anthropic" {
		t.Errorf("served by %q, want primary (no failover)", res.Model)
	}
	if primary.calls != 2 || backup.calls != 0 {
		t.Errorf("calls primary=%d backup=%d, want 2/0", primary.calls, backup.calls)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry ||
		(*events)[0].Class != llm.ErrNetwork {
		t.Errorf("events = %+v, want one network retry", *events)
	}
}

func TestChat_EventFromUsesConcreteErrorProvider(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{busyErr("openai-responses", 0), nil}}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)

	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionRetry {
		t.Fatalf("events = %+v, want one retry", *events)
	}
	if (*events)[0].From != "openai-responses" {
		t.Fatalf("event From = %q, want concrete provider openai-responses", (*events)[0].From)
	}
	if got := (*events)[0].Notice(); got != "openai-responses server busy — trying once more" {
		t.Fatalf("notice = %q", got)
	}
}

func TestChat_BusyTwiceFailsOverWithStillBusyNotice(t *testing.T) {
	primary := &fakeProvider{name: "openai", outcome: []error{busyErr("openai", 0), busyErr("openai", 0)}}
	backup := &fakeProvider{name: "anthropic"}
	p, events, _ := build(primary, backup)

	res, err := p.Chat(context.Background(), inference.Call{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Model != "anthropic" || backup.calls != 1 {
		t.Errorf("want backup to serve, got model=%q backup calls=%d", res.Model, backup.calls)
	}
	if len(*events) != 2 || (*events)[1].Action != ActionFailover {
		t.Fatalf("events = %+v, want retry then failover", *events)
	}
	notice := (*events)[1].Notice()
	if notice != "openai still busy — switching to anthropic" {
		t.Errorf("notice = %q", notice)
	}
}

func TestChat_QuotaFailsOverImmediately(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic")}}
	backup := &fakeProvider{name: "openai"}
	p, events, slept := build(primary, backup)

	res, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Model != "openai" || primary.calls != 1 {
		t.Errorf("want immediate failover, got model=%q primary calls=%d", res.Model, primary.calls)
	}
	if len(*slept) != 0 {
		t.Errorf("quota must not wait, slept %v", *slept)
	}
	if n := (*events)[0].Notice(); n != "anthropic quota reached — switching to openai" {
		t.Errorf("notice = %q", n)
	}
}

func TestChat_QuotaCooldownSkipsPrimaryOnNextTurn(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic"), nil}}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)

	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"}); err != nil {
		t.Fatalf("first chat err = %v", err)
	}
	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"}); err != nil {
		t.Fatalf("second chat err = %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1; quota cooldown should skip primary on the next turn", primary.calls)
	}
	if backup.calls != 2 {
		t.Fatalf("backup calls = %d, want 2", backup.calls)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %+v, want only the initial failover notice", *events)
	}
}

func TestChat_QuotaCooldownHonorsRetryAfterAndExpires(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	quota := &llm.Error{Class: llm.ErrQuota, Provider: "anthropic", StatusCode: 429,
		RetryAfter: 2 * time.Minute, Err: errors.New("usage limit reached")}
	primary := &fakeProvider{name: "anthropic", outcome: []error{quota, nil}}
	backup := &fakeProvider{name: "openai"}
	p, _, _ := build(primary, backup)
	p.now = func() time.Time { return now }

	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"}); err != nil {
		t.Fatalf("first chat err = %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"}); err != nil {
		t.Fatalf("second chat err = %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls after one minute = %d, want 1", primary.calls)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"}); err != nil {
		t.Fatalf("third chat err = %v", err)
	}
	if primary.calls != 2 {
		t.Fatalf("primary calls after cooldown expiry = %d, want 2", primary.calls)
	}
	if backup.calls != 2 {
		t.Fatalf("backup calls = %d, want 2", backup.calls)
	}
}

func TestChat_InvalidRequestSurfaces(t *testing.T) {
	bad := &llm.Error{Class: llm.ErrInvalidRequest, Provider: "anthropic", StatusCode: 400, Err: errors.New("bad request")}
	primary := &fakeProvider{name: "anthropic", outcome: []error{bad}}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)

	_, err := p.Chat(context.Background(), inference.Call{})
	if !errors.Is(err, bad) {
		t.Fatalf("err = %v, want the surfaced 400", err)
	}
	if backup.calls != 0 {
		t.Error("malformed invalid_request must not fail over")
	}
	if len(*events) != 1 || (*events)[0].Action != ActionSurface {
		t.Errorf("events = %+v", *events)
	}
}

func TestChat_ModelUnavailableInvalidRequestFailsOver(t *testing.T) {
	bad := &llm.Error{Class: llm.ErrInvalidRequest, Provider: "anthropic", StatusCode: 404, Err: errors.New(`not_found_error: model: claude-opus-5`)}
	primary := &fakeProvider{name: "anthropic", outcome: []error{bad}}
	backup := &fakeProvider{name: "openai-responses"}
	p, events, _ := build(primary, backup)

	res, err := p.Chat(context.Background(), inference.Call{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if res.Model != "openai-responses" {
		t.Fatalf("result model/provider = %q, want backup", res.Model)
	}
	if backup.calls != 1 {
		t.Fatalf("backup calls = %d, want 1", backup.calls)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionFailover || (*events)[0].To != "openai-responses" {
		t.Fatalf("events = %+v", *events)
	}
}

func TestChat_NoBackupBusySurfacesAfterOneRetry(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{busyErr("anthropic", 0), busyErr("anthropic", 0)}}
	p, events, _ := build(primary, nil)

	_, err := p.Chat(context.Background(), inference.Call{})
	if llm.ClassOf(err) != llm.ErrBusy {
		t.Fatalf("err = %v", err)
	}
	if primary.calls != 2 {
		t.Errorf("calls = %d, want retry to still happen without a backup", primary.calls)
	}
	if len(*events) != 2 || (*events)[1].Action != ActionSurface {
		t.Errorf("events = %+v", *events)
	}
}

func TestChat_RetryWaitIsCapped(t *testing.T) {
	primary := &fakeProvider{name: "a", outcome: []error{busyErr("a", 20*time.Second), nil}}
	p, _, slept := build(primary, nil)

	if _, err := p.Chat(context.Background(), inference.Call{}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != defaultRetryWaitCap {
		t.Errorf("slept %v, want capped at %v", *slept, defaultRetryWaitCap)
	}
}

func TestChat_ContextCancelNeverRecovers(t *testing.T) {
	primary := &fakeProvider{name: "a", outcome: []error{quotaErr("a")}}
	backup := &fakeProvider{name: "b"}
	p, _, _ := build(primary, backup)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Chat(ctx, inference.Call{})
	if err == nil {
		t.Fatal("want error")
	}
	if backup.calls != 0 {
		t.Error("cancelled context must not fail over")
	}
}

func TestChat_TierRewriteOnFailover(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic")}}
	backup := &fakeProvider{name: "openai"}
	p, _, _ := build(primary, backup)

	if _, err := p.Chat(context.Background(), inference.Call{Model: "claude-haiku-4-5", Tier: "economy"}); err != nil {
		t.Fatal(err)
	}
	if len(backup.models) != 1 || backup.models[0] != "backup-economy" {
		t.Errorf("backup saw model %v, want tier-resolved backup-economy", backup.models)
	}
}

func TestStream_TypedEventErrorContextOverflowSurfaces(t *testing.T) {
	primary := &fakeProvider{name: "anthropic"}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)
	primaryStreamErr := &llm.Error{Class: llm.ErrContextOverflow, Provider: "openai-responses", Err: errors.New("context window exceeded")}
	primary.outcome = []error{nil}

	// Replace the successful stream with one that emits a typed EventError before
	// content. This exercises the legacy EventError path while preserving class.
	primaryStream := &fakeStream{events: []llm.StreamEvent{{Type: llm.EventError, ErrText: primaryStreamErr.Error(), Err: primaryStreamErr}}}
	primary.streamOverride = func(_ context.Context, req inference.Call) (inference.Stream, error) {
		primary.models = append(primary.models, req.Model)
		primary.calls++
		return primaryStream, nil
	}

	r, err := p.StreamChat(context.Background(), inference.Call{Model: "claude"})
	if err != nil {
		t.Fatalf("StreamChat dial err = %v", err)
	}
	_, _, err = r.Next()
	if err == nil {
		t.Fatal("Next unexpectedly succeeded; wanted context overflow")
	}
	if got := llm.ClassOf(err); got != llm.ErrContextOverflow {
		t.Fatalf("Next error class = %q, want context_overflow; err=%v", got, err)
	}
	if backup.calls != 0 {
		t.Fatalf("context overflow EventError should not fail over, backup calls=%d", backup.calls)
	}
	if len(*events) != 1 || (*events)[0].Action != ActionSurface || (*events)[0].From != "openai-responses" {
		t.Fatalf("events = %+v, want concrete-provider surface", *events)
	}
}

func TestStream_QuotaInjectsNoticeThenBackupContent(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic")}}
	backup := &fakeProvider{name: "openai"}
	p, _, _ := build(primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("stream err = %v", err)
	}
	if len(evs) < 3 || evs[0].Type != llm.EventNotice {
		t.Fatalf("events = %+v, want notice first", evs)
	}
	if evs[0].Notice != "anthropic quota reached — switching to openai" {
		t.Errorf("notice = %q", evs[0].Notice)
	}
	if evs[1].TextDelta != "from openai" {
		t.Errorf("content = %+v, want backup text", evs[1])
	}
}

func TestStream_QuotaCooldownSkipsPrimaryOnNextTurnWithoutRepeatedNotice(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", outcome: []error{quotaErr("anthropic"), nil}}
	backup := &fakeProvider{name: "openai"}
	p, events, _ := build(primary, backup)

	r, err := p.StreamChat(context.Background(), inference.Call{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("first stream dial err = %v", err)
	}
	if _, err := collectStream(t, r); err != nil {
		t.Fatalf("first stream err = %v", err)
	}
	r, err = p.StreamChat(context.Background(), inference.Call{Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("second stream dial err = %v", err)
	}
	evs, err := collectStream(t, r)
	if err != nil {
		t.Fatalf("second stream err = %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.calls)
	}
	if backup.calls != 2 {
		t.Fatalf("backup calls = %d, want 2", backup.calls)
	}
	if len(evs) == 0 || evs[0].Type == llm.EventNotice {
		t.Fatalf("second events = %+v, want direct backup content without a repeated notice", evs)
	}
	if len(*events) != 1 {
		t.Fatalf("events = %+v, want only the initial failover event", *events)
	}
}

func TestStream_BusyNoticeArrivesBeforeTheWait(t *testing.T) {
	primary := &fakeProvider{name: "openai", outcome: []error{busyErr("openai", time.Second), nil}}
	p, _, slept := build(primary, nil)

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatal(err)
	}
	ev, ok, err := r.Next()
	if err != nil || !ok || ev.Type != llm.EventNotice {
		t.Fatalf("first = (%+v, %v, %v), want the notice", ev, ok, err)
	}
	if ev.Notice != "openai server busy — trying once more" {
		t.Errorf("notice = %q", ev.Notice)
	}
	if len(*slept) != 0 {
		t.Error("the wait must happen AFTER the notice is delivered, not before")
	}
	ev, ok, err = r.Next()
	if err != nil || !ok || ev.TextDelta != "from openai" {
		t.Fatalf("second = (%+v, %v, %v), want primary content after retry", ev, ok, err)
	}
	if len(*slept) != 1 {
		t.Errorf("slept %v, want exactly one wait", *slept)
	}
}

func TestStream_MidStreamFailureNeverRecovers(t *testing.T) {
	// Primary delivers one real event, THEN dies with a failover-worthy error.
	primary := &midStreamFailure{}
	backup := &fakeProvider{name: "openai"}
	var events []Event
	p := New(primary, Options{Backup: backup,
		BackupModelFor: func(string) string { return "m" },
		OnEvent:        func(e Event) { events = append(events, e) }})

	r, _ := p.StreamChat(context.Background(), inference.Call{})
	evs, err := collectStream(t, r)
	if len(evs) != 1 || evs[0].TextDelta != "partial" {
		t.Fatalf("events = %+v", evs)
	}
	if err == nil || backup.calls != 0 {
		t.Errorf("mid-stream failure must surface (err=%v) and never re-serve (backup calls=%d)", err, backup.calls)
	}
}

type midStreamFailure struct{}

func (m *midStreamFailure) Name() string                         { return "anthropic" }
func (m *midStreamFailure) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (m *midStreamFailure) Chat(context.Context, inference.Call) (inference.Result, error) {
	return inference.Result{}, errors.New("unused")
}
func (m *midStreamFailure) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	return &fakeStream{
		events: []llm.StreamEvent{{Type: llm.EventTextDelta, TextDelta: "partial"}},
		err:    quotaErr("anthropic"),
	}, nil
}

func TestStream_NoCascadeFromBackup(t *testing.T) {
	primary := &fakeProvider{name: "a", outcome: []error{quotaErr("a")}}
	backup := &fakeProvider{name: "b", outcome: []error{quotaErr("b")}}
	p, _, _ := build(primary, backup)

	r, _ := p.StreamChat(context.Background(), inference.Call{})
	_, err := collectStream(t, r)
	if err == nil {
		t.Fatal("backup failure must surface, not loop")
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Errorf("calls primary=%d backup=%d, want 1/1", primary.calls, backup.calls)
	}
}

func TestNoticeTexts(t *testing.T) {
	cases := []struct {
		ev   Event
		want string
	}{
		{Event{Action: ActionRetry, Class: llm.ErrBusy, From: "openai", To: "openai"},
			"openai server busy — trying once more"},
		{Event{Action: ActionFailover, Class: llm.ErrQuota, From: "anthropic", To: "openai"},
			"anthropic quota reached — switching to openai"},
		{Event{Action: ActionFailover, Class: llm.ErrBusy, From: "openai", To: "anthropic"},
			"openai still busy — switching to anthropic"},
		{Event{Action: ActionFailover, Class: llm.ErrAuth, From: "openai", To: "anthropic"},
			"openai auth failed — switching to anthropic"},
		{Event{Action: ActionFailover, Class: llm.ErrNetwork, From: "anthropic", To: "openai"},
			"anthropic unreachable — switching to openai"},
	}
	for _, tc := range cases {
		if got := tc.ev.Notice(); got != tc.want {
			t.Errorf("Notice() = %q, want %q", got, tc.want)
		}
	}
}

func TestName_ImpersonatesPrimary(t *testing.T) {
	p, _, _ := build(&fakeProvider{name: "anthropic"}, &fakeProvider{name: "openai"})
	if p.Name() != "anthropic" {
		t.Errorf("Name() = %q", p.Name())
	}
	if !strings.Contains(p.Name(), "anthropic") {
		t.Error("composite must impersonate the primary")
	}
}
