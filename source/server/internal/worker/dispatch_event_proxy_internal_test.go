package worker

// dispatch_event_proxy_internal_test.go — focused tests for the worker-side
// dispatch-event proxy (streamSubagentPersist.appendEvent) and the host-side
// handler (workerRunner.handleDispatchEvent + HostDispatchEventSink). Every
// append is driven through the proxy's encode/correlate/decode path against the
// actual host handler helper and a real host-owned SQLite store — the exact
// wiring the drain loop and the server's SetDispatchEventSink use — proving:
//
//  1. a real DB roundtrip: appended evidence is durable, ordered, and complete,
//  2. conversation-scope rejection (only this turn's conversation and children
//     created on the stream may carry evidence),
//  3. storage errors: duplicates report conversation+seq without clobbering the
//     earlier snapshot, other store failures cross the wire sanitized,
//  4. cancellation safety and stray deliveries,
//  5. multiple/concurrent events each get their own acknowledged round-trip.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	pkgcfg "cercano/source/server/pkg/config"
	pkgproto "cercano/source/server/pkg/proto"
)

// openDispatchEventTestStore opens a real in-memory SQLite conversation store
// (dispatch evidence's production owner) with EnsureConversation rows for
// convIDs, and returns the narrow DispatchEventStore slice the host wiring
// type-asserts in production.
func openDispatchEventTestStore(t *testing.T, convIDs ...string) conversation.DispatchEventStore {
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
	ds, ok := store.(conversation.DispatchEventStore)
	if !ok {
		t.Fatalf("conversation store does not implement DispatchEventStore: %T", store)
	}
	return ds
}

// newHostBackedDispatchProxy builds a streamSubagentPersist whose emit is
// answered by running the request through the ACTUAL host handler helper —
// workerRunner.handleDispatchEvent with the HostDispatchEventSink adapter —
// mirroring exactly what the host's RunTurn drain loop does for a
// WorkerToHost_DispatchEvent, and delivering the acknowledged response back to
// the proxy's pending channel.
func newHostBackedDispatchProxy(t *testing.T, ds conversation.DispatchEventStore, scope map[string]bool) (*streamSubagentPersist, *workerRunner) {
	t.Helper()
	host := &workerRunner{dispatchEventSink: HostDispatchEventSink(ds)}
	p := newStreamSubagentPersist(nil, 0)
	p.sendFn = func(m *pkgproto.WorkerToHost) {
		req := m.GetDispatchEvent()
		if req == nil {
			t.Errorf("proxy sent non-dispatch-event message: %v", m)
			return
		}
		p.deliverDispatchEvent(host.handleDispatchEvent(context.Background(), req, scope))
	}
	return p, host
}

// TestDispatchEventProxy_AppendThroughHostStore proves the full proxy path
// against the real SQLite store: fields round-trip intact, events stay in seq
// order, and a zero timestamp is stamped host-side.
func TestDispatchEventProxy_AppendThroughHostStore(t *testing.T) {
	ctx := context.Background()
	ds := openDispatchEventTestStore(t, "main1")
	p, _ := newHostBackedDispatchProxy(t, ds, map[string]bool{"main1": true})

	stamp := time.Unix(1700000000, 0).UTC()
	events := []conversation.DispatchEvent{
		{ConversationID: "main1", Seq: 1, Kind: "dispatch_started", Iteration: 0, Timestamp: stamp, PayloadJSON: `{"task":"ship it","agents":["a","b"]}`},
		{ConversationID: "main1", Seq: 2, Kind: "agent_result", Iteration: 1, Timestamp: stamp, PayloadJSON: `{"agent":"a","ok":true}`},
		{ConversationID: "main1", Seq: 3, Kind: "dispatch_done", Iteration: 1, Timestamp: stamp, PayloadJSON: `{"result":"done"}`},
	}
	for _, ev := range events {
		if err := p.appendEvent(ctx, ev); err != nil {
			t.Fatalf("appendEvent(seq=%d): %v", ev.Seq, err)
		}
	}

	// The evidence is durable in the host store — read directly, not via the
	// proxy, ordered by seq ascending.
	rows, err := ds.ListDispatchEvents(ctx, "main1")
	if err != nil {
		t.Fatalf("ListDispatchEvents: %v", err)
	}
	if len(rows) != len(events) {
		t.Fatalf("stored %d events, want %d", len(rows), len(events))
	}
	for i, want := range events {
		got := rows[i]
		if got.ConversationID != want.ConversationID || got.Seq != want.Seq ||
			got.Kind != want.Kind || got.Iteration != want.Iteration ||
			got.PayloadJSON != want.PayloadJSON || !got.Timestamp.Equal(stamp) {
			t.Fatalf("event %d = %+v, want %+v", i, got, want)
		}
	}

	// A zero timestamp is stamped host-side (host owns the clock).
	if err := p.appendEvent(ctx, conversation.DispatchEvent{
		ConversationID: "main1", Seq: 4, Kind: "host_stamp", Iteration: 0, PayloadJSON: `{}`,
	}); err != nil {
		t.Fatalf("appendEvent(host-stamped): %v", err)
	}
	rows, err = ds.ListDispatchEvents(ctx, "main1")
	if err != nil {
		t.Fatalf("ListDispatchEvents after host stamp: %v", err)
	}
	last := rows[len(rows)-1]
	if last.Seq != 4 || last.Timestamp.IsZero() || last.Timestamp.Before(stamp) {
		t.Fatalf("host-stamped event = %+v, want seq 4 with a current timestamp", last)
	}
}

// TestDispatchEventProxy_ScopeRejection proves the conversation scope: only
// this turn's conversation and child ids authorized on this stream may carry
// evidence; everything else is rejected before touching the store.
func TestDispatchEventProxy_ScopeRejection(t *testing.T) {
	ctx := context.Background()
	ds := openDispatchEventTestStore(t, "main1", "other", "child1")
	scope := map[string]bool{"main1": true} // exactly what RunTurn seeds
	p, _ := newHostBackedDispatchProxy(t, ds, scope)

	// A foreign conversation is rejected — and nothing lands in the store.
	err := p.appendEvent(ctx, conversation.DispatchEvent{ConversationID: "other", Seq: 1, Kind: "kind", PayloadJSON: `{}`})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("foreign conversation append = %v, want scope rejection", err)
	}
	if rows, listErr := ds.ListDispatchEvents(ctx, "other"); listErr != nil || len(rows) != 0 {
		t.Fatalf("rejected append leaked to store: rows=%d err=%v", len(rows), listErr)
	}

	// A child that exists in the DB but was NOT created on this stream is still
	// rejected: durability alone is not authorization.
	err = p.appendEvent(ctx, conversation.DispatchEvent{ConversationID: "child1", Seq: 1, Kind: "kind", PayloadJSON: `{}`})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("unauthorized child append = %v, want scope rejection", err)
	}

	// The drain loop authorizes a child after a successful EnsureSubagent whose
	// parent was already in scope (see the WorkerToHost_EnsureSubagent case);
	// mirroring that, the child's evidence now round-trips.
	scope["child1"] = true
	if err := p.appendEvent(ctx, conversation.DispatchEvent{ConversationID: "child1", Seq: 1, Kind: "agent_result", PayloadJSON: `{"ok":true}`}); err != nil {
		t.Fatalf("authorized child append: %v", err)
	}
	if rows, listErr := ds.ListDispatchEvents(ctx, "child1"); listErr != nil || len(rows) != 1 || rows[0].Kind != "agent_result" {
		t.Fatalf("authorized child evidence = %+v (err=%v), want one stored agent_result", rows, listErr)
	}

	// An unwired host runner (dial-injected test runner) errors clearly instead
	// of pretending the write happened.
	unwired := &workerRunner{}
	resp := unwired.handleDispatchEvent(ctx, &pkgproto.DispatchEventRequest{ConversationId: "main1", Seq: 1, Kind: "kind", PayloadJson: `{}`}, scope)
	if resp.GetError() != "dispatch event store is not available" {
		t.Fatalf("unwired host error = %q, want clear not-available message", resp.GetError())
	}
}

// TestDispatchEventProxy_StorageErrors covers the storage-error surfaces: a
// duplicate (conversation_id, seq) is reported through the wire with the
// earlier snapshot preserved, and other store failures cross the wire
// sanitized — driver internals never reach the worker.
func TestDispatchEventProxy_StorageErrors(t *testing.T) {
	ctx := context.Background()
	ds := openDispatchEventTestStore(t, "main1")
	p, _ := newHostBackedDispatchProxy(t, ds, map[string]bool{"main1": true})

	first := conversation.DispatchEvent{ConversationID: "main1", Seq: 1, Kind: "dispatch_started", PayloadJSON: `{"first":true}`}
	if err := p.appendEvent(ctx, first); err != nil {
		t.Fatalf("first append: %v", err)
	}

	// Duplicate (conversation, seq): reported with the well-formed report —
	// and the earlier snapshot is preserved, never overwritten.
	replay := first
	replay.PayloadJSON = `{"first":false,"replayed":true}`
	err := p.appendEvent(ctx, replay)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate append = %v, want duplicate report naming the conversation", err)
	}
	rows, listErr := ds.ListDispatchEvents(ctx, "main1")
	if listErr != nil || len(rows) != 1 || rows[0].PayloadJSON != `{"first":true}` {
		t.Fatalf("duplicate overwrote evidence: rows=%+v err=%v", rows, listErr)
	}

	// Other store failures (here: invalid payload JSON rejected by the store)
	// cross the wire as the stable sanitized message, not driver internals.
	err = p.appendEvent(ctx, conversation.DispatchEvent{ConversationID: "main1", Seq: 2, Kind: "kind", PayloadJSON: `not json`})
	if err == nil || err.Error() != "dispatch event append failed" {
		t.Fatalf("store failure = %v, want sanitized \"dispatch event append failed\"", err)
	}

	// A nil store wired through the production adapter errors clearly.
	nilSink := HostDispatchEventSink(nil)
	if err := nilSink(ctx, first); err == nil || !strings.Contains(err.Error(), "dispatch event store is not available") {
		t.Fatalf("nil store sink error = %v, want not-available message", err)
	}
}

// TestDispatchEventProxy_Cancellation proves cancellation safety: an
// already-canceled context unwinds immediately without sending, an unanswered
// request unwinds with the context error instead of hanging, and a late/stray
// delivery neither panics nor wedges the proxy.
func TestDispatchEventProxy_Cancellation(t *testing.T) {
	ctx := context.Background()
	ds := openDispatchEventTestStore(t, "main1")

	// Canceled before the call: nothing is sent.
	p := newStreamSubagentPersist(nil, 0)
	sent := false
	p.sendFn = func(*pkgproto.WorkerToHost) { sent = true }
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := p.appendEvent(cctx, conversation.DispatchEvent{ConversationID: "main1", Seq: 1, Kind: "kind", PayloadJSON: `{}`}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled append = %v, want context.Canceled", err)
	}
	if sent {
		t.Fatal("canceled append sent a request; it must not cross the stream")
	}

	// Canceled mid-flight: the host never answers; the caller unwinds with the
	// context error rather than hanging.
	p2 := newStreamSubagentPersist(nil, 0)
	p2.sendFn = func(*pkgproto.WorkerToHost) {} // host never responds
	dctx, dcancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer dcancel()
	err := p2.appendEvent(dctx, conversation.DispatchEvent{ConversationID: "main1", Seq: 1, Kind: "kind", PayloadJSON: `{}`})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered append = %v, want context.DeadlineExceeded", err)
	}
	if rows, listErr := ds.ListDispatchEvents(ctx, "main1"); listErr != nil || len(rows) != 0 {
		t.Fatalf("canceled append should leave no evidence, rows=%d err=%v", len(rows), listErr)
	}

	// A late/bogus delivery must not panic or wedge the proxy.
	p2.deliverDispatchEvent(nil)
	p2.deliverDispatchEvent(&pkgproto.DispatchEventResponse{Id: 99999})
}

// TestDispatchEventProxy_MultipleEvents proves many events — including
// concurrent ones — each get their own correlated acknowledged round-trip with
// no id collisions, no leaked pending entries, and ordered durable evidence.
func TestDispatchEventProxy_MultipleEvents(t *testing.T) {
	ctx := context.Background()
	ds := openDispatchEventTestStore(t, "main1")
	p, _ := newHostBackedDispatchProxy(t, ds, map[string]bool{"main1": true})

	// Capture request ids to prove correlation is per-event.
	var mu sync.Mutex
	ids := map[uint64]bool{}
	p.sendFn = func(m *pkgproto.WorkerToHost) {
		req := m.GetDispatchEvent()
		mu.Lock()
		if ids[req.GetId()] {
			t.Errorf("duplicate correlation id %d", req.GetId())
		}
		ids[req.GetId()] = true
		mu.Unlock()
		p.deliverDispatchEvent((&workerRunner{dispatchEventSink: HostDispatchEventSink(ds)}).
			handleDispatchEvent(ctx, req, map[string]bool{"main1": true}))
	}

	const n = 8
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			ev := conversation.DispatchEvent{
				ConversationID: "main1", Seq: int64(seq), Kind: "agent_result",
				Iteration: seq / 2, PayloadJSON: `{"agent":"x"}`,
			}
			if err := p.appendEvent(ctx, ev); err != nil {
				t.Errorf("concurrent append seq=%d: %v", seq, err)
			}
		}(i)
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("correlated %d ids, want %d", len(ids), n)
	}
	p.mu.Lock()
	leaked := len(p.pending)
	p.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("pending map leaked %d entries after completion", leaked)
	}

	rows, err := ds.ListDispatchEvents(ctx, "main1")
	if err != nil {
		t.Fatalf("ListDispatchEvents: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("stored %d events, want %d", len(rows), n)
	}
	for i, row := range rows {
		if row.Seq != int64(i+1) {
			t.Fatalf("rows out of order: row %d has seq %d", i, row.Seq)
		}
	}
}

func TestWorkerBuiltDispatchAutomaticallyPersistsEveryRun(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureConversation(t.Context(), "parent", "", "model"); err != nil {
		t.Fatal(err)
	}
	ds := store.(conversation.DispatchEventStore)
	host := &workerRunner{ensureSubagent: store.EnsureSubagentConversation, dispatchEventSink: HostDispatchEventSink(ds)}
	scope := map[string]bool{}
	proxy := newStreamSubagentPersist(nil, 0)
	proxy.sendFn = func(m *pkgproto.WorkerToHost) {
		if req := m.GetEnsureSubagent(); req != nil {
			if err := host.ensureDispatchChild(t.Context(), req, "parent", scope); err != nil {
				t.Error(err)
			}
		}
		if req := m.GetDispatchEvent(); req != nil {
			proxy.deliverDispatchEvent(host.handleDispatchEvent(t.Context(), req, scope))
		}
	}
	p := &compactRequestProbe{scriptedSummaryProvider: scriptedSummaryProvider{name: "probe"}}
	svc := buildWorkerToolSvc(nil, nil, projectctx.NewLoader(), nil, p, pkgcfg.Config{}, proxy, nil, nil, nil, nil).(*toolssvc.Service)
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		result, _ := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, ConversationID: "parent", Task: "Read source evidence", Tools: []string{"Read"}, MaxIterations: 1}, inference.Selection{Provider: p}, "model")
		if result.SubConversationID == "" || ids[result.SubConversationID] {
			t.Fatal("missing independent dispatch identity")
		}
		ids[result.SubConversationID] = true
		rows, err := ds.ListDispatchEvents(t.Context(), result.SubConversationID)
		if err != nil {
			t.Fatal(err)
		}
		kinds := map[string]bool{}
		for _, row := range rows {
			kinds[row.Kind] = true
		}
		for _, kind := range []string{"dispatch_start", "model_request", "model_response", "dispatch_done"} {
			if !kinds[kind] {
				t.Fatalf("missing %s from worker-built dispatch", kind)
			}
		}
	}
}
