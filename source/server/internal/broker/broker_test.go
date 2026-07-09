package broker

import (
	"context"
	"testing"
)

// A second turn on the same conversation supersedes the first: it cancels the
// first turn's context (so its in-flight provider call / tool loop unwinds) and
// retires the first's generation, so the first turn's persistence and event
// emission go quiet. Two turns never run "live" against one conversation.
func TestBeginTurn_SecondSupersedesFirst(t *testing.T) {
	b := New()

	ctx1, gen1, release1 := b.BeginTurn(context.Background(), "conv-x")
	defer release1()
	if !b.IsCurrent("conv-x", gen1) {
		t.Fatal("first turn should be current immediately after BeginTurn")
	}

	ctx2, gen2, release2 := b.BeginTurn(context.Background(), "conv-x")
	defer release2()

	// The first turn's context must now be canceled.
	select {
	case <-ctx1.Done():
	default:
		t.Error("superseding turn did not cancel the first turn's context")
	}
	if gen2 == gen1 {
		t.Errorf("second turn must get a new generation, got %d twice", gen1)
	}
	// Fence: first turn is no longer current, second is.
	if b.IsCurrent("conv-x", gen1) {
		t.Error("superseded generation still reads as current — its persistence would not be fenced")
	}
	if !b.IsCurrent("conv-x", gen2) {
		t.Error("the live turn's generation must read as current")
	}
	_ = ctx2
}

// A different conversation is independent: BeginTurn on conv-b must not cancel
// or supersede an active turn on conv-a.
func TestBeginTurn_DifferentConversationsIndependent(t *testing.T) {
	b := New()

	ctxA, genA, releaseA := b.BeginTurn(context.Background(), "conv-a")
	defer releaseA()
	_, _, releaseB := b.BeginTurn(context.Background(), "conv-b")
	defer releaseB()

	select {
	case <-ctxA.Done():
		t.Error("a turn on another conversation canceled conv-a's turn")
	default:
	}
	if !b.IsCurrent("conv-a", genA) {
		t.Error("conv-a's turn should still be current after a conv-b turn began")
	}
}

// release clears the active-turn registration — but only if it is still ours.
// A superseded turn's release must NOT remove the newer turn's handle.
func TestBeginTurn_ReleaseIsOwnershipScoped(t *testing.T) {
	b := New()

	_, _, release1 := b.BeginTurn(context.Background(), "conv-z")
	_, gen2, release2 := b.BeginTurn(context.Background(), "conv-z") // supersedes turn 1

	// Turn 1 (superseded) releasing must leave turn 2's registration intact.
	release1()
	if !b.HasActiveTurn("conv-z") {
		t.Error("superseded turn's release removed the live turn's handle")
	}
	if !b.IsCurrent("conv-z", gen2) {
		t.Error("live turn's generation was disturbed by the superseded turn's release")
	}

	// Turn 2 releasing clears it for good.
	release2()
	if b.HasActiveTurn("conv-z") {
		t.Error("active-turn handle leaked after the live turn released")
	}
}

// HasActiveTurn reflects registration: true while a turn is live, false after release.
func TestHasActiveTurn_ReflectsRegistration(t *testing.T) {
	b := New()

	if b.HasActiveTurn("conv-new") {
		t.Error("HasActiveTurn should be false before any turn")
	}

	_, _, release := b.BeginTurn(context.Background(), "conv-new")
	if !b.HasActiveTurn("conv-new") {
		t.Error("HasActiveTurn should be true while turn is live")
	}

	release()
	if b.HasActiveTurn("conv-new") {
		t.Error("HasActiveTurn should be false after release")
	}
}

// IsCurrent is false after supersession, true for the live gen only.
func TestIsCurrent_SupersessionFence(t *testing.T) {
	b := New()

	_, gen1, release1 := b.BeginTurn(context.Background(), "conv-fence")
	defer release1()

	_, gen2, release2 := b.BeginTurn(context.Background(), "conv-fence")
	defer release2()

	if b.IsCurrent("conv-fence", gen1) {
		t.Error("superseded gen1 must not be current")
	}
	if !b.IsCurrent("conv-fence", gen2) {
		t.Error("live gen2 must be current")
	}
	// A completely different (never-registered) gen must also be false.
	if b.IsCurrent("conv-fence", gen2+99) {
		t.Error("arbitrary gen must not be current")
	}
}
