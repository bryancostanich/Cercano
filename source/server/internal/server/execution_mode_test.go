package server

// execution_mode_test.go — the SelectExecutionMode seam + per-turn runner pick.
//
// Two runners coexist: inProcessRunner (always built) and workerRunner (nil
// unless worker mode is selected). The front door picks per turn via
// pickTurnRunner: worker mode + no MCP tools → worker; worker mode + MCP tools
// present → in-process (this turn); in_process mode → always in-process.
//
// These tests verify the wiring WITHOUT spawning any worker process — they
// assert which runner pickTurnRunner returns, and that the existing suite (which
// never calls SelectExecutionMode) stays in-process.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/pkg/config"
)

// sameRunner reports whether two TurnRunner values are the same instance.
func sameRunner(a, b interface{}) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// mcpTool is a minimal MCP-origin Tool for registry routing tests.
type mcpTool struct{ name string }

func (m mcpTool) Name() string                      { return m.name }
func (m mcpTool) Description() string               { return "mcp test tool" }
func (m mcpTool) Permission() agenttools.Permission { return agenttools.PermR }
func (m mcpTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (m mcpTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return &agenttools.Result{}, nil
}
func (m mcpTool) Origin() agenttools.Origin { return agenttools.OriginMCP }

// registerMCPTool adds an MCP-origin tool to the server's live registry.
func registerMCPTool(t *testing.T, s *Server) {
	t.Helper()
	if err := s.toolSvc.Registry().Register(mcpTool{name: "mcp_probe"}); err != nil {
		t.Fatalf("register mcp tool: %v", err)
	}
}

func TestSelectExecutionMode_InProcessLeavesWorkerNil(t *testing.T) {
	srv, _ := newServerWithStore(t)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "in_process"})
	srv.SelectExecutionMode()

	if srv.workerRunner != nil {
		t.Error("in_process mode must leave workerRunner nil")
	}
	if !sameRunner(srv.pickTurnRunner(), srv.inProcessRunner) {
		t.Error("in_process mode: pickTurnRunner must return the in-process runner")
	}
}

func TestSelectExecutionMode_WorkerArmsWorkerRunner(t *testing.T) {
	srv, _ := newServerWithStore(t)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "worker"})
	srv.SelectExecutionMode()

	if srv.workerRunner == nil {
		t.Fatal("worker mode must arm workerRunner")
	}
	// No MCP tools registered → the turn uses the worker runner.
	if !sameRunner(srv.pickTurnRunner(), srv.workerRunner) {
		t.Error("worker mode + no MCP tools: pickTurnRunner must return the worker runner")
	}
}

func TestSelectExecutionMode_EmptyDefaultsToWorker(t *testing.T) {
	srv, _ := newServerWithStore(t)
	// Empty ExecutionMode is the production default (treated as worker).
	srv.SetConfigPersistence("", config.Config{})
	srv.SelectExecutionMode()

	if srv.workerRunner == nil {
		t.Error("empty ExecutionMode must default to worker (arm workerRunner)")
	}
}

// TestPickTurnRunner_MCPToolRoutesInProcess is Task A3's core assertion: with
// worker mode armed but an MCP-origin tool in the registry, the per-turn pick
// must return the IN-PROCESS runner (the worker excludes host-side MCP tools).
func TestPickTurnRunner_MCPToolRoutesInProcess(t *testing.T) {
	srv, _ := newServerWithStore(t)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "worker"})
	srv.SelectExecutionMode()
	if srv.workerRunner == nil {
		t.Fatal("precondition: worker mode must arm workerRunner")
	}

	// Before registering the MCP tool: worker.
	if !sameRunner(srv.pickTurnRunner(), srv.workerRunner) {
		t.Fatal("no MCP tools yet: expected worker runner")
	}

	registerMCPTool(t, srv)
	if !srv.hasMCPTools() {
		t.Fatal("hasMCPTools must report true after registering an MCP-origin tool")
	}
	// After: the MCP-involving turn falls back to in-process.
	if !sameRunner(srv.pickTurnRunner(), srv.inProcessRunner) {
		t.Error("worker mode + MCP tool present: pickTurnRunner must fall back to the in-process runner")
	}
}

// TestPickTurnRunner_InProcessModeAlwaysInProcess: in_process mode ignores MCP
// state and always uses the in-process runner.
func TestPickTurnRunner_InProcessModeAlwaysInProcess(t *testing.T) {
	srv, _ := newServerWithStore(t)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "in_process"})
	srv.SelectExecutionMode()

	registerMCPTool(t, srv)
	if !sameRunner(srv.pickTurnRunner(), srv.inProcessRunner) {
		t.Error("in_process mode must always use the in-process runner")
	}
}

// TestHasMCPTools_BuiltinsOnly: a registry of only built-in tools reports no MCP.
func TestHasMCPTools_BuiltinsOnly(t *testing.T) {
	srv, _ := newServerWithStore(t)
	if srv.hasMCPTools() {
		t.Error("built-in-only registry must report no MCP tools")
	}
}

// TestDefaults_ExecutionModeIsWorker locks the production default.
func TestDefaults_ExecutionModeIsWorker(t *testing.T) {
	if got := config.Defaults().ExecutionMode; got != "worker" {
		t.Errorf("config.Defaults().ExecutionMode = %q, want %q", got, "worker")
	}
}

// TestExistingSuite_StaysInProcess guards the wiring invariant: a Server built
// the way the existing suite builds it (newServerWithStore, no
// SelectExecutionMode call) has workerRunner nil, so every turn runs in-process
// and never spawns a worker. If someone arms the worker in NewServer, this fails.
func TestExistingSuite_StaysInProcess(t *testing.T) {
	srv, _ := newServerWithStore(t)
	if srv.workerRunner != nil {
		t.Fatal("default (pre-select) Server must have workerRunner nil — existing suite would spawn workers")
	}
	if !sameRunner(srv.pickTurnRunner(), srv.inProcessRunner) {
		t.Fatal("default Server must pick the in-process runner")
	}
}
