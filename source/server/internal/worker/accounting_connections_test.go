package worker

import (
	"context"
	"testing"
	"time"
)

func TestCompletedAccountingSessionIsNotDegradedByExpiredClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for i := 0; i < 32; i++ {
		sink := &immediateAccountingReceiver{}
		s := &hostAccountingSession{sink: sink, done: make(chan struct{}), drain: make(chan struct{}), cancel: func() {}}
		close(s.done)
		if err := s.close(ctx); err != nil {
			t.Fatalf("already-completed session was degraded: %v", err)
		}
		if sink.incomplete {
			t.Fatal("invented a coverage gap after completed drain")
		}
	}
}

func TestIdleAccountingRetirementRunsOutsidePoolLock(t *testing.T) {
	pool := newWorkerPool(nil)
	now := time.Unix(100, 0)
	pool.now = func() time.Time { return now }
	drained := false
	killed := false
	idle := &workerHandle{onKill: func() {
		if !drained {
			t.Error("idle worker killed before drain")
		}
		killed = true
	}}
	active := &workerHandle{onKill: func() { t.Error("active worker reaped") }}
	pool.byConv["idle"] = &pooledEntry{handle: idle, lastUsed: now.Add(-20 * time.Second)}
	pool.byConv["active"] = &pooledEntry{handle: active, inUse: true, lastUsed: now.Add(-20 * time.Second)}
	pool.setIdleRetirement(func(h *workerHandle) {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		if h != idle {
			t.Error("wrong retirement target")
		}
		drained = true
	})
	done := make(chan struct{})
	go func() { pool.reapIdle(10 * time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retirement held pool lock")
	}
	if !drained || !killed {
		t.Fatal("idle retirement did not complete")
	}
}
func TestUnhealthyReleaseKeepsImmediateKillSemantics(t *testing.T) {
	pool := newWorkerPool(nil)
	killed := false
	handle := &workerHandle{onKill: func() { killed = true }}
	pool.byConv["canceled"] = &pooledEntry{handle: handle, inUse: true}
	pool.setIdleRetirement(func(*workerHandle) { t.Error("unhealthy cancellation was delayed for accounting") })
	pool.Release("canceled", handle, false)
	if !killed {
		t.Fatal("unhealthy worker not killed")
	}
}
