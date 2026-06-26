package server

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

// Broadcast fans out to every subscriber, dedupes consecutive identical modes,
// and unsubscribe drops the subscriber.
func TestEventHub_FanOutDedupUnsubscribe(t *testing.T) {
	s := &Server{events: newEventHub()}
	ch1, unsub1 := s.events.subscribe()
	ch2, unsub2 := s.events.subscribe()
	defer unsub2()
	if got := s.events.subscriberCount(); got != 2 {
		t.Fatalf("subscriberCount = %d, want 2", got)
	}

	// Fan-out: both subscribers receive the change.
	s.broadcastPermissionMode("strict")
	for i, ch := range []<-chan *proto.ClientEvent{ch1, ch2} {
		if m := (<-ch).GetPermissionModeChanged().GetMode(); m != "strict" {
			t.Errorf("subscriber %d got mode %q, want strict", i, m)
		}
	}

	// Dedup: re-broadcasting the same mode delivers nothing.
	s.broadcastPermissionMode("strict")
	select {
	case ev := <-ch1:
		t.Errorf("expected no duplicate event, got %q", ev.GetPermissionModeChanged().GetMode())
	default:
	}

	// A real change propagates again.
	s.broadcastPermissionMode("bypass")
	if m := (<-ch1).GetPermissionModeChanged().GetMode(); m != "bypass" {
		t.Errorf("got mode %q, want bypass", m)
	}
	<-ch2 // drain ch2 so it doesn't leak

	// Unsubscribe removes the subscriber.
	unsub1()
	if got := s.events.subscriberCount(); got != 1 {
		t.Errorf("after unsubscribe subscriberCount = %d, want 1", got)
	}
}
