package recap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestProducerCloseCancelsAndBoundsWait(t *testing.T) {
	g := New(nil, nil, time.Hour, 1)
	g.Schedule("queued")
	ctx, release, ok := g.startWork(t.Context())
	if !ok {
		t.Fatal("initial work rejected")
	}
	deadline, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.Close(deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded close=%v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("active work not canceled")
	}
	release()
	if err := g.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	g.Schedule("late")
	if _, _, ok = g.startWork(t.Context()); ok {
		t.Fatal("work admitted after close")
	}
	g.mu.Lock()
	pending := len(g.timers)
	g.mu.Unlock()
	if pending != 0 {
		t.Fatalf("timers survived close: %d", pending)
	}
}
func TestProducerConcurrentStartAndClose(t *testing.T) {
	g := New(nil, nil, time.Hour, 1)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				if _, done, ok := g.startWork(t.Context()); ok {
					done()
				}
			}
		}()
	}
	if err := g.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
}
