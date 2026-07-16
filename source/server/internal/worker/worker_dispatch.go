package worker

import (
	"context"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/hostsvc/permissions"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/toolstack"
	pkgcfg "cercano/source/server/pkg/config"
)

// buildWorkerToolSvc assembles the worker's capability/tool stack via the shared
// internal/toolstack builder — the SAME assembly the host uses — so worker turns
// wire an identical capabilities.Services (Dispatch included). The previous
// worker path handed capabilities an empty Services{}, so every capability that
// routes model work through the dispatch engine (local, the co-processor caps,
// review, the web caps, and the dispatch sub-agent) failed at runtime in worker
// turns with "dispatch engine not available". MCP tools are NOT included here:
// MCP servers run host-side only.
//
// Sub-agent (Agentic dispatch) persistence is best-effort and unavailable in the
// worker (the conversation store is host-fenced), so a nil store is passed;
// RunAgenticDispatch already degrades cleanly when the store is nil. The
// sub-agent system prompt reuses the runner's builder via a context-only history
// shim so it still gets env grounding + project context.
func buildWorkerToolSvc(
	permBroker permissions.Broker,
	engine *dispatch.Engine,
	ctxLoader *projectctx.Loader,
	cloud, open inference.Provider,
	cfg pkgcfg.Config,
	subPersist *streamSubagentPersist,
) runner.ToolSvc {
	systemPrompt := func(workDir string) string {
		return runner.BuildSystemPrompt(runner.Deps{Persist: workerCtxHistory{loader: ctxLoader}}, workDir)
	}
	svc := toolssvc.New(permBroker, systemPrompt, nil, subagentPersistTurn(subPersist))
	svc.SetEngine(engine) // installs the agentic runner for sub-agent dispatch
	if subPersist != nil {
		svc.SetEnsureSubagent(subPersist.ensure) // worker creates sub-agent conversation rows on the host
	}
	toolstack.InstallCapabilities(svc, toolstack.CapDeps{
		Cloud:     cloud,
		Open:      open,
		Config:    &cfg,
		CtxLoader: ctxLoader,
	})
	return svc
}

// workerCtxHistory adapts the project-context Loader to runner.TurnHistory so
// runner.BuildSystemPrompt can source .cercano/context.md for worker sub-agent
// prompts. Only LoadProjectContext is exercised by BuildSystemPrompt; the
// history/persist methods are inert (sub-agent turns are not replayed here).
type workerCtxHistory struct{ loader *projectctx.Loader }

func (h workerCtxHistory) AssembleHistory(context.Context, string) []llm.Message { return nil }
func (h workerCtxHistory) PersistTurn(context.Context, string, llm.Message)      {}
func (h workerCtxHistory) LoadProjectContext(workDir string) string {
	s, _ := h.loader.Load(workDir)
	return s
}

// workerDispatchModelFor mirrors the host's Server.DispatchModelFor using the
// snapshotted config, so worker-mode dispatches resolve the SAME model as
// in-process. Cloud: the active profile's vendor cost table. Open: the tier's
// open slot, falling through to the everyday open workhorse so a sparse tier
// table never yields empty.
func workerDispatchModelFor(cfg pkgcfg.Config) func(isCloud bool, tier pkgcfg.Tier) string {
	return func(isCloud bool, tier pkgcfg.Tier) string {
		if isCloud {
			prof, ok := profileByName(cfg.CloudProfiles, cfg.ActiveCloudProfile)
			if !ok {
				return ""
			}
			return cfg.ModelProfiles.ResolveCloudModelForTier(prof, tier)
		}
		if id, _, ok := cfg.Models.Resolve(tier, pkgcfg.ProviderOpen, true); ok {
			return id
		}
		if id, _, ok := cfg.Models.Resolve(pkgcfg.TierEveryday, pkgcfg.ProviderOpen, true); ok {
			return id
		}
		return ""
	}
}
