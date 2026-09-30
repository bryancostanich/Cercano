package worker

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/toolstack"
	pkgcfg "cercano/source/server/pkg/config"
)

// workerAutonomyToolSvc builds the worker's capability/tool stack exactly the
// production buildDeps path does (buildWorkerToolSvc via the shared toolstack
// builder), with a recording EnterProfile hook like the stream profile
// controller. It exists so autonomy probes/regressions exercise the same wiring
// a real worker turn gets.
func workerAutonomyToolSvc(t *testing.T, enterProfile func(context.Context, string) error) *toolssvc.Service {
	return workerAutonomyToolSvcWithLedger(t, enterProfile, nil)
}

// workerAutonomyToolSvcWithLedger is workerAutonomyToolSvc with an explicit
// autonomy ledger wired into the worker's CapDeps — the seam the stream proxy
// fills in production worker turns.
func workerAutonomyToolSvcWithLedger(t *testing.T, enterProfile func(context.Context, string) error, autonomy conversation.AutonomyLedger) *toolssvc.Service {
	t.Helper()
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})
	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, nil, pkgcfg.Config{}, nil, enterProfile, nil, nil, autonomy)
	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	return ts
}

// executeCapability runs one capability through the worker-built registry with
// the registry's Services, mirroring the agent adapter's Svc injection.
func executeCapability(ctx context.Context, t *testing.T, ts *toolssvc.Service, name string, convID string, args string) (*capabilities.Result, error) {
	t.Helper()
	reg := ts.CapRegistry()
	cap, ok := reg.Get(name)
	if !ok {
		t.Fatalf("capability %q not registered in worker stack", name)
	}
	return cap.Execute(ctx, &capabilities.Call{
		Args:           []byte(args),
		ConversationID: convID,
		Svc:            reg.Services(),
	})
}

// TestProbe_WorkerAutonomyLedgerMissing reproduces the smallest worker-mode
// failure: every autonomous tool reaches requireAutonomyStore, and the worker's
// CapDeps carries no conversation store, so after the user approves
// request_autonomous_execution the capability errors with "autonomy ledger is
// not available". Because these are session-control tools, the agent tool loop
// treats the execution error as a control-boundary failure and terminates the
// turn — the approved autonomous entry is lost.
//
// Run pre-fix, this probe shows the production failure mode.
func TestProbe_WorkerAutonomyLedgerMissing(t *testing.T) {
	ctx := context.Background()
	ts := workerAutonomyToolSvc(t, func(context.Context, string) error { return nil })

	// Entry: the approved request_autonomous_execution call.
	_, err := executeCapability(ctx, t, ts, "request_autonomous_execution", "conv-probe", `{"goal":"ship the demo"}`)
	if err == nil {
		t.Fatal("expected the pre-fix autonomy-ledger failure, got success")
	}
	if !strings.Contains(err.Error(), "autonomy ledger is not available") {
		t.Fatalf("expected autonomy-ledger failure, got: %v", err)
	}
	t.Logf("probe reproduced worker failure: %v", err)

	// The other autonomous tools fail the same way.
	for _, tc := range []struct {
		name string
		args string
	}{
		{"suggest_autonomous", `{"goal":"ship the demo"}`},
		{"capture_decision", `{"decision_point":"wire the ledger","options":[{"title":"proxy over stream","cost":"one round-trip","risk":"low","reward":"host ownership preserved","side_effects":"none"},{"title":"open sqlite in worker","cost":"two owners","risk":"high","reward":"fast","side_effects":"db contention"}],"counterarguments":[{"option":"proxy over stream","strongest_case":"latency per ledger write"}],"recommendation":"proxy over stream","chosen_path":"proxy over stream","why_cleanest":"keeps a single store owner","reversibility":"easy","stop_required":false}`},
		{"auto_exit", `{}`},
		{"request_autonomous_exit", `{"summary":"done","verification":"tests pass"}`},
	} {
		_, err := executeCapability(ctx, t, ts, tc.name, "conv-probe", tc.args)
		if err == nil || !strings.Contains(err.Error(), "autonomy ledger is not available") {
			t.Fatalf("%s: expected autonomy-ledger failure, got: %v", tc.name, err)
		}
	}
}
