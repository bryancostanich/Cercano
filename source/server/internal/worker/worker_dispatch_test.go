package worker

import (
	"testing"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/toolstack"
	pkgcfg "cercano/source/server/pkg/config"
)

// TestBuildWorkerToolSvc_WiresDispatch guards against regression of the original
// bug: worker turns must wire a non-nil capabilities.Services.Dispatch so a
// capability that dispatches (local, the co-processor caps, review, the web
// caps, and the dispatch sub-agent) finds a live engine instead of failing at
// runtime with "dispatch engine not available". The worker previously handed
// capabilities an empty Services{}.
func TestBuildWorkerToolSvc_WiresDispatch(t *testing.T) {
	ctxLoader := projectctx.NewLoader()
	eng := toolstack.NewEngine(toolstack.EngineDeps{
		Providers: func() dispatch.Providers { return dispatch.Providers{} },
		LocusMode: func() locus.Mode { var m locus.Mode; return m },
		CtxLoader: ctxLoader,
		ModelFor:  func(bool, pkgcfg.Tier) string { return "model" },
	})

	svc := buildWorkerToolSvc(nil, eng, ctxLoader, nil, nil, pkgcfg.Config{}, nil)

	ts, ok := svc.(*toolssvc.Service)
	if !ok {
		t.Fatalf("expected *toolssvc.Service, got %T", svc)
	}
	if ts.CapRegistry().Services().Dispatch == nil {
		t.Fatal("worker Services.Dispatch is nil — the dispatch-engine bug has regressed")
	}
}
