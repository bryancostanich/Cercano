package launch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCompletionRejectsUnknownAndCanceled(t *testing.T) {
	for _, p := range []*Process{nil, {}, {pid: 123}} {
		if c, e := p.WaitForCompletion(context.Background()); !errors.Is(e, ErrCompletionUnknown) || c.For(p) {
			t.Fatal("unknown process accepted", e)
		}
	}
	p := &Process{pid: 123, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, e := p.WaitForCompletion(ctx); !errors.Is(e, context.Canceled) || c.For(p) {
		t.Fatal("canceled waiter accepted", e)
	}
	close(p.done)
	if c, e := p.WaitForCompletion(context.Background()); !errors.Is(e, ErrCompletionUnknown) || c.For(p) {
		t.Fatal("unproven wait accepted", e)
	}
}
func TestCompletionMultipleObserversExactIdentity(t *testing.T) {
	p := &Process{pid: 123, done: make(chan struct{}), exitCode: -1}
	other := &Process{pid: 123, done: make(chan struct{})}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := p.WaitForCompletion(context.Background())
			if e != nil || !c.For(p) || c.For(other) {
				t.Error("incorrect identity", e)
			}
			if code, ok := c.ExitCode(); !ok || code != 7 {
				t.Error("nonzero completion lost", code, ok)
			}
		}()
	}
	p.completed = true
	p.exitCode = 7
	close(p.done)
	wg.Wait()
	if (Completion{}).For(p) {
		t.Fatal("zero receipt accepted")
	}
}

// Used in both Unix direct-child and Windows owned-job fixture paths.
func checkOwnedCompletion(p *Process) error {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, e := p.WaitForCompletion(ctx); !errors.Is(e, context.Canceled) || c.For(p) {
		return errors.New("canceled completion wait yielded receipt")
	}
	wait, cancelWait := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWait()
	c, e := p.WaitForCompletion(wait)
	if e != nil {
		return e
	}
	if !c.For(p) || c.For(&Process{pid: p.Pid()}) {
		return errors.New("receipt does not bind exact owned handle")
	}
	if code, ok := c.ExitCode(); !ok || code != 0 {
		return errors.New("echo child completion status incorrect")
	}
	return nil
}
