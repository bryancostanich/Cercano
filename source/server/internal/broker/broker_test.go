package broker

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/runner"
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

// ---------------------------------------------------------------------------
// Task 2: fan-out, replay buffer, Attach
// ---------------------------------------------------------------------------

// TestBroker_FanOutToMultipleSubscribers: two Attach calls both receive all
// events published during the turn, in order.
func TestBroker_FanOutToMultipleSubscribers(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(context.Background(), "conv-fan")
	defer release()

	_, ch1, detach1 := b.Attach("conv-fan")
	defer detach1()
	_, ch2, detach2 := b.Attach("conv-fan")
	defer detach2()

	events := []runner.Event{
		{Kind: runner.EventToken, Text: "a"},
		{Kind: runner.EventToken, Text: "b"},
		{Kind: runner.EventToken, Text: "c"},
	}
	for _, ev := range events {
		b.Publish("conv-fan", gen, ev)
	}

	timeout := time.After(time.Second)
	for _, sub := range []<-chan runner.Event{ch1, ch2} {
		for i, want := range events {
			select {
			case got := <-sub:
				if got.Text != want.Text {
					t.Errorf("subscriber event[%d]: got %q, want %q", i, got.Text, want.Text)
				}
			case <-timeout:
				t.Fatalf("timed out waiting for event[%d] on subscriber", i)
			}
		}
	}
}

// TestBroker_AttachMidTurnReplaysThenLive: events published before Attach are
// in replay; events published after are on ch; union is all events, no dup, no gap.
func TestBroker_AttachMidTurnReplaysThenLive(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(context.Background(), "conv-midturn")
	defer release()

	b.Publish("conv-midturn", gen, runner.Event{Kind: runner.EventToken, Text: "pre1"})
	b.Publish("conv-midturn", gen, runner.Event{Kind: runner.EventToken, Text: "pre2"})

	// Attach after the 2 pre-events.
	replay, ch, detach := b.Attach("conv-midturn")
	defer detach()

	// Replay must have exactly the 2 pre-events.
	if len(replay) != 2 {
		t.Fatalf("replay len = %d, want 2", len(replay))
	}
	if replay[0].Text != "pre1" || replay[1].Text != "pre2" {
		t.Errorf("replay content mismatch: got %q %q", replay[0].Text, replay[1].Text)
	}

	// Publish a 3rd event after Attach.
	b.Publish("conv-midturn", gen, runner.Event{Kind: runner.EventToken, Text: "live1"})

	// Must arrive on ch, not in replay.
	select {
	case got := <-ch:
		if got.Text != "live1" {
			t.Errorf("live event: got %q, want %q", got.Text, "live1")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event on ch")
	}

	// No dup: "live1" must NOT be in replay.
	for _, ev := range replay {
		if ev.Text == "live1" {
			t.Error("live event appeared in replay (duplicate)")
		}
	}

	// No gap: "pre1"/"pre2" must NOT appear on ch (they are in replay only).
	// Drain ch with a short timeout — should be empty now.
	select {
	case extra := <-ch:
		if extra.Text == "pre1" || extra.Text == "pre2" {
			t.Errorf("pre-Attach event appeared on ch (gap/dup): %q", extra.Text)
		}
	case <-time.After(10 * time.Millisecond):
		// good: nothing unexpected on ch
	}
}

// TestBroker_BeginTurnResetsBuffer: a new BeginTurn clears the replay buffer;
// Attach after the new turn starts sees an empty replay.
func TestBroker_BeginTurnResetsBuffer(t *testing.T) {
	b := New()
	_, gen1, release1 := b.BeginTurn(context.Background(), "conv-reset")
	b.Publish("conv-reset", gen1, runner.Event{Kind: runner.EventToken, Text: "old"})
	release1()

	// Start a second turn — buffer must reset.
	_, _, release2 := b.BeginTurn(context.Background(), "conv-reset")
	defer release2()

	replay, _, detach := b.Attach("conv-reset")
	defer detach()

	if len(replay) != 0 {
		t.Errorf("replay after new BeginTurn: got %d events, want 0", len(replay))
	}
}

// TestBroker_PublishFencedByGen: publishing with a stale gen after supersession
// is a no-op — nothing appears in the buffer or on any subscriber channel.
func TestBroker_PublishFencedByStaleGen(t *testing.T) {
	b := New()
	_, gen1, release1 := b.BeginTurn(context.Background(), "conv-stale")
	defer release1()

	// Supersede immediately.
	_, gen2, release2 := b.BeginTurn(context.Background(), "conv-stale")
	defer release2()

	// Attach to the new turn BEFORE publishing with the stale gen.
	replay, ch, detach := b.Attach("conv-stale")
	defer detach()

	// Publish with the OLD (stale) gen — must be fenced.
	b.Publish("conv-stale", gen1, runner.Event{Kind: runner.EventToken, Text: "stale"})

	// Buffer (visible via replay on re-attach) should be empty.
	if len(replay) != 0 {
		t.Errorf("stale publish reached buffer: %d events", len(replay))
	}

	// Channel should be empty.
	select {
	case ev := <-ch:
		t.Errorf("stale publish reached subscriber channel: %q", ev.Text)
	case <-time.After(10 * time.Millisecond):
		// good
	}

	_ = gen2
}

// TestBroker_DetachStopsDelivery: after detach(), further Publish must not
// deliver to that channel and must not block the publisher.
func TestBroker_DetachStopsDelivery(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(context.Background(), "conv-detach")
	defer release()

	_, ch, detach := b.Attach("conv-detach")

	// Detach before any publish.
	detach()

	// This must not block or panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10; i++ {
			b.Publish("conv-detach", gen, runner.Event{Kind: runner.EventToken, Text: "after-detach"})
		}
	}()

	select {
	case <-done:
		// good: publisher returned without blocking
	case <-time.After(time.Second):
		t.Fatal("Publish blocked after detach — publisher stalled")
	}

	// Channel should be closed and drained (no events after detach).
	select {
	case ev, ok := <-ch:
		if ok {
			t.Errorf("received event on detached channel: %q", ev.Text)
		}
		// closed channel, fine
	default:
		// also fine: nothing to receive
	}
}
