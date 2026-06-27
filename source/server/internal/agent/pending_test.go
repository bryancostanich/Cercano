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
		p.Resolve("u1", Decision{Allow: true})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d, err := p.Wait(ctx, "u1")
	if err != nil || !d.Allow {
		t.Errorf("expected allow=true, err=nil; got %v %v", d.Allow, err)
	}
}

func TestPendingCarriesPersist(t *testing.T) {
	p := NewPendingDecisions()
	go func() { p.Resolve("t1", Decision{Allow: true, Persist: true}) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d, err := p.Wait(ctx, "t1")
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
	_, err := p.Wait(ctx, "u1")
	if err == nil {
		t.Errorf("expected ctx timeout error")
	}
}
