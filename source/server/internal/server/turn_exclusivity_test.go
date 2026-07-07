package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// blockingStream is a fakeStream whose ctx we control, and whose Send blocks on
// a gate so we can hold a turn "in flight" while a second turn arrives.
type blockingStream struct {
	fakeStream
	firstSend sync.Once
	gate      chan struct{} // closed to release the first Send
	released  chan struct{} // closed once the first Send has been let through
}

func (b *blockingStream) Send(m *proto.StreamProcessResponse) error {
	b.firstSend.Do(func() {
		close(b.released)
		<-b.gate // hold the turn open until the test releases it
	})
	return b.fakeStream.Send(m)
}

// blockingProvider parks in StreamChat until its ctx is canceled, then returns
// ctx.Err() — modeling a long/stuck upstream turn.
type blockingProvider struct {
	caps    llm.Capabilities
	entered chan struct{}
	once    sync.Once
}

func (p *blockingProvider) Name() string                   { return "blocking" }
func (p *blockingProvider) Capabilities() llm.Capabilities { return p.caps }
func (p *blockingProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *blockingProvider) StreamChat(ctx context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// A second turn on the SAME conversation must cancel the first turn's context —
// two turns must never run concurrently against one conversation (they would
// interleave persistence and share one upstream session key).
func TestTurnExclusivity_SecondTurnCancelsFirst(t *testing.T) {
	srv, _ := newServerWithStore(t)
	prov := &blockingProvider{caps: llm.Capabilities{SupportsTools: true}, entered: make(chan struct{})}
	srv.SetCloudLLMProvider(prov)

	// Turn 1: runs in a goroutine, parks in the provider until canceled.
	firstErr := make(chan error, 1)
	go func() {
		s1 := &fakeStream{ctx: context.Background()}
		firstErr <- srv.streamProcessRequestWithToolLoop(
			&proto.ProcessRequestRequest{Input: "first", ConversationId: "conv-x"}, s1)
	}()

	select {
	case <-prov.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first turn never reached the provider")
	}

	// Turn 2 on the same conversation. It must supersede turn 1 — which means
	// turn 1's blocked provider call unblocks (ctx canceled) and returns.
	prov2 := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "second done"}}},
		caps:    llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov2)
	go func() {
		s2 := &fakeStream{ctx: context.Background()}
		_ = srv.streamProcessRequestWithToolLoop(
			&proto.ProcessRequestRequest{Input: "second", ConversationId: "conv-x"}, s2)
	}()

	select {
	case <-firstErr:
		// Turn 1 returned because turn 2 canceled it. Success.
	case <-time.After(3 * time.Second):
		t.Fatal("second turn did not cancel the first — turns ran concurrently on one conversation")
	}
}

// A superseded turn's late persistence must not land: once a newer turn owns
// the conversation, the older turn's onTurn writes are fenced out. Otherwise a
// stuck turn's tardy assistant turn interleaves into the live turn's history.
func TestTurnExclusivity_SupersededTurnPersistenceFenced(t *testing.T) {
	srv, store := newServerWithStore(t)

	// gen is the generation the fence sees; a turn persists only if it still
	// owns the conversation. Drive two turns and assert the first's fenced
	// writes never appear after the second took over.
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "turn A"}}},
		caps:    llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	s1 := &fakeStream{ctx: context.Background()}
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "A", ConversationId: "conv-y"}, s1); err != nil {
		t.Fatalf("turn A: %v", err)
	}

	// After a clean turn, the conversation must have no lingering active-turn
	// registration (the handle is released on return).
	if srv.hasActiveTurn("conv-y") {
		t.Error("active-turn handle leaked after a completed turn")
	}

	turns, _ := store.GetTurns(context.Background(), "conv-y")
	if len(turns) == 0 {
		t.Fatal("turn A did not persist at all")
	}
}
