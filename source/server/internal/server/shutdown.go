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

// stopBackstop bounds the hard-stop phase. grpc's Stop() cannot abort a
// GracefulStop() that has already reached handlersWG.Wait() holding the server
// mutex (what a wedged streaming handler causes) — Stop() blocks acquiring that
// same mutex, so the two deadlock. DrainThenStop is the backstop of last
// resort: it must always return so the process can exit. If Stop() can't make
// progress within this window we return anyway; the leaked stop goroutines die
// with the process.
const stopBackstop = 2 * time.Second

// DrainThenStop gracefully stops gs, waiting up to grace for in-flight RPCs to
// finish before hard-stopping. Returns true if the drain completed within
// grace, false if the backstop fired. Call BeginShutdown first — standing
// event streams never finish on their own.
//
// Guarantees termination: it returns within grace + stopBackstop no matter how
// wedged the server is. The earlier version called gs.Stop() synchronously on
// the timeout path, which deadlocked whenever GracefulStop had already parked
// in handlersWG.Wait() holding the server mutex — Stop() then blocked on that
// mutex and DrainThenStop never returned.
func DrainThenStop(gs *grpc.Server, grace time.Duration) bool {
	graceful := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(graceful)
	}()
	select {
	case <-graceful:
		return true
	case <-time.After(grace):
	}
	// Grace expired. Force-close transports to unstick conn-blocked handlers —
	// but run Stop() off the return path: if it can't acquire the server mutex
	// (GracefulStop parked in handlersWG.Wait), it would block us forever.
	// Return as soon as either stop call makes progress, or the backstop fires.
	hard := make(chan struct{})
	go func() {
		gs.Stop()
		close(hard)
	}()
	select {
	case <-graceful:
	case <-hard:
	case <-time.After(stopBackstop):
	}
	return false
}
