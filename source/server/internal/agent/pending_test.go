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
		p.Resolve("u1", true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	allow, err := p.Wait(ctx, "u1")
	if err != nil || !allow {
		t.Errorf("expected allow=true, err=nil; got %v %v", allow, err)
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
