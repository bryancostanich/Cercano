# Dispatch Engine — Implementation Notes & Follow-ons

Post-build record of decisions and as-built deviations discovered **during**
implementation and the main-integration merge, plus the consolidated follow-on
list. Design rationale lives in [`design.md`](design.md); the task breakdown in
[`plan.md`](plan.md). This file exists so none of it is lost when the SDD scratch
ledger (git-ignored) is discarded.

**Status:** BUILT and merged to `main` (fast-forward, merged tip `326e3b5`).
Built across 16 commits on `worktree-agent-capabilities`, then integrated with
~94 commits of parallel `main` work via two catch-up merges. Server suite (50
pkgs) and CLI suite (8 pkgs) green; dispatch-engine files gofmt-clean.

## As-built deviations from plan.md (intentional, from the main-integration merge)

The plan was written before `main` landed its cloud-profile / hot-reload work.
Reconciling the two required these changes beyond the plan:

1. **Dynamic provider resolution.** The plan constructed the engine with a static
   `Providers` struct. `main` made the cloud provider hot-swappable (cloud profiles
   + `RebuildCloud` + config watcher), so `NewEngine` now takes `func() Providers`,
   resolved fresh per dispatch. A runtime cloud-profile swap is honored without
   rebuilding the engine.

2. **Usage-wrap moved to hand-off.** The plan wrapped providers with
   `usage.Wrap("main", …)` at construction (DE-T2). To keep the server's stored
   providers **raw** (so the engine wraps per-dispatch by source without
   double-counting) *and* still record main-loop usage, the `"main"` wrap now
   happens in `server.resolveMainProvider` as it hands the provider to
   `RunToolLoop`. Net: server stores raw; the tool loop gets a wrapped view; the
   engine reads raw. The server gained `SetUsageSink` plus `CloudLLMProvider()` /
   `LocalLLMProvider()` raw getters.

3. **Import-cycle seam.** `internal/agent` imports `internal/dispatch` (the Agent
   holds a `*dispatch.Engine` for co-processor work), so `dispatch` cannot import
   `agent`. Agentic dispatch reuses `agent.RunToolLoop` via a
   `dispatch.AgenticRunner` func-type seam implemented on the server
   (`runAgenticDispatch`) and wired through `SetDispatchEngine → SetAgenticRunner`.

4. **Co-processor model name.** Migrated co-processor work reports the **configured**
   model name (`cfg.CloudModel` / `cfg.LocalModel`) in `RoutingMetadata.ModelName`,
   not the legacy provider `Name()` (e.g. `"anthropic"`). Intentional improvement.

## Telemetry decision (cost-usage seam)

- **Co-processor telemetry stays MCP-side.** The existing MCP `emitEvent` records
  co-processor tokens from the gRPC response. The dispatch engine is deliberately
  **not** given a `usageSink`, because doing so would double-count co-processor
  usage against the MCP recorder (both write the same `telemetry.db`).
- **Main-loop turns are now recorded agent-side** (the `resolveMainProvider`
  `"main"` wrap → an agent-server telemetry collector). The gRPC server had no
  telemetry of its own before this work.
- **Follow-on:** to record co-processor / dispatch usage *at the provider boundary*
  (the design's ideal single seam), set the engine's `usageSink` **and** drop the
  MCP-side local emit for co-processor in the same change — otherwise telemetry
  double-counts. Pairs with "remove bespoke MCP co-processor handlers" below.

## Follow-ons / known issues

(See also `plan.md` → "Deferred / follow-on".)

- **Remove bespoke MCP co-processor handlers** (`handleSummarize`/`Extract`/`Classify`/
  `Explain`) in favor of `InvokeCapability` routing — eliminates the duplicate prompt
  templates now living in both the MCP handlers and the new capabilities. Do this
  together with activating engine-side co-processor telemetry (above).
  **CAVEAT (found while scoping this):** NOT a quick cleanup. `mcp/server.go`'s shared
  `emitEvent` records `ContentTokensAvoided` + `TokenSaving` (the cloud-tokens-saved-by-
  local-processing metric — Cercano's core savings telemetry). The engine's `usage.Usage`
  only carries raw in/out tokens, so naively moving these commands to the provider
  boundary ZEROES their savings telemetry. Doing it right requires extending the usage
  seam (`usage.Usage` + the engine wrap + the coproc capabilities) to carry
  `ContentTokensAvoided`, then de-duping `emitEvent` (which is also shared by
  `cercano_local`/`init`/`deep_research`). This is its own scoped effort against the usage
  seam, not a handler deletion. Until then, leaving the bespoke handlers in place keeps the
  savings telemetry intact (the only cost is the duplicate prompt templates).
- **Retire `legacymodels.CloudModelProvider` (langchaingo) + the `ModelProvider`
  interface** once the SmartRouter main-agent intent path migrates onto `llm.Provider`
  (pairs with the future embedded small-model router).
- **Async job + poll MCP dispatch mode** for long agentic runs (synchronous today; risks
  host tool-call timeouts).
- **Parallel fan-out orchestrator** over the single-dispatch primitive (bounded concurrency).
- **Watchdog (Spec 0b Part C)** — now unblocked by the dispatch engine's small-model
  routing seam; the protocol-enforcement layer from 0b can be built against it.
- **`review` structured verdict** — the capability returns a free-text `VERDICT/REASONING`;
  a parsed `{real bool, reasoning}` is a future enhancement (deliberately avoided fragile
  parsing for v1).
- **Pre-existing gofmt dirt** in several 0a `builtins/*.go` (`fs_read`/`git_read`/`grep`/`run`
  + the agentadapter/mcpadapter files) — present before this work; worth a one-shot
  repo-wide `gofmt -w`.

## Carried-over 0a minor findings (low priority)

- `capability.go` `ToPermission` defaults an unknown `Tier` to `PermR` (silent
  down-escalation) — plan-mandated.
- `capability.go` `LLMContent` returns empty on `json.Marshal` failure (mirrors prior
  `agenttools` behavior).
- `Services.CloudProvider` (and `Engine`/`Conversations`) are captured by value and are
  nil when unconfigured — builtins reading them must nil-guard.
