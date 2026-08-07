// Package toolstack assembles the agent's capability/tool stack — the dispatch
// engine, the capability registry (with a fully-populated Services), and the
// agent tool registry — in ONE place, so every turn-execution environment (the
// in-process host and the crash-isolated worker subprocess) wires an identical
// stack.
//
// It exists to kill a class of bug. The host and worker previously built this
// stack through two divergent code paths, and the worker's path handed
// capabilities an empty capabilities.Services{}. Any capability that routes
// model work through the dispatch engine — local, the co-processor caps
// (summarize/extract/classify/explain), review, the web caps
// (research/deep_research/document), and the dispatch sub-agent — therefore
// failed at runtime in worker turns with "dispatch engine not available", while
// working in-process. Both paths now call these builders, so the wiring cannot
// drift again.
package toolstack

import (
	"context"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	tools "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/config"
)

// EngineDeps configures the shared dispatch engine.
//
// Providers is called per dispatch (so a runtime cloud-profile swap is honored)
// and MUST return RAW, unwrapped providers — the engine emits usage itself and
// conditionally, so already-wrapped providers would double-count.
type EngineDeps struct {
	Providers func() inference.Tiers
	LocusMode func() locus.Mode
	CtxLoader *projectctx.Loader
	ModelFor  func(isCloud bool, tier config.Tier) string
	UsageSink func(usage.Usage) // optional; nil disables usage recording
}

// NewEngine builds the dispatch engine with model resolution and (optionally) a
// usage sink installed. It is the single construction point for the engine, so
// the host and worker resolve providers and models the same way.
func NewEngine(d EngineDeps) *dispatch.Engine {
	e := dispatch.NewEngine(d.Providers, d.LocusMode, d.CtxLoader)
	if d.ModelFor != nil {
		e.SetModelFor(d.ModelFor)
	}
	if d.UsageSink != nil {
		e.SetUsageSink(d.UsageSink)
	}
	return e
}

// CapDeps carries the collaborators the capability Services needs beyond the
// dispatch engine. The provider values and Config are currently read by no
// built-in capability (capabilities resolve providers live through the engine),
// but they are populated to keep the host and worker Services identical, so a
// future capability that reads them cannot silently diverge between the two.
type CapDeps struct {
	Cloud     inference.Provider
	Open      inference.Provider
	Config    *config.Config
	CtxLoader *projectctx.Loader
	// EnterProfile switches the session's active capability profile (used by the
	// suggest_plan capability to enter planning mode on user approval). Optional;
	// nil means planning mode is unavailable and suggest_plan errors clearly.
	EnterProfile func(name string) error
	// RestartAgent bounces the singleton agent process (used by the restart_agent
	// capability). Optional; nil means agent restart is unavailable and
	// restart_agent errors clearly.
	RestartAgent func(reason string) error
}

// InstallCapabilities builds the capability registry with a fully-populated
// Services (Dispatch backed by svc's engine), registers the built-in
// capabilities, and builds and installs the agent tool registry on svc.
//
// svc MUST already have its engine set (svc.SetEngine) before this call: the
// Dispatch closure resolves the engine live from svc, so a later engine swap is
// honored and a missing engine yields a clear error rather than a nil call.
func InstallCapabilities(svc tools.Catalog, d CapDeps) {
	capReg := capabilities.NewRegistry(capabilities.Services{
		CloudProvider: d.Cloud,
		OpenProvider:  d.Open,
		Config:        d.Config,
		ProjectCtx:    d.CtxLoader,
		Dispatch: func(ctx context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			e := svc.Engine()
			if e == nil {
				return dispatch.Result{}, fmt.Errorf("dispatch engine not configured")
			}
			return e.Dispatch(ctx, spec)
		},
		EnterProfile: d.EnterProfile,
		RestartAgent: d.RestartAgent,
	})
	builtins.Register(capReg)
	svc.SetCapRegistry(capReg)
	svc.SetRegistry(agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms()))
}
