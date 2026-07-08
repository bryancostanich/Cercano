# Agent Execution Isolation — Phase 2 Implementation Plan (Host Decomposition)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the `Server` god-object (~2900 lines, ~122 methods, 3 cross-cutting mutexes) into focused services behind interfaces, wired by a thin composition root — with **zero behavior change** — so the worker boundary (Phase 5) becomes cleanly cuttable and the host becomes embeddable.

**Architecture:** Extract one cohesive responsibility at a time into `internal/hostsvc/<name>`, define the small interface the front door depends on, move the fields + methods verbatim, repoint the front door to delegate, and keep the existing test suite green as the regression net. The gRPC `Server` shrinks to a front door that holds the services and delegates. See `docs/agent/agent-isolation/design.md`.

**Tech Stack:** Go 1.21+ (module `cercano/source/server`), gRPC/protobuf, SQLite (`modernc.org/sqlite`). No new dependencies.

## Global Constraints

- Module `cercano/source/server`. Build: `cd source/server && go build ./...`. Test: `go test ./... -count=1`.
- **This phase changes NO behavior.** Every task's gate is: the existing suite stays green and `go vet ./...` is clean. If a service has thin test coverage, the task ADDS a characterization test FIRST (locking current behavior) before moving anything.
- No `os.Chdir` in request handling (Phase 1 invariant — do not reintroduce).
- Each extracted service owns its own synchronization. Do not leave a moved field guarded by a mutex that stayed on `Server`. Retire `cfgMu`/`permBcastMu` from `Server` as their fields leave.
- Commit messages: never the word "Claude" anywhere (message or trailers).
- Do not change public gRPC behavior or the `proto.AgentServer` method set. The front door keeps implementing every RPC; it just delegates the body.
- Follow existing patterns; no unrelated refactoring, no renaming of exported RPCs.

---

## Target package layout

New packages under `internal/hostsvc/` (each one responsibility, small interface):

| Package | Owns (today's `Server` fields) | Front-door-facing interface |
|---|---|---|
| `hostsvc/permissions` | `permStore`, `pendingDecisions`, `permBcastMu`, `lastBcastMode` | `PermissionBroker` |
| `hostsvc/config` | `configPath`, `currentConfig`, `cfgMu`, `secrets` + cloud-profile state | `ConfigService` |
| `hostsvc/providers` | `cloudLLMProvider`, `openLLMProvider`, `cloudFactory`, `router`, `coordinator`, `registry`, `catalogManager`, `openProvider` | `ProviderResolver` |
| `hostsvc/tools` | `toolRegistry`, `capRegistry`, `dispatchEngine` | `ToolCatalog` |
| `hostsvc/persistence` | conversation store (via `agent`), `retentionSweeper`, `compactionGen`, `contextLoader` | `ConversationService` |
| `hostsvc/runtimes` | `meridianMgr`, `runtimeManager`, `mcpManager` | `RuntimeSupervisors` |
| `internal/broker` (Phase 4 grows this) | `events`, `turnsMu`, `activeTurns`, `turnGens` | (internal to front door for now) |

`internal/server` keeps: the gRPC front door (every RPC handler, delegating), the turn/broker glue (`beginTurn`, `runMainLoop`, `streamProcessRequestWithToolLoop` — Phase 3 extracts these into the runner), `SubscribeEvents`, shutdown. The `Server` struct's field count drops from ~30 to a handful of service references.

---

## The extraction pattern (every service task follows these steps)

Each service task N below fills in the **parameters** (fields, methods, interface). The mechanical steps are identical — do them in this order:

1. **Characterization guard (only if coverage is thin).** If the service's behavior is not already covered by existing tests, write a test in `internal/server/` that exercises its current behavior THROUGH the `Server` surface, and confirm it passes now. This locks behavior before the move. (Skip when the named existing tests already cover it — the task says which.)
2. **Create the package + the concrete type.** `internal/hostsvc/<name>/<name>.go` with a struct that will hold the moved fields, plus a constructor `New(...)` taking the collaborators it needs (never the whole `Server`).
3. **Move the fields and methods verbatim.** Cut the named fields off `Server`, cut the named methods (change receiver `(s *Server)` → `(x *<Type>)`, rename `s.` field refs to `x.`), keep bodies unchanged. Move the mutex that guards those fields with them.
4. **Define the front-door interface** (the signatures listed in the task) in the service package, and have the concrete type satisfy it. The front door holds the interface, not the concrete type.
5. **Repoint the front door.** Replace each moved method on `Server` with either (a) a one-line delegator for methods that are `proto.AgentServer` RPCs (they must stay on `Server`), or (b) a call through the interface field for internal helpers. Add the service to the composition root (`NewServer` / the `Set*` wiring) so it's constructed and injected.
6. **Green gate + commit.** `go build ./... && go vet ./... && go test ./... -count=1` all clean; the field is gone from `Server`; the guarding mutex is gone from `Server` if all its fields left. Commit with the task's message.

**Note on "verbatim move":** the method bodies already exist and are correct — this plan does not repaste them. The actionable content per task is *which* fields/methods move, the *interface* they hide behind, and the service-specific gotchas. A reviewer verifies the move is behavior-preserving (diff shows methods relocated, not rewritten) and the suite is green.

---

## Task 1: Extract `hostsvc/permissions` (leaf — proves the pattern)

Smallest, most self-contained service. Do it first to establish the extraction pattern with minimal blast radius.

**Files:**
- Create: `internal/hostsvc/permissions/permissions.go`, `internal/hostsvc/permissions/permissions_test.go`
- Modify: `internal/server/server.go` (remove fields + methods, add delegators + wiring)

**Fields to move off `Server`:** `permStore *agent.PermissionStore`, `pendingDecisions *agent.PendingDecisions`, `permBcastMu sync.Mutex`, `lastBcastMode string`.

**Methods to move** (receiver becomes `(p *Broker)`): `SetPermissionMode`, `GetPermissionMode`, `AllowToolCall`, `DenyToolCall`, `StartPermissionWatcher`, `broadcastPermissionMode`, and the `SetPermissions` wiring. `AllowToolCall`/`DenyToolCall`/`GetPermissionMode`/`SetPermissionMode` are `proto.AgentServer` RPCs — keep thin delegators on `Server`.

**Interface the front door depends on:**
```go
package permissions

type Broker interface {
    Mode() agent.PermissionMode
    SetMode(m agent.PermissionMode) error
    // Wait blocks for the client's allow/deny on toolUseID (used by the tool loop's requester).
    Resolve(toolUseID string, allow, persist bool)
    Wait(ctx context.Context, toolUseID string) (agent.Decision, error)
    StartWatcher(ctx context.Context)
}
```
(Match the real signatures of the existing `PermissionStore` / `PendingDecisions` methods the tool loop already calls — grep `s.pendingDecisions.` and `s.permStore.` in `server.go` and the tool-loop requester closure to get exact names/types; the interface must cover exactly those call sites plus the RPCs.)

**Gotchas:** the tool-loop `requester` closure in `streamProcessRequestWithToolLoop` calls `s.pendingDecisions.Wait(...)` and the persist-on-allow path calls `s.permStore.AddMCPAllow(...)`. Those call sites now go through the injected broker. `broadcastPermissionMode` uses the event hub — pass the hub (or a `broadcast func(mode string)`) into the broker's constructor, don't reach back into `Server`.

**Characterization:** covered by `agentic_perms_test.go` and `permission_watcher`-adjacent tests — confirm they pass before and after; no new characterization test needed.

**Commit:** `refactor(server): extract permission broker into hostsvc/permissions`

---

## Task 2: Extract `hostsvc/config`

Extract early so downstream services (providers, persistence, runtimes) can consume a `ConfigService` instead of reaching into `s.currentConfig`.

**Files:**
- Create: `internal/hostsvc/config/config.go`, `internal/hostsvc/config/config_test.go`
- Modify: `internal/server/server.go`, `internal/server/config_watcher.go`, `internal/server/cloud_backup.go`, and cloud-profile methods in `server.go`

**Fields to move:** `configPath string`, `currentConfig config.Config`, `cfgMu sync.RWMutex`, `secrets secrets.Store`.

**Methods to move:** `GetConfig`, `UpdateConfig`, `SetConfigPersistence`, `persistConfig`, `reloadConfigFromDisk`, `StartConfigWatcher`, `SetSecrets`, `broadcastConfigChanged`; the cloud-profile set: `GetCloudProfiles`, `UpsertCloudProfile`, `RemoveCloudProfile`, `SetActiveCloudProfile`, `SetBackupCloudProfile`, `SetCloudProfileKey`, `activeProfile`, `wrapBackupLocked`. (`GetConfig`/`UpdateConfig`/`GetCloudProfiles`/`UpsertCloudProfile`/`RemoveCloudProfile` are RPCs — thin delegators stay on `Server`.)

**Interface:**
```go
package config

type Service interface {
    Get() cfg.Config                 // snapshot copy (holds RLock internally)
    Update(changes cfg.Config) error // persists + notifies subscribers
    Path() string
    Secrets() secrets.Store
    ActiveProfile() cfg.CloudProfile
    // Subscribe registers a callback fired after any committed config change.
    Subscribe(fn func(cfg.Config))
    // plus the cloud-profile CRUD the RPCs delegate to
}
```
(Exact `cfg.Config` shape is the existing `config.Config`; keep the alias/import name consistent. Grep every `s.currentConfig` and `s.cfgMu` read/write to enumerate the real accessor surface — there are many; the interface must expose a `Get()` snapshot used by all readers rather than exposing the mutex.)

**Gotchas:** MANY sites read `s.currentConfig` under `s.cfgMu.RLock()`. Replace each with `cfgSvc.Get()` (returns a snapshot). This is the largest mechanical fan-out in the phase — do it methodically; the compiler finds every site. `broadcastConfigChanged` fans out via the event hub AND triggers `rebuildCloud` (providers) and `syncMeridianForProfile` (runtimes) — for now, keep those cross-service reactions wired via the `Subscribe` callback registered by the front door (front door subscribes and calls the provider/runtime services). Do NOT let config call providers directly.

**Characterization:** `cloud_profiles_test.go`, `config_watcher_test.go` cover this — confirm green throughout.

**Commit:** `refactor(server): extract config service into hostsvc/config`

---

## Task 3: Extract `hostsvc/providers`

**Files:**
- Create: `internal/hostsvc/providers/providers.go`, `providers_test.go`
- Modify: `internal/server/server.go`, `internal/server/cloud_models.go`, `internal/server/models_resolve.go`

**Fields to move:** `cloudLLMProvider llm.Provider`, `openLLMProvider llm.Provider`, `openProvider *legacymodels.OpenModelProvider`, `cloudFactory agent.CloudFactory`, `router RouterCloudUpdater`, `coordinator *loop.ADKCoordinator`, `registry *engine.EngineRegistry`, `catalogManager *ollamacatalog.Manager`.

**Methods to move:** `resolveMainProvider`, `resolveTierModel`, `mainModelFor`, `primaryModel`, `activeCloudModel`, `LocusMode`, `rebuildCloud`, `RebuildCloud`, `rebuildCloudLocked`, `installAbsentCloud`, `applyRuntimeEndpoints`, `refreshRuntimeEndpoints`, `ListModels`, `ListCloudProfileModels`, `GetModelRAMEstimate`, `SetCloudLLMProvider`, `SetOpenLLMProvider`, `CloudLLMProvider`, `OpenLLMProvider`, `SetCatalogManager`. (`ListModels`/`ListCloudProfileModels`/`GetModelRAMEstimate` are RPCs — delegators stay.)

**Interface:**
```go
package providers

type Resolver interface {
    // Main returns the provider + model for the active locus mode, plus fallback signal.
    Main() (prov llm.Provider, isCloud bool, fellBack bool, err error)
    MainModel(isCloud bool) string
    PrimaryModel() string
    Rebuild() error   // re-derive providers from current config (was rebuildCloud)
    Cloud() llm.Provider
    Open() llm.Provider
}
```
(Grep `resolveMainProvider`, `s.cloudLLMProvider`, `s.openLLMProvider`, `mainModelFor`, `primaryModel` call sites — `streamProcessRequestWithToolLoop` and `runAgenticDispatch` are the hot consumers. The interface must cover exactly what they call.)

**Gotchas:** `resolveMainProvider` reads locus mode from config → takes a `config.Service` dependency (injected). `Rebuild()` is triggered by the config `Subscribe` callback (registered in the front door in Task 2) — verify the wiring lands here. `applyRuntimeEndpoints`/`refreshRuntimeEndpoints` bridge to runtimes (Task 6) — for now they can stay as provider methods reading a runtime-endpoints snapshot; note the coupling for Task 6.

**Characterization:** `models_resolve_test.go`, `cloud_models`-adjacent tests. Add a characterization test for `resolveMainProvider` under each locus mode if not already covered (grep for existing coverage first).

**Commit:** `refactor(server): extract provider resolver into hostsvc/providers`

---

## Task 4: Extract `hostsvc/tools`

**Files:**
- Create: `internal/hostsvc/tools/tools.go`, `tools_test.go`
- Modify: `internal/server/server.go`, `internal/server/agentic_dispatch.go`, `internal/server/skills.go`, `internal/server/invoke_capability.go`, `internal/server/tool_call.go`

**Fields to move:** `toolRegistry *agenttools.Registry`, `capRegistry *capabilities.Registry`, `dispatchEngine *dispatch.Engine`.

**Methods to move:** `SetToolRegistry`, `ToolRegistry`, `ListTools`, `InvokeTool`, `InstallCapabilities`, `InvokeCapability`, `SetDispatchEngine`, `ListSkills`, `GetSkill`, `GetToolCall`, `runAgenticDispatch`, `grantedRegistry`, `resolveGrantName`, `availableToolsHint`, `dispatchStore`. (`ListTools`/`InvokeTool`/`ListSkills`/`GetSkill`/`GetToolCall`/`InvokeCapability` are RPCs — delegators stay.)

**Interface:**
```go
package tools

type Catalog interface {
    Registry() *agenttools.Registry
    // GrantedRegistry builds a least-privilege sub-registry for a dispatch (was grantedRegistry).
    GrantedRegistry(names []string, mode agent.PermissionMode) (*agenttools.Registry, error)
    Skills() []skills.Skill
    Skill(name string) (skills.Skill, bool)
}
```

**Gotchas:** `runAgenticDispatch` is wired onto the `dispatch.Engine` via `SetDispatchEngine` and consumes providers + persistence + permissions (it builds a `ToolLoopInput`). Keep `runAgenticDispatch` in this service but inject the provider resolver, conversation service, and permission broker it needs — it must NOT reach back into `Server`. This is the most cross-cutting method in the phase; give it a constructor that takes those three collaborators. `GetToolCall` reads the conversation store — inject the conversation service.

**Characterization:** `agentic_dispatch_test.go`, `agentic_observability_test.go`, `agentic_perms_test.go`, `agentic_session_scope_test.go` cover the dispatch path heavily — confirm green throughout. These are the load-bearing guard for the trickiest move.

**Commit:** `refactor(server): extract tool catalog into hostsvc/tools`

---

## Task 5: Extract `hostsvc/persistence`

**Files:**
- Create: `internal/hostsvc/persistence/persistence.go`, `persistence_test.go`
- Modify: `internal/server/server.go`, `internal/server/context_turns.go`, `internal/server/context_edit.go`, `internal/server/context_regen.go`

**Fields to move:** `retentionSweeper *retention.Sweeper`, `compactionGen *compactiongen.Generator`, `contextLoader *projectctx.Loader`. (The conversation store lives on `agent`; this service wraps access to it, it doesn't own the `*agent.Agent`.)

**Methods to move:** `ListConversations`, `GetConversation`, `ResumeConversation`, `DeleteConversation`, `RenameConversation`, `GetConversationTurns`, `DeleteConversationTurns`, `GetContextUsage`, `GetCompactionState`, `ExportContext`, `persistTurn`, `assembleHistory`, `RegenerateContext`, `ProposeContextEdit`, `SuggestNextPrompt`, `loadProjectContext`, `SetCompactionGenerator`, `SetRetentionSweeper`, `SetContextLoader`. (Conversation/context RPCs — delegators stay.)

**Interface:**
```go
package persistence

type Service interface {
    PersistTurn(ctx context.Context, convID string, m llm.Message)
    AssembleHistory(ctx context.Context, convID string) []llm.Message
    Store() conversation.Store
    LoadProjectContext(workDir string) string
}
```
(`persistTurn` and `assembleHistory` are the hot consumers from the turn loop — grep `s.persistTurn` and `s.assembleHistory` call sites; those go through this interface.)

**Gotchas:** `buildSystemPrompt` calls `loadProjectContext` — decide whether `buildSystemPrompt` stays on the front door (it composes env + steering + project context) consuming the persistence service, or moves. Keep it on the front door for now; it consumes `persistence.LoadProjectContext`. `assembleHistory` reads compaction config from the config service — inject it.

**Characterization:** `context_turns_test.go`, plus conversation tests in `server_test.go`/`toolloop_persist_test.go`. Confirm green.

**Commit:** `refactor(server): extract conversation/persistence service into hostsvc/persistence`

---

## Task 6: Extract `hostsvc/runtimes`

**Files:**
- Create: `internal/hostsvc/runtimes/runtimes.go`, `runtimes_test.go`
- Modify: `internal/server/server.go`, `internal/server/open_runtime_install.go`, `internal/server/cloud_models.go`, `internal/server/chatgpt_login.go`

**Fields to move:** `meridianMgr *meridian.Manager`, `runtimeManager localruntime.Manager`, `mcpManager McpManager`.

**Methods to move:** `SetupMeridian`, `syncMeridianForProfile`, `broadcastMeridianStatus`, `SetRuntimeManager`, `GetRuntimeStatus`, `ListRuntimeModels`, `ListRuntimeEndpoints`, `StartRuntimeModel`, `StopRuntimeModel`, `RestartRuntime`, `DownloadRuntimeModel`, `CancelRuntimeModelDownload`, `DeleteRuntimeModel`, `StreamRuntimeLogs`, `RefreshOnlineCatalog`, `InstallOpenRuntime`, `GetOpenRuntimeStatus`, `broadcastOpenRuntimeStatus`, `SetMcpManager`, `ListMcpServers`, `AddMcpServer`, `RemoveMcpServer`, `RestartMcpServer`, `StartChatGPTLogin`. (The many runtime/MCP RPCs — delegators stay.)

**Interface:**
```go
package runtimes

type Supervisors interface {
    Meridian() *meridian.Manager
    SyncMeridianForProfile(ctx context.Context, p cfg.CloudProfile)
    Endpoints() []localruntime.Endpoint   // was refreshRuntimeEndpoints, for providers
    // MCP + runtime lifecycle methods the RPC delegators call
}
```

**Gotchas:** `syncMeridianForProfile` is triggered by the config `Subscribe` callback (registered in the front door, Task 2). The provider service's `applyRuntimeEndpoints`/`refreshRuntimeEndpoints` (Task 3) reads runtime endpoints — now via `runtimes.Endpoints()`. The status broadcasts use the event hub — inject the hub/broadcast func. This service composes the Meridian supervisor pattern that Phase 5's workers will join.

**Characterization:** `meridian_test.go` and runtime-adjacent tests; add a characterization test for `syncMeridianForProfile` if uncovered.

**Commit:** `refactor(server): extract runtime supervisors into hostsvc/runtimes`

---

## Task 7: Slim the front door + composition root

After Tasks 1–6, `Server` holds service interfaces + the turn/broker glue. This task cleans up the wiring and confirms the struct is actually slim.

**Files:**
- Modify: `internal/server/server.go` (the `Server` struct + `NewServer` + remaining `Set*` methods), `internal/server/events.go`

**Steps (follows the pattern, plus):**
- The `Server` struct should now be: the service interfaces (`config.Service`, `providers.Resolver`, `tools.Catalog`, `persistence.Service`, `runtimes.Supervisors`, `permissions.Broker`), the event hub, the turn/broker state (`turnsMu`, `activeTurns`, `turnGens`), `agent *agent.Agent`, `watchdog`, `usageSink`. Confirm no orphan fields remain.
- `NewServer` becomes the composition root: construct each service with its collaborators, register the config `Subscribe` callbacks (→ providers.Rebuild, runtimes.SyncMeridianForProfile), inject services into each other, return the wired front door. The many `Set*` methods that tests use to inject collaborators should now forward to the owning service (keep them for test compatibility, delegating).
- Keep `beginTurn`/`turnIsCurrent`/`turnGenLocked`/`hasActiveTurn`/`runMainLoop`/`streamProcessRequestWithToolLoop`/`SubscribeEvents`/`BeginShutdown`/`Shutdown` on the front door — Phase 3 extracts the turn execution into the runner; this phase leaves them.
- **Gate:** `go build ./... && go vet ./... && go test ./... -count=1` green; `wc -l internal/server/server.go` is dramatically smaller (target: the bulk of the ~2900 lines has moved to `hostsvc/*`); `grep -c "s\.currentConfig\|s\.cfgMu\|s\.permStore\|s\.pendingDecisions\|s\.toolRegistry\|s\.meridianMgr" internal/server/server.go` is 0 (all field access now goes through services).

**Commit:** `refactor(server): slim front door to a composition root over hostsvc/*`

---

## Sequencing rationale

Permissions first (smallest leaf, proves the pattern with minimal blast radius). Config second (most-depended-upon — extracting it early lets every later service consume `config.Service` instead of threading `s.currentConfig`). Then providers → tools → persistence → runtimes (increasing entanglement; `runAgenticDispatch` in tools is the most cross-cutting single method, so it comes after its provider/persistence/permission dependencies exist as injectable services). Front-door cleanup last. Each task is independently shippable — the suite is green after every commit — so the sequence can pause between any two tasks.

## What Phase 2 deliberately does NOT do

- Does not touch the turn loop / `streamProcessRequestWithToolLoop` internals beyond repointing field access through services — Phase 3 extracts the `TurnRunner`.
- Does not add the worker process or multi-surface attach — Phases 4–5.
- Does not change any gRPC RPC signature or client-visible behavior.

## Self-review

- **Coverage:** every field group in the design's decomposition table maps to a task (permissions T1, config T2, providers T3, tools T4, persistence T5, runtimes T6, front-door/broker T7). The broker is left minimal here and grown in Phase 4 per the design.
- **No behavior change:** every task's gate is the existing suite green + vet clean; thin-coverage services get a characterization test first (T3 provider-resolve, T6 syncMeridian flagged).
- **Type consistency:** interface names used consistently (`config.Service`, `providers.Resolver`, `tools.Catalog`, `persistence.Service`, `runtimes.Supervisors`, `permissions.Broker`); `runAgenticDispatch` placed in tools with injected collaborators, consistent with T3/T5/T1 producing those collaborators first.
- **Known unknowns (resolve at implementation, flagged not hidden):** exact accessor surface of each service (the plan says "grep every `s.<field>` site" per task — the compiler enumerates them); whether `buildSystemPrompt` stays on the front door (T5 says yes for now). These are enumeration tasks, not design gaps.
