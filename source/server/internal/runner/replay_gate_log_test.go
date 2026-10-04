package runner

import (
	"path/filepath"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/routinglog"
)

// This file pins the whole-turn replay-gate LOG: when a failed turn already
// delivered visible text and/or executed a tool, the runner records
// loop.replay_blocked with the precise reason(s) — and only then, because the
// retry semantics themselves are unchanged (the existing replay-probe tests
// assert the caller-visible behavior; these assert the log line exists and
// the retry counts stay identical).

func newRoutingLog(t *testing.T) (string, *routinglog.Writer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routing.jsonl")
	w, err := routinglog.NewWriter(path)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return path, w
}

func TestReplayGateLogVisibleTextBlocksWholeTurnRetry(t *testing.T) {
	path, w := newRoutingLog(t)
	p := &partialNetworkProvider{}
	deps := buildDeps(p)
	deps.RoutingLog = w
	_, err := New(deps).RunTurn(t.Context(), Request{
		Input: "fixture", ConversationID: "replay-log-text", WorkDir: t.TempDir(),
	}, &captureSink{}, nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	if p.calls != 1 {
		t.Fatalf("retry semantics changed: provider calls=%d, want 1", p.calls)
	}
	evs := readRoutingEvents(t, path, "loop.replay_blocked")
	if len(evs) != 1 {
		t.Fatalf("got %d loop.replay_blocked events, want 1", len(evs))
	}
	ev := evs[0]
	if got := ev["conversation_id"]; got != "replay-log-text" {
		t.Errorf("conversation_id = %v", got)
	}
	if got := ev["attempt"]; got != "primary" {
		t.Errorf("attempt = %v, want primary", got)
	}
	if got := ev["blocked_by"]; got != "visible_text" {
		t.Errorf("blocked_by = %v, want visible_text", got)
	}
	if got := ev["error_class"]; got != "network" {
		t.Errorf("error_class = %v, want network", got)
	}
	// No retry may have been attempted for the blocked turn.
	if retries := readRoutingEvents(t, path, "loop.retry"); len(retries) != 0 {
		t.Errorf("replay-blocked turn still logged %d loop.retry events", len(retries))
	}
}

func TestReplayGateLogToolExecutionBlocksWholeTurnRetry(t *testing.T) {
	path, w := newRoutingLog(t)
	p := &executedToolProvider{}
	deps := buildDeps(p)
	reg := agenttools.NewRegistry()
	if err := reg.Register(testTool{name: "probe", perm: agenttools.PermR}); err != nil {
		t.Fatal(err)
	}
	deps.Tools = &fakeToolSvc{reg: reg}
	deps.RoutingLog = w
	_, err := New(deps).RunTurn(t.Context(), Request{
		Input: "probe", ConversationID: "replay-log-tool", WorkDir: t.TempDir(),
	}, &captureSink{}, nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	if p.calls != 2 {
		t.Fatalf("retry semantics changed: provider calls=%d, want 2", p.calls)
	}
	evs := readRoutingEvents(t, path, "loop.replay_blocked")
	if len(evs) != 1 {
		t.Fatalf("got %d loop.replay_blocked events, want 1", len(evs))
	}
	if got := evs[0]["blocked_by"]; got != "tool_execution" {
		t.Errorf("blocked_by = %v, want tool_execution", got)
	}
	if got := evs[0]["attempt"]; got != "primary" {
		t.Errorf("attempt = %v, want primary", got)
	}
}

// TestReplayGateLogSilentFailureStillRetries pins that the new log is
// instrumentation only: a failure with nothing yet delivered retries exactly
// as before and emits NO replay_blocked line.
func TestReplayGateLogSilentFailureStillRetries(t *testing.T) {
	path, w := newRoutingLog(t)
	deps := buildDeps(&busyProvider{})
	deps.RoutingLog = w
	_, err := New(deps).RunTurn(t.Context(), Request{
		Input: "fixture", ConversationID: "replay-log-silent", WorkDir: t.TempDir(),
	}, &captureSink{}, nil, nil)
	if err == nil {
		t.Fatal("expected busy error after retry")
	}
	retries := readRoutingEvents(t, path, "loop.retry")
	if len(retries) != 1 {
		t.Fatalf("got %d loop.retry events, want exactly one same-provider retry", len(retries))
	}
	if got := retries[0]["attempt"]; got != "same_provider" {
		t.Errorf("loop.retry attempt = %v, want same_provider", got)
	}
	if got := retries[0]["error_class"]; got != "busy" {
		t.Errorf("loop.retry error_class = %v, want busy", got)
	}
	if evs := readRoutingEvents(t, path, "loop.replay_blocked"); len(evs) != 0 {
		t.Errorf("silent retry must not emit loop.replay_blocked, got %d", len(evs))
	}
}
