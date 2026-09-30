package server

// execution_mode_test.go — the SelectExecutionMode seam + per-turn runner pick.
//
// Two runners coexist: inProcessRunner (always built) and workerRunner (nil
// unless worker mode is selected). The front door picks per turn via
// pickTurnRunner: worker mode → worker (host MCP tools are proxied into the
// worker, so they no longer force a fallback); in_process mode → in-process.
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

// MCP tools no longer divert a turn away from the worker: host MCP tools are
// proxied into the worker process, so MCP-involving turns get the same crash
// isolation as every other turn. This replaces the previous assertion that such
// turns fell back in-process.
func TestPickTurnRunner_MCPToolStaysInWorker(t *testing.T) {
	srv, _ := newServerWithStore(t)
	srv.SetConfigPersistence("", config.Config{ExecutionMode: "worker"})
	srv.SelectExecutionMode()
	if srv.workerRunner == nil {
		t.Fatal("precondition: worker mode must arm workerRunner")
	}
	if !sameRunner(srv.pickTurnRunner(), srv.workerRunner) {
		t.Fatal("no MCP tools yet: expected worker runner")
	}

	registerMCPTool(t, srv)
	if !srv.hasMCPTools() {
		t.Fatal("hasMCPTools must report true after registering an MCP-origin tool")
	}
	if !sameRunner(srv.pickTurnRunner(), srv.workerRunner) {
		t.Error("worker mode + MCP tool present: the turn must stay in the worker (tools are proxied)")
	}
}

// The worker can only call what it was told about, so advertisement must report
// the live MCP tools — and must NOT leak built-ins into the MCP namespace.
func TestAdvertiseMCPToolsReportsOnlyMCPOriginTools(t *testing.T) {
	srv, _ := newServerWithStore(t)

	if got := srv.advertiseMCPTools(); len(got) != 0 {
		t.Fatalf("built-in-only registry advertised %d MCP tools, want 0", len(got))
	}

	registerMCPTool(t, srv)
	got := srv.advertiseMCPTools()
	if len(got) != 1 {
		t.Fatalf("advertised %d tools, want 1", len(got))
	}
	if got[0].Name == "" {
		t.Error("advertised tool has no name; the worker could not address it")
	}
	if len(got[0].Schema) == 0 {
		t.Error("advertised tool has no schema; the model could not call it correctly")
	}
}

// The host must refuse to run a non-MCP tool through the MCP call path, so a
// stale or forged advertisement cannot reach a first-party tool.
func TestCallMCPToolRejectsNonMCPAndUnknownTools(t *testing.T) {
	srv, _ := newServerWithStore(t)
	registerMCPTool(t, srv)

	if _, err := srv.callMCPTool(context.Background(), "definitely__missing", nil); err == nil {
		t.Error("expected an error for an unregistered tool")
	}

	// Find a genuine built-in and try to call it through the MCP path.
	var builtin string
	for _, tl := range srv.toolSvc.Registry().All() {
		if agenttools.OriginOf(tl) == agenttools.OriginBuiltin {
			builtin = tl.Name()
			break
		}
	}
	if builtin == "" {
		t.Skip("no built-in tool registered in this fixture")
	}
	if _, err := srv.callMCPTool(context.Background(), builtin, nil); err == nil {
		t.Errorf("callMCPTool(%q) succeeded; built-ins must not be reachable via the MCP path", builtin)
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
