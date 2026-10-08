package broker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cercano/source/server/internal/runner"
)

func TestLosslessDeliveryBarrierWaitsForForwarding(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(context.Background(), "barrier")
	defer release()
	_, ch, barrier, detach := b.AttachLosslessWithBarrier("barrier")
	defer detach()
	// More than the output capacity guarantees forwarding is still pending,
	// independently of goroutine scheduling. Repeat to cover continuation turns.
	for round := 0; round < 3; round++ {
		const n = subChanCap*3 + 1
		for i := 0; i < n; i++ {
			b.Publish("barrier", gen, runner.Event{Kind: runner.EventToken, Text: fmt.Sprint(i)})
		}
		ack := barrier()
		select {
		case <-ack:
			t.Fatal("acknowledged before queued events could be forwarded")
		default:
		}
		timer := time.NewTimer(5 * time.Second)
		got := 0
		receive := func(ev runner.Event) {
			t.Helper()
			if ev.Text != fmt.Sprint(got) {
				t.Fatalf("event %d: got %q", got, ev.Text)
			}
			got++
		}
	wait:
		for {
			select {
			case ev := <-ch:
				receive(ev)
			case <-ack:
				break wait
			case <-timer.C:
				t.Fatal("delivery barrier did not finish")
			}
		}
		timer.Stop()
	drain:
		for {
			select {
			case ev := <-ch:
				receive(ev)
			default:
				break drain
			}
		}
		if got != n {
			t.Fatalf("delivered %d/%d events", got, n)
		}
	}
	select {
	case <-barrier():
	case <-time.After(5 * time.Second):
		t.Fatal("empty barrier did not finish")
	}
}

func TestLosslessDeliveryBarrierDetachWhileForwardingBlocked(t *testing.T) {
	b := New()
	_, gen, release := b.BeginTurn(context.Background(), "cancel-barrier")
	defer release()
	_, ch, barrier, detach := b.AttachLosslessWithBarrier("cancel-barrier")
	defer detach()
	for i := 0; i < subChanCap*3; i++ {
		b.Publish("cancel-barrier", gen, runner.Event{Kind: runner.EventToken})
	}
	_ = barrier()
	detach()
	detach() // Still idempotent with a pending barrier.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			} // Forwarding goroutine exited, even with a blocked tail.
		case <-timer.C:
			t.Fatal("detach leaked the forwarding goroutine")
		}
	}
}
