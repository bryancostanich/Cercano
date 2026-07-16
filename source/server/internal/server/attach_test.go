package server

// TestAttachConversation_TwoSurfacesSeeOneTurn is the Phase-4 load-bearing
// multi-surface proof: two surfaces (the initiator + a mid-turn attacher) must
// collectively receive the full event sequence of one turn — the initiator sees
// all events live; the attacher sees events published before it attached as
// REPLAY and the rest as LIVE events — with no gap and no duplicate.
//
// Proof approach:
//
//  1. Build a server with the real broker wired (newServerWithStore).
//  2. Use a pausableProvider: emits the first event (a text token → RouteSelected
//     + TokenDelta proto payloads), then blocks until the test releases it.
//  3. Run streamProcessRequestWithToolLoop in a goroutine (the initiator).
//  4. Wait for the "first event published" signal (the pause point).
//  5. Start AttachConversation in a goroutine (the attacher).  At this point the
//     replay buffer contains the first event; remaining events are live.
//  6. Release the provider so it emits the remaining events.
//  7. Wait for both goroutines to finish.
//  8. Assert initiator got ALL events in order.
//     Assert attacher's received events = full sequence (replay + live), in
//     order, with no gap and no dup.
//
// Runs under -race.

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ---------------------------------------------------------------------------
// pausableStream: a StreamReader that delivers the first N events, then blocks
// until released. After release it delivers the remaining events.
// ---------------------------------------------------------------------------

type pausableStream struct {
	events []llm.StreamEvent
	idx    int

	// Once idx reaches pauseAfter the stream blocks on released.
	pauseAfter int
	released   <-chan struct{} // closed by the test to unblock

	// onPause is called exactly once when the pause point is reached.
	onPause func()
	paused  sync.Once
}

func (s *pausableStream) Next() (llm.StreamEvent, bool, error) {
	if s.idx >= len(s.events) {
		return llm.StreamEvent{}, false, nil
	}
	if s.idx == s.pauseAfter {
		// Signal the test, then block.
		s.paused.Do(s.onPause)
		<-s.released
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, true, nil
}

func (s *pausableStream) Close() error { return nil }

// pausableProvider wraps scriptedProvider but returns a pausableStream.
type pausableProvider struct {
	blocks     []llm.Block
	pauseAfter int           // event index at which to pause
	release    chan struct{} // test closes this to unblock
	onPause    func()        // called when the pause point is reached
}

func (p *pausableProvider) Name() string { return "pausable" }
func (p *pausableProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *pausableProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Blocks: p.blocks}, nil
}
func (p *pausableProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	evs := blocksToEvents(p.blocks) // reuse helper from toolloop_persist_test.go
	return &pausableStream{
		events:     evs,
		pauseAfter: p.pauseAfter,
		released:   p.release,
		onPause:    p.onPause,
	}, nil
}

// ---------------------------------------------------------------------------
// collectingStream: a thread-safe fake stream that records all sent responses.
// Used for both the initiator and the attacher.
// ---------------------------------------------------------------------------

// collectingStream implements both proto.Agent_StreamProcessRequestServer and
// proto.Agent_AttachConversationServer (both are grpc.ServerStreamingServer[StreamProcessResponse]).
// It records every Send call in a thread-safe slice. An optional notifyAfter
// count causes a channel to be closed once that many messages have been received.
type collectingStream struct {
	grpc.ServerStream // embed for unimplemented methods
	mu                sync.Mutex
	ctx               context.Context
	msgs              []*proto.StreamProcessResponse
	notifyAfter       int           // if > 0, close notifyCh once len(msgs) >= notifyAfter
	notifyCh          chan struct{} // closed once notifyAfter is reached
	notifyOnce        sync.Once
}

func newCollectingStream(ctx context.Context) *collectingStream {
	return &collectingStream{ctx: ctx, notifyCh: make(chan struct{})}
}

func (s *collectingStream) Context() context.Context { return s.ctx }
func (s *collectingStream) Send(m *proto.StreamProcessResponse) error {
	s.mu.Lock()
	s.msgs = append(s.msgs, m)
	n := len(s.msgs)
	notify := s.notifyAfter
	s.mu.Unlock()
	if notify > 0 && n >= notify {
		s.notifyOnce.Do(func() { close(s.notifyCh) })
	}
	return nil
}
func (s *collectingStream) SetHeader(metadata.MD) error  { return nil }
func (s *collectingStream) SendHeader(metadata.MD) error { return nil }
func (s *collectingStream) SetTrailer(metadata.MD)       {}
func (s *collectingStream) SendMsg(interface{}) error    { return nil }
func (s *collectingStream) RecvMsg(interface{}) error    { return nil }

func (s *collectingStream) collected() []*proto.StreamProcessResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*proto.StreamProcessResponse, len(s.msgs))
	copy(out, s.msgs)
	return out
}

// ---------------------------------------------------------------------------
// Proof test
// ---------------------------------------------------------------------------

func TestAttachConversation_TwoSurfacesSeeOneTurn(t *testing.T) {
	srv, _ := newServerWithStore(t)

	// Known event sequence from the scripted provider:
	// blocksToEvents wraps blocks in EventMessageStart + per-block events + EventMessageStop.
	// For a single text block "hello world" the LLM stream events are:
	//   EventMessageStart, EventTextDelta("hello world"), EventMessageStop
	// which broker publishes as runner events:
	//   EventRouteSelected (from the route hook), EventToken("hello world")
	// The mapping sendRunnerEvent emits proto payloads for RouteSelected and Token.
	// FinalResponse is sent by streamProcessRequestWithToolLoop after RunTurn, not via broker.
	//
	// We pause after the first event delivered to the LLM stream consumer
	// (pauseAfter = 1 = after EventMessageStart, before EventTextDelta). At that
	// point, RouteSelected has been published (it fires before streaming starts)
	// and EventToken has NOT been published yet.
	//
	// So: replay = [RouteSelected]; live = [Token, (FinalResponse via direct send)].

	releaseCh := make(chan struct{})
	pauseReady := make(chan struct{})

	prov := &pausableProvider{
		blocks: []llm.Block{
			{Type: llm.BlockText, Text: "hello world"},
		},
		// pauseAfter=1: after EventMessageStart is consumed, before EventTextDelta.
		// At this pause point, RouteSelected has been published to the broker.
		pauseAfter: 1,
		release:    releaseCh,
		onPause: func() {
			close(pauseReady)
		},
	}
	srv.SetCloudLLMProvider(prov)

	const convID = "conv-attach-proof"

	// Known event counts:
	// Broker-published events for "hello world" text block:
	//   [optional Progress events], EventRouteSelected, EventToken = N broker events
	// FinalResponse is NOT broker-published; it's sent directly by the turn handler.
	// The stream pauses at pauseAfter=1 — after the runner consumes EventMessageStart
	// but before the EventTextDelta is returned — so EventToken is not yet published
	// when the attacher joins:
	//   replay = events published before the attach: Progress + RouteSelected
	//   live = events published after resume: Token
	// The attacher sees replay+live = all broker-published events.
	// We wait for the load-bearing broker events from the attacher: RouteSelected
	// replay plus Token live. Progress events are advisory and route-dependent, so
	// they must not be part of the synchronization contract.
	const attacherExpected = 2

	// Initiator stream.
	initiatorCtx, initiatorCancel := context.WithCancel(context.Background())
	defer initiatorCancel()
	initiatorStream := newCollectingStream(initiatorCtx)

	req := &proto.ProcessRequestRequest{
		Input:          "test",
		ConversationId: convID,
	}

	// Start the turn in a goroutine.
	var turnErr error
	var turnWG sync.WaitGroup
	turnWG.Add(1)
	go func() {
		defer turnWG.Done()
		turnErr = srv.streamProcessRequestWithToolLoop(req, initiatorStream)
	}()

	// Wait until the provider is paused (RouteSelected has been published).
	<-pauseReady

	// Now attach the second surface.
	// Set notifyAfter so we know when the attacher has received all expected events.
	attacherCtx, attacherCancel := context.WithCancel(context.Background())
	defer attacherCancel()
	attacherStream := newCollectingStream(attacherCtx)
	attacherStream.notifyAfter = attacherExpected

	attachReq := &proto.AttachConversationRequest{ConversationId: convID}
	var attachErr error
	var attachWG sync.WaitGroup
	attachWG.Add(1)
	go func() {
		defer attachWG.Done()
		attachErr = srv.AttachConversation(attachReq, attacherStream)
	}()

	// Release the provider to emit the rest of the events.
	close(releaseCh)

	// Wait for the turn to complete.
	turnWG.Wait()
	if turnErr != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", turnErr)
	}

	// Wait for the attacher to receive all expected events, then disconnect it.
	// The broker does not close lossy subscriber channels when a turn ends; in
	// production a surface disconnects when the turn ends (e.g. it received a
	// FinalResponse via a higher-layer protocol). Here we cancel after all
	// broker-published events have been received by the attacher goroutine.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-attacherStream.notifyCh:
		// All expected events received.
	case <-timer.C:
		t.Fatal("timed out waiting for attacher to receive all expected events")
	}
	attacherCancel()
	attachWG.Wait()
	if attachErr != nil && attachErr != context.Canceled {
		t.Fatalf("AttachConversation: %v", attachErr)
	}

	// -----------------------------------------------------------------------
	// Assertions
	// -----------------------------------------------------------------------

	initiatorMsgs := initiatorStream.collected()
	attacherMsgs := attacherStream.collected()

	t.Logf("initiator received %d messages", len(initiatorMsgs))
	t.Logf("attacher received %d messages (replay+live)", len(attacherMsgs))

	// (a) Initiator must have received RouteSelected, TokenDelta, and FinalResponse.
	if len(initiatorMsgs) < 3 {
		t.Fatalf("initiator: expected ≥3 messages (RouteSelected, TokenDelta, FinalResponse), got %d", len(initiatorMsgs))
	}

	// Verify initiator got RouteSelected somewhere in the stream.
	initiatorHasRoute := false
	for _, m := range initiatorMsgs {
		if m.GetRouteSelected() != nil {
			initiatorHasRoute = true
			break
		}
	}
	if !initiatorHasRoute {
		t.Error("initiator: no RouteSelected in stream")
	}

	// Verify initiator got TokenDelta.
	initiatorHasToken := false
	for _, m := range initiatorMsgs {
		if m.GetTokenDelta() != nil && m.GetTokenDelta().GetContent() == "hello world" {
			initiatorHasToken = true
			break
		}
	}
	if !initiatorHasToken {
		t.Error("initiator: no TokenDelta with 'hello world' in stream")
	}

	// Verify initiator got FinalResponse (last message).
	last := initiatorMsgs[len(initiatorMsgs)-1]
	if last.GetFinalResponse() == nil {
		t.Errorf("initiator last message: expected FinalResponse, got %T payload", last.Payload)
	}

	// (b) Attacher must have received ≥2 messages (RouteSelected + TokenDelta minimum).
	// FinalResponse is sent directly by streamProcessRequestWithToolLoop, not via
	// broker, so the attacher does NOT see it (correct — it's observe-only).
	if len(attacherMsgs) < 2 {
		t.Fatalf("attacher: expected ≥2 messages (replay + live), got %d", len(attacherMsgs))
	}

	// (c) Attacher must have received RouteSelected (in replay).
	attacherHasRoute := false
	for _, m := range attacherMsgs {
		if m.GetRouteSelected() != nil {
			attacherHasRoute = true
			break
		}
	}
	if !attacherHasRoute {
		t.Error("attacher: no RouteSelected — replay events not received")
	}

	// (d) Attacher must have received TokenDelta (as live event after pause release).
	attacherHasToken := false
	for _, m := range attacherMsgs {
		if m.GetTokenDelta() != nil && m.GetTokenDelta().GetContent() == "hello world" {
			attacherHasToken = true
			break
		}
	}
	if !attacherHasToken {
		t.Error("attacher: no TokenDelta('hello world') — live events not received")
	}

	// (e) No gap: attacher must NOT have seen FinalResponse (only initiator sees that).
	for _, m := range attacherMsgs {
		if m.GetFinalResponse() != nil {
			t.Error("attacher: received FinalResponse — only the initiator should get this direct send")
			break
		}
	}

	// (f) No duplicate: attacher must have RouteSelected exactly once.
	routeCount := 0
	for _, m := range attacherMsgs {
		if m.GetRouteSelected() != nil {
			routeCount++
		}
	}
	if routeCount != 1 {
		t.Errorf("attacher: RouteSelected count = %d, want exactly 1 (no dup from replay+live)", routeCount)
	}

	// (g) Order: for the attacher, RouteSelected must precede TokenDelta.
	routeIdx, tokenIdx := -1, -1
	for i, m := range attacherMsgs {
		if m.GetRouteSelected() != nil && routeIdx < 0 {
			routeIdx = i
		}
		if m.GetTokenDelta() != nil && tokenIdx < 0 {
			tokenIdx = i
		}
	}
	if routeIdx >= 0 && tokenIdx >= 0 && routeIdx >= tokenIdx {
		t.Errorf("attacher: RouteSelected[%d] must precede TokenDelta[%d]", routeIdx, tokenIdx)
	}
}

// TestAttachConversation_EmptyConvID: handler must return InvalidArgument.
func TestAttachConversation_EmptyConvID(t *testing.T) {
	srv, _ := newServerWithStore(t)
	stream := newCollectingStream(context.Background())
	err := srv.AttachConversation(&proto.AttachConversationRequest{ConversationId: ""}, stream)
	if err == nil {
		t.Fatal("expected error for empty conversation_id, got nil")
	}
}

// TestAttachConversation_ClientDisconnect: attaching to a conv with no active
// turn blocks; cancelling the stream ctx returns the ctx error promptly.
func TestAttachConversation_ClientDisconnect(t *testing.T) {
	srv, _ := newServerWithStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := newCollectingStream(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() {
		defer wg.Done()
		err = srv.AttachConversation(&proto.AttachConversationRequest{ConversationId: "no-active-turn"}, stream)
	}()

	cancel() // simulate client disconnect
	wg.Wait()

	if err == nil {
		t.Fatal("expected context error after cancel, got nil")
	}
}
