package server

import (
	"testing"
	"time"
)

func TestSafeStopIdleTimerCannotBypassActiveWork(t *testing.T) {
	s := &Server{events: newEventHub()}
	done := make(chan struct{}, 1)
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatal(err)
	}
	s.EnableIdleShutdown(0, func() { done <- struct{}{} })
	_, unsubscribe := s.events.subscribe()
	unsubscribe()
	select {
	case <-done:
		work()
		t.Fatal("idle-client shutdown bypassed active work")
	case <-time.After(40 * time.Millisecond):
	}
	work()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle shutdown did not follow completed work")
	}
	if release, err := s.updateWork.enter(); err == nil {
		release()
		t.Fatal("shutdown reopened admission")
	}
}
