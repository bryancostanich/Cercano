package agentclient

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsUnavailable_TrueForGRPCUnavailable(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection error")
	if !isUnavailable(err) {
		t.Fatal("expected codes.Unavailable to be recognised")
	}
}

func TestIsUnavailable_FalseForOtherStatusCodes(t *testing.T) {
	err := status.Error(codes.InvalidArgument, "nope")
	if isUnavailable(err) {
		t.Fatal("InvalidArgument must not be treated as connection death")
	}
}

func TestIsUnavailable_FalseForNilAndPlainErrors(t *testing.T) {
	if isUnavailable(nil) {
		t.Fatal("nil error is not Unavailable")
	}
	if isUnavailable(errors.New("some plain error")) {
		t.Fatal("plain (non-gRPC) errors must not be classified as Unavailable")
	}
}

func TestReconnectBackoff_ExponentialSchedule(t *testing.T) {
	// Locks in the documented "1s, 2s, 4s" schedule so accidental edits
	// to the exponent get caught.
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
	}
	for _, c := range cases {
		got := reconnectBackoff(c.attempt)
		if got != c.want {
			t.Errorf("attempt %d: got %s, want %s", c.attempt, got, c.want)
		}
	}
}

func TestConnState_StringNames(t *testing.T) {
	// The state names surface in logs, test failures, and eventually the
	// CLI status chip. Lock them in so an accidental reorder of the
	// const block doesn't silently corrupt UI copy.
	cases := []struct {
		s    ConnState
		want string
	}{
		{ConnStateConnected, "connected"},
		{ConnStateReconnecting, "reconnecting"},
		{ConnStateFailed, "failed"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("%d: got %q, want %q", int(c.s), got, c.want)
		}
	}
}

func TestStateBroker_SubscribeBroadcastUnsubscribe(t *testing.T) {
	b := newStateBroker()
	ch, unsub := b.subscribe()

	b.broadcast(ConnStateChanged{State: ConnStateReconnecting, Attempt: 1})
	select {
	case ev := <-ch:
		if ev.State != ConnStateReconnecting || ev.Attempt != 1 {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber did not receive event")
	}

	unsub()
	// After unsubscribe the channel should be closed; second recv gets
	// the zero-value and ok=false.
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after unsubscribe")
	}
}

func TestStateBroker_SlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	// If a subscriber's buffer fills up, the broker must drop instead
	// of blocking — otherwise one wedged listener stalls every other
	// observer.
	b := newStateBroker()
	_, _ = b.subscribe() // never drained
	done := make(chan struct{})
	go func() {
		// Fire more events than the buffer (8) can hold.
		for i := 0; i < 20; i++ {
			b.broadcast(ConnStateChanged{State: ConnStateReconnecting, Attempt: i})
		}
		close(done)
	}()
	select {
	case <-done:
		// Success — broadcast returned even though a subscriber was stuck.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("broadcast blocked on a slow subscriber")
	}
}

func TestClientSetState_EmitsOnlyOnActualChange(t *testing.T) {
	c := &Client{stateBroker: newStateBroker()}
	ch, unsub := c.ConnStateChanges()
	defer unsub()

	c.setState(ConnStateReconnecting, 1, nil)
	c.setState(ConnStateReconnecting, 2, nil) // same state → no event
	c.setState(ConnStateConnected, 2, nil)

	// Two events expected; a third recv should time out.
	got := 0
	timeout := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got != 2 {
		t.Fatalf("expected 2 events (one per real transition), got %d", got)
	}
}

func TestClientCurrentState_StartsAtConnectedZeroValue(t *testing.T) {
	// The zero value of ConnState is Connected — matches the assumption
	// that a freshly-Dialed client is connected before watchConn kicks
	// off, so early callers of currentState never see a phantom
	// "reconnecting" state before the first tick.
	c := &Client{}
	if c.currentState() != ConnStateConnected {
		t.Fatalf("zero-value currentState = %s, want connected", c.currentState())
	}
}
