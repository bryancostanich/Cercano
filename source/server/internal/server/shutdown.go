// Graceful shutdown for the singleton agent.
//
// The agent is routinely SIGTERMed mid-stream — the dev launcher kills any
// running agent older than a freshly built binary. Before this existed, that
// kill severed every in-flight StreamProcessRequest and each attached CLI
// surfaced "Unavailable: error reading from server: EOF". The drain sequence:
//
//  1. BeginShutdown ends the standing SubscribeEvents streams (they'd
//     otherwise hold GracefulStop open for as long as any client is attached).
//  2. DrainThenStop lets in-flight turns finish, with a hard-stop backstop so
//     a wedged handler can't keep a zombie agent alive.
//
// GracefulStop closes the listener immediately, so the port frees for a
// replacement agent while the old process drains.
package server

import (
	"time"

	"google.golang.org/grpc"
)

// BeginShutdown ends the standing per-client subscription streams so a
// subsequent GracefulStop only has to wait for real in-flight work. Safe to
// call on a server that never created an event hub, and idempotent.
func (s *Server) BeginShutdown() {
	if s.events != nil {
		s.events.closeAll()
	}
}

// DrainThenStop gracefully stops gs, waiting up to grace for in-flight RPCs to
// finish before hard-stopping. Returns true if the drain completed within
// grace, false if the backstop fired. Call BeginShutdown first — standing
// event streams never finish on their own.
func DrainThenStop(gs *grpc.Server, grace time.Duration) bool {
	done := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(grace):
		// Force-close transports and return without waiting for the
		// GracefulStop goroutine: grpc's stop(graceful=true) waits on
		// handlersWG unconditionally, so a truly wedged handler would hold
		// <-done forever even after Stop. Stop itself skips the handler wait
		// and returns once connections are torn down; the caller is about to
		// exit the process, so the leaked goroutine is moot.
		gs.Stop()
		return false
	}
}
