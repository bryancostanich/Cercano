package broker

import (
	"fmt"
	"testing"
	"time"

	"cercano/source/server/internal/runner"
)

// TestAttachLosslessWithBarrierAndFinish_MultiTurnDeliveryAndFinalSeal is the
// combined-subscription regression guard: ONE subscription serves an
// autonomous loop of several turns. Each turn's barrier fences the events
// published so far (it cannot open while earlier events are still undelivered),
// and only the single finish call at request end seals the subscription.
// Events published between the last barrier and finish still arrive, and no
// event is dropped or reordered across turns.
func TestAttachLosslessWithBarrierAndFinish_MultiTurnDeliveryAndFinalSeal(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(t.Context(), "combined")
	defer release()

	_, ch, barrier, finish, detach := b.AttachLosslessWithBarrierAndFinish("combined")
	defer detach()

	// --- Turn 0: strict ordering proof. ---
	// Publish more events than the subscriber channel's buffer: the drain
	// goroutine blocks on its forward once the buffer is full, so the barrier
	// CANNOT open while earlier events are undelivered.
	const overflow = 5
	for i := 0; i < subChanCap+overflow; i++ {
		b.Publish("combined", gen, runner.Event{Kind: runner.EventToken, Text: fmt.Sprint(i)})
	}
	gate0 := barrier()
	select {
	case <-gate0:
		t.Fatal("barrier opened before all earlier events were delivered")
	case <-time.After(100 * time.Millisecond):
	}
	// Drain everything: events arrive in order, then the gate opens.
	for i := 0; i < subChanCap+overflow; i++ {
		select {
		case ev := <-ch:
			if ev.Text != fmt.Sprint(i) {
				t.Fatalf("event out of order: got %q want %q", ev.Text, fmt.Sprint(i))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("stalled at event %d", i)
		}
	}
	select {
	case <-gate0:
	case <-time.After(5 * time.Second):
		t.Fatal("barrier never opened after its fence was satisfied")
	}
	// Nothing may leak past the closed gate before the next turn publishes.
	select {
	case ev := <-ch:
		t.Fatalf("event %v crossed a closed barrier", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// --- Turns 1..2: subsequent turns keep using the SAME subscription. ---
	next := subChanCap + overflow
	for turn := 1; turn <= 2; turn++ {
		for i := 0; i < 3; i++ {
			b.Publish("combined", gen, runner.Event{Kind: runner.EventToken, Text: fmt.Sprint(next)})
			next++
		}
		gate := barrier()
		for i := 0; i < 3; i++ {
			select {
			case ev := <-ch:
				want := next - 3 + i
				if ev.Text != fmt.Sprint(want) {
					t.Fatalf("turn %d: event out of order: got %q want %q", turn, ev.Text, fmt.Sprint(want))
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("turn %d: stalled at event %d", turn, next-3+i)
			}
		}
		select {
		case <-gate:
		case <-time.After(5 * time.Second):
			t.Fatalf("turn %d: barrier never opened", turn)
		}
		select {
		case ev := <-ch:
			t.Fatalf("turn %d: event %v crossed a closed barrier", turn, ev)
		case <-time.After(50 * time.Millisecond):
		}
	}

	// --- Request end: the single finish seals the subscription. ---
	// An event published between the last barrier and finish must still be
	// delivered; events published after finish must never be.
	b.Publish("combined", gen, runner.Event{Kind: runner.EventProgress, Text: "final"})
	finish()
	finish() // idempotent by contract
	b.Publish("combined", gen, runner.Event{Kind: runner.EventToken, Text: "after seal"})
	select {
	case ev := <-ch:
		if ev.Kind != runner.EventProgress || ev.Text != "final" {
			t.Fatalf("unexpected event after seal: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("final event never delivered")
	}
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("subscription not sealed after finish: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("finished subscription never closed")
	}
}
