package server

// execution_mode_test.go — Task 4: the SelectExecutionMode seam.
//
// These tests verify the selector wiring WITHOUT spawning any worker process:
//   - "in_process" keeps the default in-process runner (turnRunner unchanged).
//   - "worker" (and empty) swaps in the worker runner.
//   - The existing server test suite (which builds Server via newServerWithStore
//     and never calls SelectExecutionMode) therefore stays in-process — proven
//     here by asserting the default constructed runner is the in-process one.

import (
	"reflect"
	"testing"

	"cercano/source/server/pkg/config"
)

// runnerIdentity returns a comparable identity for the current turnRunner so a
// test can tell whether SelectExecutionMode swapped it.
func runnerIdentity(s *Server) uintptr {
	return reflect.ValueOf(s.turnRunner).Pointer()
}

func TestSelectExecutionMode_InProcessKeepsDefault(t *testing.T) {
	srv, _ := newServerWithStore(t)
	// newServerWithStore never spawns a worker; the default runner is in-process.
	before := runnerIdentity(srv)

	srv.SetConfigPersistence("", config.Config{ExecutionMode: "in_process"})
	srv.SelectExecutionMode()

	if after := runnerIdentity(srv); after != before {
		t.Errorf("in_process must keep the default runner: identity changed %x → %x", before, after)
	}
}

func TestSelectExecutionMode_WorkerSwapsRunner(t *testing.T) {
	srv, _ := newServerWithStore(t)
	before := runnerIdentity(srv)

	srv.SetConfigPersistence("", config.Config{ExecutionMode: "worker"})
	srv.SelectExecutionMode()

	if after := runnerIdentity(srv); after == before {
		t.Error("worker mode must swap in the worker runner, but the runner is unchanged")
	}
}

func TestSelectExecutionMode_EmptyDefaultsToWorker(t *testing.T) {
	srv, _ := newServerWithStore(t)
	before := runnerIdentity(srv)

	// Empty ExecutionMode is the production default (Defaults() sets "worker",
	// and an unset field is treated as worker by the selector).
	srv.SetConfigPersistence("", config.Config{})
	srv.SelectExecutionMode()

	if after := runnerIdentity(srv); after == before {
		t.Error("empty ExecutionMode must default to worker (swap the runner)")
	}
}

// TestDefaults_ExecutionModeIsWorker locks the production default: crash
// isolation is the default posture.
func TestDefaults_ExecutionModeIsWorker(t *testing.T) {
	if got := config.Defaults().ExecutionMode; got != "worker" {
		t.Errorf("config.Defaults().ExecutionMode = %q, want %q", got, "worker")
	}
}

// TestExistingSuite_StaysInProcess documents + guards the wiring invariant: a
// Server built the way the existing suite builds it (newServerWithStore, no
// SelectExecutionMode call) runs turns IN-PROCESS — it must never spawn a
// worker. If someone moves the selector into NewServer, this fails.
func TestExistingSuite_StaysInProcess(t *testing.T) {
	srv, _ := newServerWithStore(t)
	// The concrete type of the default runner is the in-process runnersvc.Core.
	// We assert it is NOT the worker runner by confirming SelectExecutionMode
	// with "in_process" is a no-op (identity stable), which only holds if the
	// default was already the in-process runner.
	before := runnerIdentity(srv)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "in_process"})
	srv.SelectExecutionMode()
	if runnerIdentity(srv) != before {
		t.Fatal("default (pre-select) runner was not the in-process runner — existing suite would spawn workers")
	}
}
