package telemetry

import (
	"context"
	"sync/atomic"
	"testing"
)

type blockedAccountingProbeStore struct {
	Store
	entered chan struct{}
	release chan struct{}
	count   atomic.Int64
}

func (s *blockedAccountingProbeStore) RecordEvent(context.Context, *Event) error {
	if s.count.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return nil
}

// Characterizes the legacy queue, not the new accounting contract. Synchronizing
// the first write makes overflow deterministic without sleeps or load simulation.
func TestLegacyAccountingOverflowProbe(t *testing.T) {
	s := &blockedAccountingProbeStore{entered: make(chan struct{}), release: make(chan struct{})}
	c := NewCollector(s, 1)
	c.Emit(NewEvent("first", "fake"))
	<-s.entered
	c.Emit(NewEvent("queued", "fake"))
	c.Emit(NewEvent("lost", "fake"))
	close(s.release)
	c.Close()
	t.Logf("emitted=3 persisted=%d; legacy collector exposes no loss counter", s.count.Load())
	if s.count.Load() != 2 {
		t.Fatalf("expected demonstrated one-event loss, persisted=%d", s.count.Load())
	}
}
