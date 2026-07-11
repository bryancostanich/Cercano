package agent

import (
	"context"
	"testing"
	"time"
)

func TestPending_WaitResolves(t *testing.T) {
	p := NewPendingDecisions()
	go func() {
		time.Sleep(10 * time.Millisecond)
		p.Resolve("c1", "u1", Decision{Allow: true})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d, err := p.Wait(ctx, "c1", "u1")
	if err != nil || !d.Allow {
		t.Errorf("expected allow=true, err=nil; got %v %v", d.Allow, err)
	}
}

func TestPendingCarriesPersist(t *testing.T) {
	p := NewPendingDecisions()
	go func() { p.Resolve("c1", "t1", Decision{Allow: true, Persist: true}) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d, err := p.Wait(ctx, "c1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow || !d.Persist {
		t.Fatalf("decision = %+v", d)
	}
}

func TestPending_WaitTimesOut(t *testing.T) {
	p := NewPendingDecisions()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Wait(ctx, "c1", "u1")
	if err == nil {
		t.Errorf("expected ctx timeout error")
	}
}

// TestPending_ConversationScoped verifies a decision reaches only the waiter in
// the matching conversation: two conversations register the SAME tool-use ID,
// and resolving one must not unblock or satisfy the other. This is the
// regression guard for the per-conversation keying.
func TestPending_ConversationScoped(t *testing.T) {
	p := NewPendingDecisions()

	type res struct {
		d   Decision
		err error
	}
	chA := make(chan res, 1)
	chB := make(chan res, 1)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() { d, err := p.Wait(ctx, "convA", "u1"); chA <- res{d, err} }()
	go func() { d, err := p.Wait(ctx, "convB", "u1"); chB <- res{d, err} }()

	// Let both waiters register under the identical tool-use ID.
	time.Sleep(20 * time.Millisecond)

	if ok := p.Resolve("convA", "u1", Decision{Allow: true}); !ok {
		t.Fatal("Resolve(convA) returned false; convA waiter not registered")
	}

	select {
	case r := <-chA:
		if r.err != nil || !r.d.Allow {
			t.Fatalf("convA: expected allow=true err=nil; got %+v %v", r.d, r.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("convA did not resolve")
	}

	// convB must still be blocked — convA's decision must not leak to it.
	select {
	case r := <-chB:
		t.Fatalf("convB resolved on convA's decision (cross-talk): %+v %v", r.d, r.err)
	case <-time.After(100 * time.Millisecond):
	}

	// convB resolves independently under its own conversation.
	if ok := p.Resolve("convB", "u1", Decision{Allow: false}); !ok {
		t.Fatal("Resolve(convB) returned false; convB waiter not registered")
	}
	select {
	case r := <-chB:
		if r.err != nil || r.d.Allow {
			t.Fatalf("convB: expected allow=false err=nil; got %+v %v", r.d, r.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("convB did not resolve after its own Resolve")
	}
}
