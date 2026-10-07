package main

import (
	"testing"
	"time"
)

// The process-local stop-request channel is the Windows-portable replacement
// for self-signaling (os.Process.Signal to our own PID is unsupported on
// Windows). These tests exercise the channel fixture only — they never
// signal, kill, or restart any real process.
func TestProcessStopRequestCoalescesRepeats(t *testing.T) {
	r := newProcessStopRequest()

	// Two requests with different reasons: both accepted without blocking,
	// coalesced into a single stop token.
	r.request("safe stop committed")
	r.request("idle shutdown")

	select {
	case <-r.wait():
	default:
		t.Fatal("first stop request never reached the channel")
	}
	select {
	case <-r.wait():
		t.Fatal("coalesced request delivered a second token")
	default:
	}
}

func TestProcessStopRequestNeverBlocks(t *testing.T) {
	r := newProcessStopRequest()
	// A pending (unread) request must not block a subsequent caller: the
	// serve loop consumes the token, but API stop triggers arrive from
	// arbitrary handler goroutines and must always return.
	done := make(chan struct{})
	go func() {
		r.request("first")
		r.request("second")
		r.request("third")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request() blocked behind an unconsumed token")
	}
	select {
	case <-r.wait():
	default:
		t.Fatal("no stop token after three coalesced requests")
	}
}
