package credentials

import (
	"cercano/source/server/internal/secrets"
	"context"
	"sync"
	"testing"
)

type observedLoginContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

func (c *observedLoginContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}
func TestCanceledQueuedLoginDoesNotSupersedeLiveAttempt(t *testing.T) {
	s := New(secrets.NewMemory())
	live, err := s.BeginLogin(context.Background(), "work", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	p := s.profile("work")
	p.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	probe := &observedLoginContext{Context: ctx, checked: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		attempt, err := s.BeginLogin(probe, "work", "anthropic")
		if attempt != nil {
			attempt.Close()
		}
		result <- err
	}()
	<-probe.checked
	cancel()
	p.mu.Unlock()
	if err := <-result; err != context.Canceled {
		t.Fatalf("queued canceled attempt accepted: %v", err)
	}
	if live.Context().Err() != nil {
		t.Fatal("canceled caller superseded another active login")
	}
}
