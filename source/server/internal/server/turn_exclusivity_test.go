package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// A second turn on the same conversation supersedes the first: it cancels the
// first turn's context (so its in-flight provider call / tool loop unwinds) and
// retires the first's generation, so the first turn's persistence and event
// emission go quiet. Two turns never run "live" against one conversation.
func TestBeginTurn_SecondSupersedesFirst(t *testing.T) {
	srv, _ := newServerWithStore(t)

	ctx1, gen1, release1 := srv.beginTurn(context.Background(), "conv-x")
	defer release1()
	if !srv.turnIsCurrent("conv-x", gen1) {
		t.Fatal("first turn should be current immediately after beginTurn")
	}

	ctx2, gen2, release2 := srv.beginTurn(context.Background(), "conv-x")
	defer release2()

	// The first turn's context must now be canceled — its blocked provider
	// call / tool loop unwinds without any waiting.
	select {
	case <-ctx1.Done():
	default:
		t.Error("superseding turn did not cancel the first turn's context")
	}
	if gen2 == gen1 {
		t.Errorf("second turn must get a new generation, got %d twice", gen1)
	}
	// The fence: the first turn is no longer current, the second is.
	if srv.turnIsCurrent("conv-x", gen1) {
		t.Error("superseded generation still reads as current — its persistence would not be fenced")
	}
	if !srv.turnIsCurrent("conv-x", gen2) {
		t.Error("the live turn's generation must read as current")
	}
	_ = ctx2
}

// A different conversation is independent: beginTurn on conv-b must not cancel
// or supersede an active turn on conv-a.
func TestBeginTurn_DifferentConversationsIndependent(t *testing.T) {
	srv, _ := newServerWithStore(t)

	ctxA, genA, releaseA := srv.beginTurn(context.Background(), "conv-a")
	defer releaseA()
	_, _, releaseB := srv.beginTurn(context.Background(), "conv-b")
	defer releaseB()

	select {
	case <-ctxA.Done():
		t.Error("a turn on another conversation canceled conv-a's turn")
	default:
	}
	if !srv.turnIsCurrent("conv-a", genA) {
		t.Error("conv-a's turn should still be current after a conv-b turn began")
	}
}

// release clears the active-turn registration — but only if it is still ours.
// A superseded turn's release must NOT remove the newer turn's handle.
func TestBeginTurn_ReleaseIsOwnershipScoped(t *testing.T) {
	srv, _ := newServerWithStore(t)

	_, _, release1 := srv.beginTurn(context.Background(), "conv-z")
	_, gen2, release2 := srv.beginTurn(context.Background(), "conv-z") // supersedes turn 1

	// Turn 1 (superseded) releasing must leave turn 2's registration intact.
	release1()
	if !srv.hasActiveTurn("conv-z") {
		t.Error("superseded turn's release removed the live turn's handle")
	}
	if !srv.turnIsCurrent("conv-z", gen2) {
		t.Error("live turn's generation was disturbed by the superseded turn's release")
	}

	// Turn 2 releasing clears it for good.
	release2()
	if srv.hasActiveTurn("conv-z") {
		t.Error("active-turn handle leaked after the live turn released")
	}
}

// A completed real turn through the handler leaves no active-turn registration
// (the deferred release fires on return) and persists its turn.
func TestTurnExclusivity_CompletedTurnReleasesAndPersists(t *testing.T) {
	srv, store := newServerWithStore(t)
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

	if srv.hasActiveTurn("conv-y") {
		t.Error("active-turn handle leaked after a completed turn")
	}
	turns, _ := store.GetTurns(context.Background(), "conv-y")
	if len(turns) == 0 {
		t.Fatal("turn A did not persist at all")
	}
}
