package worker

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestUpdateWorkerTeardownOnlyOnce(t *testing.T) {
	var calls atomic.Int64
	h := &workerHandle{onKill: func() { calls.Add(1) }}
	h.Kill()
	h.Kill()
	if calls.Load() != 1 {
		t.Fatalf("worker teardown ran %d times", calls.Load())
	}
}
func TestUpdateWorkerConcurrentTeardownCompletesOnce(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	h := &workerHandle{onKill: func() {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-finish
	}}
	var wg sync.WaitGroup
	var returned atomic.Int64
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.Kill(); returned.Add(1) }()
	}
	<-entered
	if returned.Load() != 0 {
		t.Error("cleanup returned before teardown finished")
	}
	close(finish)
	wg.Wait()
	if calls.Load() != 1 || returned.Load() != 12 {
		t.Fatalf("teardowns=%d returned=%d", calls.Load(), returned.Load())
	}
}
