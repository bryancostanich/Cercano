# Runtime-agnostic model lifecycle — plan

Status: proposed (grounded in code as of `main` @ `78e5a5e5`)
Author intent: the runtime is a swappable backend. Download-on-switch,
readiness, the status chip, and open-model resolution for EVERY consumer
(interactive chat, compaction, recap, research) must be runtime-agnostic and go
through one common path. The only place a runtime name may be branched on is
inside a `localruntime.Provider` implementation (how *that* backend
lists/downloads/launches). No orchestration code should say
`if runtime == "llama_server"` / `== "mistralrs"`.

## Why this doc exists

We built download-on-switch + readiness chip for mistral.rs, but it was wired
as mistralrs-specific special-casing, and a separate legacy model path
(compaction/recap/research) bypasses the runtime abstraction entirely and
hard-fails on a missing model. This plan removes the per-runtime hardcoding and
unifies model provisioning/readiness behind one seam.

---

# North star — the greenfield architecture

This is the target we are migrating TOWARD. Design it as if greenfield: a
request starts, gets routed to a tier (Open or Cloud), and both the routing and
the call are agnostic of the backend underneath. Backend names appear exactly
once, at assembly time in `main.go`.

## The core insight: two axes, never conflated

1. **Tier** = *where/whose* the work runs — `Open` (on this machine) vs `Cloud`
   (a vendor). A routing decision.
2. **Backend** = *what implements it* — llama.cpp / mistral.rs / ollama, or
   anthropic / openai / bedrock. An implementation detail the tiers are blind to.

A request flows DOWN through axis 1 (tier selection) and only touches axis 2 at
the very bottom (assembly). Nothing above the backend branches on a backend
name.

## The one seam: `inference.Provider`

ONE interface. Everyone above it — router, coordinator, chat, compaction,
recap, research — sees only this. No `llm` in the name (that implies a model
type); no `chat` in the method (chat is one shape of inference).

```go
package inference

type Provider interface {
    Identity() Identity        // name, backend kind, tier — self-describing
    Supports() Capabilities    // tools, vision, streaming, embeddings…
    Infer(ctx, Call) (Result, error)
    Stream(ctx, Call) (Stream, error)
}
```

- Request/response types are `inference.Call` / `inference.Result` (NOT
  `ChatRequest`). They carry the message/block vocabulary.
- The chat-message VOCABULARY (`Message`, `Block`, `Role*`, `Tool`) may stay in
  a `llm`/`chat` sub-package that `inference` imports — those genuinely describe
  LLM-chat structure. Only the *seam* is named for inference.

## The flow: request → router → provider

```
Request ─▶ inference.Router.Route(req) ─▶ inference.Provider ─▶ Infer / Stream
                 │
                 ├── Tiers.Open  = inference.Open(  localruntime.MistralRS(cfg) )
                 └── Tiers.Cloud = inference.Failover(
                                       inference.Cloud( cloudvendor.Anthropic(cfg) ),
                                       inference.Cloud( cloudvendor.OpenAI(cfg) ),
                                   )
```

- **`inference.Router`** holds `Tiers{Open, Cloud Provider}`, applies policy
  (locus mode, role, readiness) and returns an `inference.Provider`. It knows
  ONLY tiers + policy — never a backend. (This is exactly today's
  `dispatch.Select`, promoted to a first-class Router returning the one seam.)
- **Tier wrappers** are themselves `inference.Provider`s: `inference.Open(rt)`
  wraps a local runtime; `inference.Cloud(v)` wraps a vendor.
- **`inference.Failover(a, b)`** is a decorator — an `inference.Provider` that
  wraps two others. The router can't tell a failover composite from a plain
  provider. (This is what `llm/fallback` already is.)

## Construction — no `New`, backend named once

Constructors read as the thing itself (Go idiom):

| Concept | Name | Constructs as |
|---|---|---|
| The seam | `inference.Provider` | (interface) |
| Request / result | `inference.Call` / `inference.Result` | — |
| Router | `inference.Router` | `inference.Router(tiers)` |
| Tier wrappers | `inference.Open` / `inference.Cloud` | `inference.Open(rt)` |
| Resilience | `inference.Failover` | `inference.Failover(a, b)` |
| Local backends | `localruntime.LlamaServer` / `.MistralRS` / `.Ollama` | `localruntime.MistralRS(cfg)` |
| Cloud backends | `cloudvendor.Anthropic` / `.OpenAI` / `.Bedrock` | `cloudvendor.Anthropic(cfg)` |

Backend names (`MistralRS`, `Anthropic`) appear ONCE, at assembly in `main.go`.
Nothing branches on `if runtime == "mistralrs"` because the runtime *is* the
mistralrs object.

## What disappears in greenfield

- **`agent.ModelProvider`** — collapses INTO `inference.Provider`. The router
  selects `inference.Provider` directly.
- **`llmModelProvider` / `NewLLMModelProvider`** (the bridge adapter) — deleted,
  not renamed. It only exists because two provider interfaces coexist today
  (`agent.ModelProvider` for the router, `llm.Provider` for dispatch). One seam
  ⇒ no bridge.
- **`legacymodels.OpenModelProvider`** — deleted (its consumers move to the seam).
- **stringly-typed provider maps** (`ModelProviders["OpenModel"]`,
  `["CloudModel"]`) and the **`cloudFactory func(provider, model, apiKey, ...)`**
  string factory — replaced by typed `Tiers{Open, Cloud}` and
  `cloudvendor.X(cfg)` constructors.

---

## The two problems (both confirmed in code)

### Problem 1 — runtime-name string branching (~15+ sites, 8 files)

Orchestration reaches around the `localruntime.Provider`/`Manager` abstraction
with string checks. Confirmed sites:

- `server.go:919` `if req.OpenRuntime == "llama_server"` — switch branch A
- `server.go:943` `if req.OpenRuntime == "mistralrs"` — switch branch B
  (this is where `autoDownloadMistralRSDefault` lives → download-on-switch is
  **mistralrs-only**; switching to llama_server auto-downloads nothing)
- `server.go:1327/1330` resolved-model default, per runtime
- `server.go:1353/1355` mistralrs launch-flag restart special-case
- `server.go:1860/1893` mistralrs-only status/instance paths
- `server.go:243` `if m.Runtime != "mistralrs"` in resolveMistralRSDefault
- `events.go:159` `if runtime == "ollama"` in buildOpenRuntimeStatus
- `runtime_observer.go:60/62/86/88` `case "mistralrs"/"llama_server"`
- `open_runtime_install.go:32/99` `if runtime != "llama_server"` — the
  GetOpenRuntimeStatus pull-path gate (returns unconditional ok=true for
  everything else; the readiness bug)
- `dispatch/readiness.go:23` `OpenModelReady`: llama-server-only GGUF file-stat;
  returns `true` for any other runtime — the ROUTING readiness gate ("cloud
  covers the gap while the model downloads") is blind to mistralrs/download-state
- `worker/worker.go:395` `cfg.OpenRuntime == "llama_server" || == "mistralrs"`
- `providers.go:448` GetEngine("ollama") health-monitor special-case

Two distinct readiness formatters (`buildMistralRSStatus` vs
`buildOpenRuntimeStatus`) instead of one.

### Problem 2 — two disconnected open-model systems

- **Runtime manager path** (`internal/localruntime`): providers, inventory,
  `DownloadState` machine, downloads, the readiness/observer flow. Governs the
  interactive open runtime and its `default_model`.
- **Legacy path** (`internal/legacymodels.OpenModelProvider`): a static
  `ModelName` string + `Engine`, set once via `SetEngine`/`SetModelName` from
  `open_model`, dispatched straight to `engine.Complete`. **No connection to
  downloads or readiness.**

Consumers of the LEGACY path (verified): compaction (`compactor.Advance` →
`OpenModelProvider.Process`), recap (`recap.Generator.regenerate`), research,
and the dispatch open lane. Because the legacy provider has no model-presence
awareness, a missing/stale `open_model` (e.g. `llama_server:0338a4e6edca`
pointing at a model not on disk) **hard-fails** — this is the compaction
failure we hit. The thing that needs the model isn't going through the system
that manages models.

## Design principle

One runtime-agnostic seam, parameterized by runtime, delegating runtime
specifics to the provider:

```
RuntimeModelService (new, in internal/localruntime or a thin service over it):
  EnsureModelsPresent(ctx, runtime) error      // download if missing, no-op if present
  Readiness(ctx, runtime) RuntimeReadiness      // {state: missing|downloading|ready, model, msg}
  ResolveOpenModel(ctx, runtime, want) (ModelRecord, error) // fuzzy → canonical
```

- Every switch calls `EnsureModelsPresent(ctx, newRuntime)` — NOT a mistralrs
  branch. The provider decides what "its models" are and how to fetch them
  (llama-server: the default GGUF; mistralrs: the curated default; ollama:
  no-op, it manages its own).
- ONE `Readiness` used by: the UpdateConfig push broadcast, the
  GetOpenRuntimeStatus pull path, the observer, the chip, AND
  `dispatch.OpenModelReady` (routing gate). Kills the two-formatter fork and
  the `!= "llama_server"` gate.
- The legacy `OpenModelProvider` resolves + ensures its model through this
  service before dispatch, so compaction/recap/research never hard-fail on a
  missing model — they get the same download/readiness behavior as chat.

Runtime-specific code survives ONLY inside each `localruntime.Provider`
(`EnsureModelsPresent`/readiness become provider methods or are derived from
provider inventory + a `RequiredModels()` provider hook).

## Phases (independently landable, each green)

### Phase 1 — runtime-agnostic readiness (collapse the fork)
- Introduce `Readiness(ctx, runtime) RuntimeReadiness` computed from provider
  inventory + config default (reuse `resolveMistralRSDefault`'s logic,
  generalized to any runtime via the provider). Include the three-state
  `missing | downloading | ready` (fixes the `o: downloading` gap too).
- Replace `buildMistralRSStatus` + `buildOpenRuntimeStatus` call sites and the
  `open_runtime_install.go` pull-path gate with this one function.
- Point `dispatch.OpenModelReady` at the same readiness (so routing "cloud
  covers the gap" works for mistralrs, not just llama-server).
- Proto: add `downloading` to `OpenRuntimeStatus` (from the switch-download
  plan). CLI chip renders `o: downloading`.
- Tests: readiness table per runtime × {missing, downloading, ready}.

### Phase 2 — runtime-agnostic download-on-switch
- Add `EnsureModelsPresent(ctx, runtime)` (provider-delegated). Call it for
  ANY runtime in the UpdateConfig switch path, replacing the mistralrs-only
  `autoDownloadMistralRSDefault` branch.
- llama-server's provider implements "ensure default GGUF present" (this is
  also what closes the Phase-3 orphan/legacy-flat-file gap noted elsewhere);
  ollama is a no-op; mistralrs keeps current behavior via the general path.
- Observer stays but its `case "mistralrs"/"llama_server"` collapses to the
  generic ensure/readiness calls.
- Tests: switch → ensure called for each runtime; missing → download enqueued;
  present → no-op.

### Phase 3 — unify the legacy open-model path
- Route `legacymodels.OpenModelProvider` model resolution through
  `ResolveOpenModel` + `EnsureModelsPresent` so compaction/recap/research
  resolve to a canonical, present model (download/await or degrade gracefully
  rather than hard-fail).
- Decision required (see below): retire the legacy provider vs. keep it as a
  thin shim that delegates resolution/ensure to the service.
- Tests: compaction with a missing open_model → does not hard-fail; resolves
  or defers via readiness.

### Phase 4 — sweep remaining hardcoded sites
- `worker/worker.go:395`, `providers.go` ollama health-monitor, any residual
  `== "llama_server"` outside provider impls. Each either generalized or
  explicitly annotated as provider-adapter-level (allowed).

---

## Seam consolidation — reaching the greenfield north star

Phases 1–4 fix the runtime-agnostic LIFECYCLE (downloads, readiness, resolution)
without disturbing the provider interfaces. Phases 5–7 collapse the TWO provider
interfaces into the single `inference.Provider` seam. These are sequenced last
because they touch the router/coordinator, where risk concentrates — and because
1–4 already fix the user-facing bugs (compaction hard-fail, `o: downloading`,
mistralrs auto-download), so 5–7 are pure architecture with no behavior change.

### Phase 5 — rename the seam: `llm.Provider` → `inference.Provider`
- New `internal/inference` package: move the `Provider` interface +
  `Capabilities` there. Rename `Provider.Chat/StreamChat` → `Infer/Stream` and
  `ChatRequest/ChatResponse` → `Call/Result` (or keep request types in `llm` and
  have `inference` import them — decide at implementation; the SEAM name is what
  matters). ~138 call sites.
- The chat vocabulary (`Message`, `Block`, `Role*`, `Tool`) stays in `llm`.
- Pure rename + move; no behavior change. Green build/test is the gate.
- The `Provider` method signatures are unchanged by the `78e5a5e5` failover
  work, so this is unaffected by latest main.

### Phase 6 — collapse `agent.ModelProvider` INTO `inference.Provider`
- Today the router/coordinator speak `agent.ModelProvider` (`Process`/text) and
  dispatch speaks `inference.Provider` (`Infer`/blocks), bridged by the adapter.
  Migrate the router (`SmartRouter`/`LazyRouter`), `ADKCoordinator`,
  `cloud_degrade`/`cloud_absent`, `streaming`, and `tools/generic_generator` to
  consume `inference.Provider` directly.
- Replace stringly-typed provider maps (`ModelProviders["OpenModel"]`,
  `["CloudModel"]`) with typed `Tiers{Open, Cloud inference.Provider}`; replace
  the `cloudFactory func(provider, model, apiKey, baseURL)` string factory with
  `cloudvendor.X(cfg)` typed constructors.
- DELETE the bridge adapter (`llmModelProvider`/`NewLLMModelProvider`,
  `llm_adapter.go`) — one seam ⇒ no bridge. This is the "stupid name" cleanup;
  it's a deletion, not a rename.
- Keep the failover composite intact (`providers.wrapBackup` + worker mirror →
  become `inference.Failover`).

### Phase 7 — promote `dispatch.Select` → `inference.Router`
- Lift the tier-selection logic (`dispatch.Select` + `dispatch.Providers`) into
  `inference.Router` holding typed `Tiers{Open, Cloud}` and returning an
  `inference.Provider`. Locus policy stays the governor.
- Backends now constructed once at assembly (`localruntime.MistralRS(cfg)`,
  `cloudvendor.Anthropic(cfg)`), wrapped as `inference.Open(rt)` /
  `inference.Cloud(v)` — backend names appear ONCE in `main.go`.

At the end of Phase 7 the code matches the north-star diagram: one seam, typed
tiers, no adapter, no stringly-typed provider maps, backend names only at
assembly.

## Decisions (settled)

### Naming — Option A (settled)
The backend seam is *inference*, not *llm* (which implies a model type). Rename
the interface, NOT the whole `llm` package:
- `llm.Provider` → `inference.Provider` in a new small `internal/inference`
  package holding the interface + `Capabilities` (~138 call sites).
- The chat/message VOCABULARY stays in `llm` (`llm.Message` 326 uses,
  `llm.Block` 268, `llm.Role*`, `llm.Tool`, `llm.ChatRequest`/`ChatResponse`) —
  those genuinely describe LLM-chat structure and would read wrong as
  `inference.RoleAssistant`. Only the provider seam moves.
- Greenfield finish (Phase 5) also renames the methods to shed the chat framing:
  `Chat/StreamChat` → `Infer/Stream`, `ChatRequest/ChatResponse` → `Call/Result`.
  The `inference` package imports the `llm` chat vocabulary for the message/block
  types inside `Call`. Clean layering: "the inference seam" over "the chat
  message model". (The exact request-type placement is an implementation detail;
  the SEAM name — `inference.Provider` — is the settled decision.)

### Retirement — settled: retire `legacymodels.OpenModelProvider`, in two stages.
The end state is the greenfield north star (one `inference.Provider` seam, no
adapter). We get there in two stages so the risky part is isolated:

**Stage 1 (Phase 3) — delete `legacymodels.OpenModelProvider`, keep the router's
`agent.ModelProvider` for now.** Confirmed the migration is nearly done already:
`agent.NewLLMModelProvider` (`internal/agent/llm_adapter.go`) ALREADY adapts an
`llm.Provider` to `agent.ModelProvider` via a one-shot `Chat` + text flatten —
exactly what compaction/recap/research need. So Stage 1 is:
1. Point the remaining `agent.ModelProvider` open consumers at the adapter over
   the `inference.Provider`, with the model resolved by
   `RuntimeModelService.ResolveOpenModel` + `EnsureModelsPresent` (fixes the
   compaction hard-fail on a phantom `open_model`).
2. Delete `legacymodels.OpenModelProvider` + its wiring
   (`cmd/cercano/main.go:159`, `cmd/agent/main.go:82`,
   `providers.Reconfigure` SetEngine/SetModelName, `OpenLegacy()`).
Low risk — the adapter already exists; no router/coordinator interface rewrite.

**Stage 2 (Phase 6) — delete the adapter too, collapse `agent.ModelProvider`
into `inference.Provider`.** This is the greenfield finish: the router/
coordinator consume the seam directly, `llmModelProvider`/`NewLLMModelProvider`
(the "stupid name") is deleted rather than renamed, and stringly-typed provider
maps + the `cloudFactory` string factory become typed `Tiers` + `cloudvendor.X`
constructors. Sequenced last (see Seam consolidation) because it touches the
router.

## Latest-main note (as of `78e5a5e5`, failover cleanup)
- `78e5a5e5 feat(llm): experience-preserving cloud failover` added
  `llm.ChatRequest.Tier` + `llm.ChatResponse.Model` and reworked
  `llm/fallback`. The `Provider` interface's 4 method signatures are UNCHANGED,
  so the `Provider → inference.Provider` rename is unaffected.
- It deleted `Server.wrapBackupLocked` (dead twin). The live composite builder
  is `providers.wrapBackup` + the worker mirror — those are the failover seams
  the retirement/rename must keep intact.

## Non-goals
- No progress bar in the chip (word "downloading" only; `/m` owns progress).
- Not changing how a Provider launches its sidecar — only how orchestration
  above the provider decides ensure/readiness/resolution.

## Cross-refs
- `docs/features/embedded_inference/mistralrs-switch-download-plan.md` — the
  `o: downloading` chip + pull-path readiness fix; folds into Phase 1.
