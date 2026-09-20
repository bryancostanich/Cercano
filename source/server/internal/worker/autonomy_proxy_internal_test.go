package worker

// autonomy_proxy_internal_test.go — focused tests for the worker-side
// autonomy-ledger proxy (streamAutonomyLedger) and the host adapter
// (HostAutonomyLedger). Every operation is driven through the proxy's
// encode/correlate/decode path against a real host-owned SQLite store — the
// exact AutonomyLedgerFunc the server wires via SetAutonomyLedger — proving:
//
//  1. create / get_active / update round-trips (host assigns the run id),
//  2. error propagation (unavailable ledger, unknown op, store failures) and
//     the sql.ErrNoRows miss contract,
//  3. conversation scope rejection (one conversation never sees another's run),
//  4. the worker capability sequence request_autonomous_execution →
//     capture_decision → auto_exit backed end-to-end by the proxy.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	pkgproto "cercano/source/server/pkg/proto"
)

// openAutonomyTestStore opens a real in-memory SQLite conversation store (the
// ledger's production owner) with EnsureConversation rows for convIDs.
func openAutonomyTestStore(t *testing.T, convIDs ...string) conversation.Store {
	t.Helper()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range convIDs {
		if err := store.EnsureConversation(context.Background(), id, "/proj", "model"); err != nil {
			t.Fatalf("EnsureConversation(%s): %v", id, err)
		}
	}
	return store
}

// newHostBackedProxy builds a streamAutonomyLedger whose emit is answered by
// applying the request through hostFn — mirroring exactly what the host's
// RunTurn loop does for AutonomyRequest — and delivering the acknowledged
// response back to the proxy's pending channel.
func newHostBackedProxy(t *testing.T, hostFn AutonomyLedgerFunc) *streamAutonomyLedger {
	t.Helper()
	p := newStreamAutonomyLedger(nil)
	p.sendFn = func(m *pkgproto.WorkerToHost) {
		req := m.GetAutonomyRequest()
		if req == nil {
			t.Errorf("proxy sent non-autonomy message: %v", m)
			return
		}
		runJSON, found, err := hostFn(context.Background(), req.GetOp(), string(req.GetRunJson()), req.GetConversationId())
		resp := &pkgproto.AutonomyLedgerResponse{Id: req.GetId(), RunJson: []byte(runJSON), Found: found}
		if err != nil {
			resp.Error = err.Error()
		}
		p.deliver(resp)
	}
	return p
}

// TestAutonomyProxy_CreateGetUpdateThroughHostStore proves the full proxy path
// against the real SQLite store: the host assigns the run id on create,
// get_active scopes by conversation, and update persists through the proxy.
func TestAutonomyProxy_CreateGetUpdateThroughHostStore(t *testing.T) {
	ctx := context.Background()
	store := openAutonomyTestStore(t, "conv-a", "conv-b")
	p := newHostBackedProxy(t, HostAutonomyLedger(store))

	created, err := p.CreateAutonomyRun(ctx, conversation.AutonomyRun{
		ConversationID: "conv-a",
		State:          "running",
		BriefJSON:      `{"goal":"ship the demo"}`,
	})
	if err != nil {
		t.Fatalf("CreateAutonomyRun through proxy: %v", err)
	}
	if created.RunID == "" {
		t.Fatal("host did not assign the run id; stored run echoed without one")
	}

	// The row lives in the host store — visible directly, not via the proxy.
	hostRow, err := store.GetActiveAutonomyRun(ctx, "conv-a")
	if err != nil {
		t.Fatalf("host GetActiveAutonomyRun: %v", err)
	}
	if hostRow.RunID != created.RunID || hostRow.BriefJSON != created.BriefJSON {
		t.Fatalf("host row %+v does not match proxied create %+v", hostRow, created)
	}

	// get_active through the proxy returns the stored run.
	got, err := p.GetActiveAutonomyRun(ctx, "conv-a")
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun through proxy: %v", err)
	}
	if got.RunID != created.RunID || got.State != "running" {
		t.Fatalf("proxied get = %+v, want run %s running", got, created.RunID)
	}

	// update through the proxy persists a decision append.
	created.DecisionsJSON = `[{"decision_point":"storage shape","chosen_path":"proxy over stream"}]`
	if err := p.UpdateAutonomyRun(ctx, created); err != nil {
		t.Fatalf("UpdateAutonomyRun through proxy: %v", err)
	}
	hostRow, err = store.GetActiveAutonomyRun(ctx, "conv-a")
	if err != nil {
		t.Fatalf("host GetActiveAutonomyRun after update: %v", err)
	}
	if !strings.Contains(hostRow.DecisionsJSON, "storage shape") {
		t.Fatalf("update did not persist through proxy; decisions=%q", hostRow.DecisionsJSON)
	}

	// A second create while one run is active is rejected by the host's
	// one-active-run index — surfaced through the proxy as an error.
	if _, err := p.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-a", State: "running"}); err == nil {
		t.Fatal("expected one-active-run rejection from host store, got success")
	}
}

// TestAutonomyProxy_MissAndScopeRejection proves the sql.ErrNoRows miss
// contract and that get_active is scoped: a run in one conversation is never
// visible to another, and capabilities can't cross conversations.
func TestAutonomyProxy_MissAndScopeRejection(t *testing.T) {
	ctx := context.Background()
	store := openAutonomyTestStore(t, "conv-a", "conv-b")
	p := newHostBackedProxy(t, HostAutonomyLedger(store))

	if _, err := p.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-a", State: "running"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Miss: conv-b has no active run → sql.ErrNoRows (the in-process contract).
	_, err := p.GetActiveAutonomyRun(ctx, "conv-b")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get_active miss = %v, want sql.ErrNoRows", err)
	}

	// Scope: updating conv-b's (nonexistent) run by the id borrowed from
	// conv-a is rejected by the host store — ids never cross conversations.
	a, err := p.GetActiveAutonomyRun(ctx, "conv-a")
	if err != nil {
		t.Fatalf("get active conv-a: %v", err)
	}
	foreign := a
	foreign.ConversationID = "conv-b"
	// The error text crosses the stream (host errors are stringly-typed, so
	// the sentinel is not errors.Is-matchable here); get_active misses keep
	// their sql.ErrNoRows contract through the explicit found flag instead.
	if err := p.UpdateAutonomyRun(ctx, foreign); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("cross-conversation update = %v, want no-rows rejection", err)
	}
}

// TestAutonomyProxy_ErrorPropagation covers the error surfaces: an unwired
// host ledger, an unknown op, a host store failure, and context cancellation.
func TestAutonomyProxy_ErrorPropagation(t *testing.T) {
	ctx := context.Background()

	// Unwired ledger: HostAutonomyLedger(nil) errors clearly, and the proxy
	// surfaces the host's error text verbatim.
	p := newHostBackedProxy(t, HostAutonomyLedger(nil))
	_, err := p.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: "conv-x"})
	if err == nil || !strings.Contains(err.Error(), "autonomy ledger is not available") {
		t.Fatalf("unwired ledger error = %v", err)
	}

	// Unknown op is rejected by the host adapter.
	store := openAutonomyTestStore(t, "conv-x")
	hostFn := HostAutonomyLedger(store)
	if _, _, err := hostFn(ctx, "bogus", "", "conv-x"); err == nil || !strings.Contains(err.Error(), "unknown autonomy ledger op") {
		t.Fatalf("unknown op error = %v", err)
	}

	// Host store failure (create without a conversation row scope): the store's
	// error text reaches the caller through the proxy.
	p2 := newHostBackedProxy(t, hostFn)
	_, err = p2.CreateAutonomyRun(ctx, conversation.AutonomyRun{ConversationID: ""})
	if err == nil || !strings.Contains(err.Error(), "conversation id required") {
		t.Fatalf("expected host store validation error, got %v", err)
	}

	// Context cancellation: a request the host never answers fails with the
	// context error rather than hanging.
	p3 := newStreamAutonomyLedger(nil)
	var sendMu sync.Mutex
	p3.sendFn = func(*pkgproto.WorkerToHost) { sendMu.Lock(); defer sendMu.Unlock() } // host never responds
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := p3.GetActiveAutonomyRun(cctx, "conv-x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled call = %v, want context.DeadlineExceeded", err)
	}

	// A late/bogus delivery must not panic or wedge the proxy.
	p3.deliver(&pkgproto.AutonomyLedgerResponse{Id: 99999, Found: true})
}

// TestAutonomyProxy_WorkerCapabilities_EntryCaptureExit runs the real worker
// capability stack (same buildWorkerToolSvc wiring as a production worker turn)
// with the stream proxy as its ledger, then drives the autonomous protocol:
// entry approval → decision capture → exit, each durable write landing in the
// host store through the proxy. It also proves the protocol rejects a second
// entry while a run is active and allows re-entry after the run is abandoned.
func TestAutonomyProxy_WorkerCapabilities_EntryCaptureExit(t *testing.T) {
	ctx := context.Background()
	store := openAutonomyTestStore(t, "conv-run")
	p := newHostBackedProxy(t, HostAutonomyLedger(store))

	var mu sync.Mutex
	var profiles []string
	ts := workerAutonomyToolSvcWithLedger(t,
		func(_ context.Context, name string) error {
			mu.Lock()
			defer mu.Unlock()
			profiles = append(profiles, name)
			return nil
		},
		p,
	)

	// Entry: request_autonomous_execution creates the ledger run on the host
	// through the proxy, then flips the session profile.
	entryArgs := `{"goal":"ship the demo","done_when":["tests pass"]}`
	res, err := executeCapability(ctx, t, ts, "request_autonomous_execution", "conv-run", entryArgs)
	if err != nil {
		t.Fatalf("request_autonomous_execution: %v", err)
	}
	if res == nil || !strings.Contains(res.Text, "Entered autonomous mode") {
		t.Fatalf("entry result = %+v, want entered-autonomous confirmation", res)
	}
	run, err := store.GetActiveAutonomyRun(ctx, "conv-run")
	if err != nil {
		t.Fatalf("host lost the run the worker created: %v", err)
	}
	if run.State != "running" {
		t.Fatalf("run state = %q, want running", run.State)
	}
	if !strings.Contains(run.BriefJSON, "ship the demo") {
		t.Fatalf("brief not persisted: %q", run.BriefJSON)
	}

	// A second entry while the run is active is rejected.
	if _, err := executeCapability(ctx, t, ts, "request_autonomous_execution", "conv-run", entryArgs); err == nil ||
		!strings.Contains(err.Error(), "autonomous run already active") {
		t.Fatalf("second entry error = %v, want already-active rejection", err)
	}

	// capture_decision appends to the run's ledger through the proxy.
	decideArgs := `{"decision_point":"wire the ledger","options":[{"title":"proxy over stream","cost":"one round-trip","risk":"low","reward":"host ownership preserved","side_effects":"none"},{"title":"open sqlite in worker","cost":"two owners","risk":"high","reward":"fast","side_effects":"db contention"}],"counterarguments":[{"option":"proxy over stream","strongest_case":"latency per ledger write"}],"recommendation":"proxy over stream","chosen_path":"proxy over stream","why_cleanest":"keeps a single store owner","reversibility":"easy","stop_required":false}`
	if _, err := executeCapability(ctx, t, ts, "capture_decision", "conv-run", decideArgs); err != nil {
		t.Fatalf("capture_decision: %v", err)
	}
	run, err = store.GetActiveAutonomyRun(ctx, "conv-run")
	if err != nil {
		t.Fatalf("get run after capture_decision: %v", err)
	}
	if !strings.Contains(run.DecisionsJSON, "wire the ledger") {
		t.Fatalf("decision not persisted through proxy: %q", run.DecisionsJSON)
	}

	// Scope rejection: another conversation's capture_decision finds no run.
	if _, err := executeCapability(ctx, t, ts, "capture_decision", "conv-other", decideArgs); err == nil ||
		!strings.Contains(err.Error(), "no active autonomous run for conversation conv-other") {
		t.Fatalf("cross-conversation capture error = %v, want scope rejection", err)
	}

	// auto_exit abandons the run (durable) and leaves autonomous mode.
	if _, err := executeCapability(ctx, t, ts, "auto_exit", "conv-run", `{"reason":"scope done"}`); err != nil {
		t.Fatalf("auto_exit: %v", err)
	}
	if _, err := store.GetActiveAutonomyRun(ctx, "conv-run"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("run still active after auto_exit: %v", err)
	}
	latest, err := store.GetLatestAutonomyRun(ctx, "conv-run")
	if err != nil {
		t.Fatalf("GetLatestAutonomyRun after exit: %v", err)
	}
	if latest.State != "abandoned" {
		t.Fatalf("exit state = %q, want abandoned", latest.State)
	}
	mu.Lock()
	want := []string{"autonomous", "default"}
	if len(profiles) != len(want) || profiles[0] != want[0] || profiles[1] != want[1] {
		t.Fatalf("profile switches = %v, want %v", profiles, want)
	}
	mu.Unlock()

	// Continuation: after the run is abandoned, a fresh entry succeeds — the
	// append-only ledger permits a new run for the same conversation.
	if _, err := executeCapability(ctx, t, ts, "request_autonomous_execution", "conv-run", entryArgs); err != nil {
		t.Fatalf("re-entry after exit: %v", err)
	}
	run2, err := store.GetActiveAutonomyRun(ctx, "conv-run")
	if err != nil {
		t.Fatalf("re-entry run not active on host: %v", err)
	}
	if run2.RunID == latest.RunID {
		t.Fatal("re-entry reused the abandoned run id; ledger must be append-only")
	}
}
