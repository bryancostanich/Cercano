# Agent execution isolation — architecture (as-built)

**Status:** as-built through Phase 4 (landed) + Phase 5 (worker, in progress).
This is the AS-BUILT complement to `design.md` (the forward-looking design) and
the per-phase plans (`plan-phase{2,3,4,5}.md`). Where the design describes intent,
this describes the code as it stands: which types exist, what they own, and how a
turn actually flows through them. Read `design.md` first for the *why*; read this
for the *what-is*.

> **Founding principle:** *an agent dying must not take down another agent.*
> Every structural decision below traces back to that sentence.

---

## 1. Purpose — the problem it solves

The agent server (`internal/server.Server`) is a singleton gRPC process. Before
this work, every conversation's turn executed in that one address space, sharing
mutable execution state. That produced a recurring class of bugs — each fixed
one at a time, all the same defect underneath:

- **cwd cross-talk.** The turn handler did a process-global `os.Chdir(WorkDir)`
  with `defer os.Chdir(prev)`. Concurrent turns from different conversations
  raced the process cwd; a relative-path `Read`/`Write`/`Bash` in one
  conversation could resolve into another's repo.
- **Session cross-delivery / turn overlap.** Shared upstream-session routing
  state and shared turn state let two turns interleave history writes or share
  one upstream session key.
- **No crash isolation.** One panic, OOM, or fatal runtime error took down
  *every* conversation, because they all lived in one process.
- **The `Server` god-object.** One struct with ~30 fields and three cross-cutting
  mutexes fused every responsibility (client streams, gRPC, config, providers,
  the DB, subprocess managers, permissions, the turn loop). Each bug above was
  "a field on the big `Server` mutated by two paths." It was also un-embeddable:
  it dragged a gRPC server and subprocess managers with it.

The goal: make the cross-talk bug class **unrepresentable** (owned per-conversation
execution state), add **crash isolation** (one conversation's death contained),
support **multi-surface attach** (CLI + VS Code on one conversation), and keep the
core **embeddable** (runs in-process when Cercano is hosted in another app).

**Core idea — one execution core, two deployment wrappers.** The correctness bugs
come from shared *mutable state*, not from the lack of a process boundary. So the
isolation lives in an execution **core** (`internal/runner.Core`) that holds zero
process-global mutable state; the process boundary is an *additional* wrapper on
top (`internal/worker`), not a second implementation. The same `Core` runs
in-process (embedded / test mode) or one-per-child-process (worker mode).

---

## 2. The big picture

```
                         ┌──────── surfaces ────────┐
                         │  CLI      VS Code   Zed   │  (gRPC clients)
                         └────┬─────────┬────────────┘
                              │ StreamProcessRequest / AttachConversation
                              │ (proto)
   ┌──────────────────────────▼───────────────────────────────────────────┐
   │  HOST PROCESS  (singleton :50052)                                      │
   │                                                                        │
   │  ┌───────────────────── Server (front door) ───────────────────────┐  │
   │  │  proto.UnimplementedAgentServer + handlers; ~13 fields.          │  │
   │  │  terminate client conns, route, proto ↔ domain. Thin.           │  │
   │  └───┬───────────────┬───────────────┬──────────────┬─────────────┘  │
   │      │ delegates to  │               │              │ turnRunner      │
   │      ▼               ▼               ▼              ▼  .RunTurn(...)   │
   │  ┌─────────┐   ┌──────────┐   ┌───────────┐   ┌──────────────────┐    │
   │  │ hostsvc │   │  broker  │   │  hostsvc  │   │   TurnRunner     │    │
   │  │ services│   │ (fan-out,│   │  services │   │  (interface)     │    │
   │  │ (6)     │   │ turn     │   │           │   │                  │    │
   │  └─────────┘   │ fence,   │   └───────────┘   │  in-process:     │    │
   │   SHARED /     │ replay)  │                   │   runner.Core    │    │
   │   host-owned:  └──────────┘                   │  OR              │    │
   │   config, DB,     ▲  fan-out to surfaces      │  worker mode:    │    │
   │   keychain,       │                           │   workerRunner ──┼────┼──┐
   │   permissions,    └── events published ───────┘                  │    │  │
   │   broker                                       └──────────────────┘    │  │
   └────────────────────────────────────────────────────────────────────────┘  │
                                                                                │ spawn +
                                          ISOLATED per-conversation execution   │ bidi gRPC
                                                                                ▼ (unix socket)
                                          ┌──────────────────────────────────────────┐
                                          │  WORKER PROCESS  ("cercano worker")        │
                                          │  proto.Worker server. Stateless compute.   │
                                          │  Builds execution Deps from ConfigSnapshot │
                                          │  + host-resolved credential; runs the SAME │
                                          │  runner.Core; proxies state ops to host:   │
                                          │   events · permission · persist · cred     │
                                          └──────────────────────────────────────────┘
```

**SHARED vs ISOLATED — the load-bearing split:**

| Host-owned (SHARED, singleton) | Per-conversation (ISOLATED) |
|---|---|
| config + secrets | turn execution (tool loop, tool exec, LLM stream) |
| conversation DB (one writer) | cwd (carried in `Request.WorkDir`, never `os.Chdir`) |
| keychain / OAuth refresh | provider/session identity |
| broker: fan-out, turn fence, replay | the `runner.Core` (or the worker process running it) |
| permission decisions (policy + prompt) | |

The worker holds **no durable state**. Anything stateful/UI it needs, it proxies
back to the host over the stream.

---

## 3. The layers / components

### 3.1 The front door — `internal/server/server.go`

`Server` embeds `proto.UnimplementedAgentServer` and terminates client
connections. Phase 2 shrank it from a ~30-field god-object to ~13 fields; it now
*delegates* to focused services instead of *being* them. Its job is thin:
terminate gRPC, route RPCs, map proto ↔ domain. Its fields:

```
agent        *agent.Agent
providerSvc  providers.Resolver      // the 6 hostsvc services ↓
cfgSvc       cfgsvc.Service
toolSvc      toolssvc.Catalog
persistSvc   persistsvc.Service
permBroker   permissions.Broker
runtimesSvc  runtimessvc.Supervisors
watchdog     *watchdog.Watchdog
turnRunner   runner.TurnRunner       // in-process Core OR workerRunner
events       *eventHub               // legacy server→client push
turnBroker   *broker.Broker          // Phase 4 turn fence + fan-out
```

The turn handler `streamProcessRequestWithToolLoop` (server.go ~1978) is the
single per-turn choreographer: it opens the turn fence (`beginTurn`), attaches
the initiator as the first (lossless) broker subscriber, spins the turn in a
goroutine that calls `s.turnRunner.RunTurn(ctx, req, sink, requester, persist)`
(server.go ~2081), and drains broker events to the initiator's stream. **This
handler is identical regardless of execution mode** — `turnRunner` is an
interface (§3.4, §3.6).

### 3.2 The six host services — `internal/hostsvc/`

Phase 2 decomposed the god-object into six services behind interfaces, wired by
the front door as a thin composition root. One line each:

| Service | Package | Owns |
|---|---|---|
| **Config** | `hostsvc/config` | the live `config.Config` + secrets; parse/validate/persist; change notification. Sole owner of `configPath`/`cfgMu`. |
| **Providers** | `hostsvc/providers` | given config + locus, hands out the right provider (cloud/open, router, coordinator, engine registry, catalog manager). Implements `providers.Resolver`. |
| **Tools** | `hostsvc/tools` | the tool universe + dispatch — tool registry, capability registry, dispatch engine. Implements `tools.Catalog`. |
| **Persistence** | `hostsvc/persistence` | durable conversation state — the shared transcript; retention sweeper, compaction gen, context loader. One writer. Implements `persistence.Service`. |
| **Permissions** | `hostsvc/permissions` | policy + the confirm round-trip — permission store, pending decisions, mode. Implements `permissions.Broker`. |
| **Runtimes** | `hostsvc/runtimes` | spawned-subprocess lifecycle — meridian manager, local runtime manager, MCP manager. **Workers join this family.** Implements `runtimes.Supervisors`. |

Why full decomposition is load-bearing (not aesthetic): (1) it is the precondition
for a cuttable worker boundary — a worker needs exactly config + providers + tools
+ persist/permission callbacks, and you cannot hand that clean subset out of a
tangled struct; (2) it makes Cercano embeddable — a host app instantiates the
service set it wants; (3) the bugs we fought were god-object symptoms; (4) lock
soup (three cross-cutting mutexes) becomes per-component ownership.

### 3.3 The broker — `internal/broker/broker.go`

The `Broker` owns three per-conversation concerns, all under one `mu`. It is
**proto-free** and does not import `internal/server`.

- **Turn exclusivity + generation fence.** `BeginTurn(parent, conv)` supersedes
  any turn already live on that conversation (cancels its ctx), increments the
  per-conversation generation counter, resets the replay buffer, and returns
  `(ctx, gen, release)`. `IsCurrent(conv, gen)` is the fence: a superseded turn
  fails it and goes quiet. `Publish`/persist paths check the gen, so a stale
  turn's late events and writes are silently dropped — never interleave into
  live history.
- **Fan-out.** `Publish(conv, gen, ev)` fans a `runner.Event` to every attached
  subscriber, under `mu`, fenced by gen.
- **Replay buffer.** `Publish` also appends to the current turn's buffer.
  `Attach` atomically snapshots that buffer (replay) *and* registers a live
  channel under the same lock, so every event is **either** in replay (published
  before Attach) **or** on the channel (published after) — never both, never
  neither. This is the mid-turn-join guarantee.
- **Lossy vs lossless subscribers.** `Attach` returns a cap-64 **drop-on-full**
  channel — correct for a passive observer that must never stall the turn.
  `AttachLossless` returns a channel backed by an unbounded queue drained by a
  background goroutine — Publish never drops, never blocks. The turn's initiator
  uses `AttachLossless` (its stream is authoritative output); passive attachers
  use `Attach`.

### 3.4 The runner — `internal/runner/{runner.go,core.go,event.go}`

The execution **core**, where correctness lives. It is **transport-agnostic,
proto-free, provider-free**, and holds **zero process-global mutable state** —
which is what makes N cores safe in one process (embedded mode) and one core per
worker process (worker mode) *the same code path*.

- **`TurnRunner` interface** (runner.go): a single method
  `RunTurn(ctx, req, sink, requester, persist) (Result, error)`. Both the
  in-process `Core` and the host-side `workerRunner` satisfy it.
- **`Request`** carries only user-facing turn inputs: `ConversationID`, `Input`,
  `Images`, `WorkDir`, `Gen`. It deliberately holds **no** `llm.Provider` and
  **no** assembled history — the runner resolves the provider and assembles
  history itself, so it works across a process boundary.
- **`Deps`** are the shared services injected once at construction:
  `Providers` (resolver), `Tools`, `Persist` (a narrow `TurnHistory`), `Config`,
  `Perms`, `Agent`, and a **live** `Watchdog` accessor. `TurnHistory` and
  `ToolSvc` are *narrow* interfaces defined in the runner package precisely so
  the runner stays proto-free — the full `persistence.Service` / `tools.Catalog`
  carry proto-typed methods that would drag `pkg/proto` in.
- **`Event` vocabulary** (event.go): a proto-free `Event` struct + `EventKind`
  enum (route-selected, token, progress, tool-use-start/stop, tool-exec-start/
  complete, watchdog, done). The host maps these to `proto.StreamProcessResponse`
  payloads; embedded mode consumes them directly; a worker serializes them over
  its stream. `EventSink.Emit` is fire-and-forget; `PermissionRequester` is a
  request/response func (separate because it round-trips); `PersistFunc` is
  fire-and-forget and host-fenced.
- **`Core.RunTurn`** (core.go) does the whole turn: resolve provider from locus,
  assemble history, persist the user turn up front (crash-resilience), build the
  watchdog gate + gated registry, run `agent.RunToolLoop`, do cross-tier fallback
  on error, and post-turn bookkeeping (recap, compaction, token accounting). Note
  `buildSystemPrompt` and the directory/git snapshot live here (proto-free copies)
  and use `req.WorkDir` — never `os.Chdir`.

### 3.5 The worker — `internal/worker/`

The worker is a child process that runs the execution of **one** conversation's
turn in isolation and holds **no durable state**. Worker-side files:

- **`worker.go`** — `WorkerServer` implements the `proto.Worker` bidi service.
  `RunTurn` receives `StartTurn`, builds execution `Deps` (local providers + tools
  from the `ConfigSnapshot`, stream-backed permissions, preloaded history), then
  runs the **unchanged** `runner.New(deps).RunTurn(...)` with stream-backed
  sink/requester/persist. It builds providers via `buildWorkerProviders`
  (a minimal `workerResolver`) and tools via `buildWorkerTools` (the full
  built-in registry; **MCP tools excluded** — MCP servers run host-side only).
  Panics in the turn are recovered into a `TurnError`.
- **`proxies.go`** — the stream-backed `Deps`/callback proxies: a single
  serialized `sender` goroutine (gRPC `Send` is not concurrency-safe);
  `streamEventSink` (Emit → `WorkerEvent`), `streamPermissionRequester`
  (id-correlated round-trip), `streamCredentialSource` + `streamTokenSource`
  (id-correlated credential fetch; the ChatGPT-subscription token source), and
  `streamPersistFunc` + `preloadedHistory` (Assemble/LoadProjectContext return
  the StartTurn-provided values; PersistTurn proxies to the host).
- **`wire.go`** — the codecs: `MarshalMessage`/`UnmarshalMessage` (lossless
  `llm.Message ↔ LLMMessage` via `blocks_json`), `MarshalEvent`/`UnmarshalEvent`,
  `SnapshotConfig`/`ConfigFromSnapshot`.
- **`host.go`** — the host-side `workerRunner` (see §3.6 / §4-worker).
- **`spawn.go`** — host-side process lifecycle: `spawnWorker` finds the `cercano`
  binary (sibling-then-$PATH), starts `cercano worker --socket <path>` with
  `Setpgid` + a pidfile (mirroring `internal/meridian/manager.go`), polls the
  unix socket to ready, and gRPC-dials it. `workerHandle.Kill()` kills the
  process group, closes the conn, removes socket + pidfile — idempotent.
- **`cmd/cercano/main.go`** — the `worker` subcommand (`runWorkerMode`): parse
  `--socket`, serve `proto.Worker` on the unix listener. Not user-invoked; the
  host spawns it.

### 3.6 The `workerRunner` (host side) — `internal/worker/host.go`

`workerRunner` implements `runner.TurnRunner`, so the host turn handler is
**unchanged** — only the concrete `turnRunner` type differs. Its `RunTurn`:
pre-assembles history + project context (it owns the store), builds the
`ConfigSnapshot` + permission mode, spawns the worker (or uses an injected dial
in tests), sends `StartTurn`, then runs a **drain loop** mapping worker→host
messages onto the host callbacks it was handed. It is the mirror of the worker's
proxies. §5 details the transport.

---

## 4. A turn's lifecycle — both modes

The host turn handler (`streamProcessRequestWithToolLoop`) is identical either
way. The only difference is which `TurnRunner` `RunTurn` runs behind the interface.

### 4.1 IN-PROCESS mode (embedded / test / current default)

1. **Front door.** Client calls `StreamProcessRequest`. The handler calls
   `beginTurn(stream.Context(), convID)` → `broker.BeginTurn`, which supersedes
   any prior turn on this conversation and returns `(ctx, gen, release)`.
2. **Attach initiator.** The handler `AttachLossless(convID)` — the initiator is
   just the first subscriber. It builds the `sink` (Publish to broker at `gen`),
   the `requester` (host permission round-trip), and the fenced `persist`.
3. **Run.** In a goroutine, `s.turnRunner.RunTurn(ctx, runReq, sink, requester,
   persist)` — here `turnRunner` is an in-process `runner.Core`. `Core.RunTurn`
   resolves the provider, assembles history, runs the tool loop, and emits
   `runner.Event`s via `sink.Emit` → `broker.Publish(convID, gen, ev)`.
4. **Fan-out.** The broker fans each event to every attached subscriber; the
   handler drains the initiator's lossless channel and `sendRunnerEvent`s each to
   the client stream.
5. **Done.** `RunTurn` returns a `Result`; the handler sends the terminal
   response; `release()` retires the turn.

### 4.2 WORKER mode (crash-isolated; Phase 5)

Steps 1, 2, 4, 5 are **byte-identical** — same handler, same broker, same
callbacks. Only step 3 changes: `turnRunner` is a `workerRunner`, and
`workerRunner.RunTurn` (host.go):

1. **Pre-assemble.** `history := persist.AssembleHistory(ctx, convID)`;
   `projectCtx := persist.LoadProjectContext(workDir)` — the host owns the store.
2. **Snapshot.** `snap := SnapshotConfig(cfg, "")` (no credential in the snapshot —
   the worker fetches on demand); `permMode := perms.Mode()`.
3. **Spawn.** `spawnWorker(ctx, convID, gen)` → `cercano worker --socket …`,
   Setpgid + pidfile, poll-to-ready, gRPC-dial. (Test path: injected `dial`.)
4. **Start.** Open `client.RunTurn(ctx)` bidi stream; `Send(StartTurn{input,
   images, workDir, gen, config, history, projectContext, permissionMode})`.
5. **Drain loop.** For each `WorkerToHost` message:
   - `WorkerEvent` → `UnmarshalEvent` → `sink.Emit` (→ broker → surfaces).
   - `PermissionRequest` → call host `requester(...)` in a goroutine (a slow human
     must not block the event drain) → send `PermissionResponse`.
   - `CredentialRequest` → `resolveCredential` on the host (keychain / OAuth) →
     send `CredentialResponse`.
   - `PersistTurn` → `UnmarshalMessage` → host `persist(m)` (fenced by gen).
   - `TurnDone` → capture `Result`, keep draining until stream EOF, return nil.
   - `TurnError` → return the error.
6. **Meanwhile, in the worker:** the `WorkerServer.RunTurn` handler runs the
   **same `runner.Core`** with stream-backed proxies. Its events, permission asks,
   persist calls, and credential asks all go back over the stream — exactly what
   the host drain loop consumes.
7. **Teardown.** `defer cleanupFn()` (`workerHandle.Kill()`) always fires:
   process group killed, socket + pidfile removed.

The payoff: identical client-visible behavior, but the entire tool loop + tool
execution + LLM streaming ran in a process the host can kill without harm.

---

## 5. The worker transport — `proto.Worker` (`source/proto/agent.proto`)

A **separate** service from `Agent` (the worker's failure surface must not touch
the client-facing service). The host is the gRPC **client**; the worker **serves**:

```
service Worker {
  rpc RunTurn (stream HostToWorker) returns (stream WorkerToHost) {}
}

HostToWorker { oneof: StartTurn | PermissionResponse | Cancel | CredentialResponse }
WorkerToHost { oneof: WorkerEvent | PermissionRequest | PersistTurn
                    | TurnDone | TurnError | CredentialRequest }
```

`StartTurn` carries `conversation_id`, `input`, `images`, `work_dir`, `gen`,
`ConfigSnapshot`, pre-assembled `history` (`[]LLMMessage`), `project_context`, and
`permission_mode` (the worker's gating mode **must match** the host — never
default, or a strict host would silently auto-run writes in the worker).

**`LLMMessage`** is the wire form of `llm.Message`: `role` + `blocks_json`
(`json.Marshal(m.Blocks)`) + `content`. `blocks_json` is the same encoding the
persistence layer already uses, so round-trip fidelity equals what the DB
guarantees — lossless for all block kinds. **`ConfigSnapshot`** carries the ~37
config fields the worker's execution slice reads at turn time (locus mode, the
active cloud profile, model tiers, compaction knobs, the full watchdog block) —
and deliberately **excludes** host-only concerns (Port, LlamaServer, Embedding,
Retention). `WorkerEvent` mirrors `runner.Event` flat (a `WorkerEventKind` enum +
all fields), so host↔worker event mapping is a trivial inverse.

**The four proxied operations** (worker asks; host owns the state):

1. **Events** — worker `WorkerEvent` → host `sink.Emit` → broker fan-out.
2. **Permission round-trip** — worker `PermissionRequest{id,…}` blocks on a
   per-id channel; host prompts its client and answers `PermissionResponse{id}`.
3. **Persist** — worker `PersistTurn{message, gen}` (one-way) → host writes it,
   fenced by gen. One writer, host-side.
4. **Credential-on-demand** — worker `CredentialRequest{id, profile_name}`; host
   `resolveCredential` reads the keychain (static-key route: `secrets.Get`) or
   refreshes the OAuth token (ChatGPT-subscription route:
   `chatgptauth.NewSource(...).Token`) and answers `CredentialResponse{id, token,
   account}`.

**The credential proxy — why it matters.** The worker holds **no durable
credential**. Earlier design put a resolved credential in the `ConfigSnapshot`
(the `resolved_credential` field is now deprecated/unread). The proxy supersedes
it: the worker asks the host over the stream, exactly like permissions. Two
payoffs — (a) the host stays the sole owner of the keychain and of
ChatGPT-subscription OAuth refresh (no secret ever sits statically in the child),
and (b) it dissolves the mid-turn-token-expiry edge and the "ChatGPT-subscription
loses cloud in the worker" limitation: the worker builds a `streamTokenSource` for
the subscription route, so the provider gets fresh tokens on demand from the host.

---

## 6. Multi-surface attach — `AttachConversation` (Phase 4)

The broker fan-out is what makes one conversation watchable from several surfaces.
`AttachConversation` (server.go ~1930) is a server-streaming RPC:

1. `replay, ch, detach := turnBroker.Attach(convID)` — a **lossy** (drop-on-full)
   subscription, correct for a passive observer.
2. Send the `replay` buffer first (everything published before this attach) via
   `sendRunnerEvent`.
3. Forward live events off `ch` until the channel closes (turn ended/superseded)
   or the client disconnects.

Because `Attach` snapshots the replay buffer and registers the channel under one
lock, a second surface that attaches **mid-turn** sees the turn so far (replay)
then the rest live, with no duplicate and no gap. The runner never knows two
surfaces exist — attachment is purely a host concern. A surface can attach whether
or not a runner is currently live; the durable thing is the conversation (store +
broker), the runner is a transient engine behind it.

---

## 7. Crash isolation (Phase 5)

- **Detection.** In `workerRunner.RunTurn`'s drain loop, `stream.Recv()` returns
  EOF or an error. If `TurnDone` was already seen, that's the normal close →
  return the captured `Result`. If **not** (crash mid-turn: panic that killed the
  process, OOM, kill), the worker died before completing → the host returns
  `status.Errorf(codes.Unavailable, "worker turn failed: %v", …)`.
- **Containment.** `defer cleanupFn()` (`workerHandle.Kill()`) kills the worker's
  process group and cleans up. The error flows back through the *same* host turn
  handler as an in-process error would: the handler's `defer release()` retires
  the broker turn, the generation fence guarantees any late/racing event from the
  dead worker is dropped (`Publish` checks `gen`), and attached surfaces see the
  turn end (`codes.Unavailable` surfaced like any transport loss).
- **Blast radius = one conversation.** The host process does not exit. Other
  conversations — their own workers, their own broker turns — are untouched. The
  next submit on the crashed conversation spawns a fresh worker. This is the
  phase's raison d'être and the founding principle made real.
- **Host death** (out of scope for the mid-turn path, noted for completeness):
  workers are the host's children (Setpgid group); clean shutdown kills the group,
  a hard-killed host leaves pidfiles for the next host to reap (Phase 6).

---

## 8. Key invariants

- **Proto-free `runner` + `broker`.** Neither imports `pkg/proto` or
  `internal/server`. `runner` defines *narrow* `TurnHistory`/`ToolSvc` interfaces
  precisely to avoid dragging proto in. This is what lets the same core run
  in-process and in a worker.
- **Zero process-global mutable state in the runner.** `Core` carries all
  execution state in locals/args; nothing global. Directly guarded by
  `internal/runner.TestCore_ConcurrentTurns_NoCrossTalk` — two cores, different
  workdirs/sessions, concurrent under `-race`, asserting zero cross-talk. That
  test *is* embedded mode; it guards the whole design in milliseconds.
- **The generation fence.** At most one live turn per conversation; a superseding
  turn advances the gen; `Publish`/persist check `IsCurrent(conv, gen)`, so a
  superseded (or dead-worker) turn's late events and writes are dropped, never
  interleaved.
- **Host owns all state; worker is stateless compute.** Config, DB (one writer),
  keychain/OAuth, broker, permission decisions all live host-side. The worker
  never opens the DB and never touches the keychain — it proxies.
- **`workerRunner` implements `TurnRunner` → the host handler is unchanged.**
  In-process vs worker is a concrete-type swap behind the interface, not a
  parallel code path with its own bug surface.
- **`WorkDir` + session threaded via ctx/`Request`, never `os.Chdir`.** The turn
  carries its cwd explicitly (`Request.WorkDir`, used by `buildSystemPrompt` and
  the tool loop) and its session id via `anthropic.WithSessionID(ctx, convID)`.
  The process-global `os.Chdir` is gone.
- **Client-visible behavior is identical in both modes.** A turn produces the
  same event stream whether it ran in-process or in a worker; the existing turn
  tests must pass with either runner selected.

---

## 9. Phase roadmap / status

| Phase | Scope | Status |
|---|---|---|
| **1** | cwd isolation — thread `WorkDir`, delete `os.Chdir` | Done |
| **2** | host decomposition — the six `hostsvc` services + thin front door | Done |
| **3** | `TurnRunner` core — proto-free, provider-free, zero global state; concurrent-runner guard test | Done |
| **4** | broker + attach — per-conversation fan-out, replay, `AttachConversation` | Done |
| **5** | worker process — bidi transport, worker binary, host `workerRunner`, credential proxy, basic crash isolation | Done |
| **6** | equivalence (backup failover, watchdog, MCP fallback) + lifecycle (per-conversation reuse, health/restart, idle-reap, orphan-sweep, graceful drain) | Done |

**Phase 5 sub-status (as-built).** The wire protocol (`proto.Worker`, envelopes,
`LLMMessage`, `ConfigSnapshot`, credential messages) and codecs are complete and
round-trip-tested. The worker binary (`worker.go`, `proxies.go`, the `worker`
subcommand) is complete, including the credential proxy (`streamCredentialSource`
/ `streamTokenSource`). The host-side `workerRunner` (`host.go`, `spawn.go`) is
implemented: spawn/dial/drain/crash-detect and host-side credential resolution.
The config toggle is landed: `config.ExecutionMode` defaults to `worker`, and
`Server.SelectExecutionMode()` (called from `runServerMode` after config load)
swaps in `worker.NewWorkerRunner`; the in-process `runner.New(runnerDeps())`
stays the default for embedded mode + the test suite (which construct `Server`
directly and never call the selector). Both-modes parity and the crash-isolation
proof (bufconn + a real spawned-process SIGKILL) are tested.

**Phase 6 as-built — equivalence (Part A).** Worker mode now matches in-process on
the three axes that previously diverged, so `ExecutionMode: worker` is safe as the
default:
- **Backup-profile failover** — the config snapshot carries both active and backup
  profiles; the worker wraps them with `wrapWorkerBackup`, mirroring the host's
  `providers.wrapBackup` (including the ChatGPT-subscription and no-credential
  carve-outs, in the same order).
- **Watchdog** — `buildWorkerWatchdog` reconstructs protocol supervision from the
  snapshot, running its fast-model OneShot lane on the worker's own local engine
  (`buildWorkerEngine`); model resolution mirrors the host's `watchdogModelFor`,
  including the everyday-open fallback so a sparse taxonomy never fails supervision
  open.
- **MCP tools** — MCP servers live host-side, so a conversation carrying MCP tools
  auto-falls-back to the in-process runner. Selection is **per-turn**
  (`Server.pickTurnRunner` consults `hasMCPTools()`), not a one-time startup check,
  because MCP loads asynchronously after `SelectExecutionMode`. Built-in/capability
  turns run in the worker; MCP turns run in-process, transparently.

**Phase 6 as-built — lifecycle (Part B).** Workers are pooled per conversation
(`internal/worker/pool.go`):
- **Warm reuse** — a conversation reuses its worker process across turns when it is
  idle and healthy; otherwise the pool spawns a fresh one (outside the lock).
- **Health-check / restart** — `workerHealthy` gates reuse on process-alive plus a
  live gRPC connection state; an unhealthy or crashed worker is evicted and killed,
  and the next `Acquire` spawns a replacement. A worker crash surfaces to the host
  as `codes.Unavailable`; the host recovers the turn and does not auto-retry.
- **Idle-reap** — a background reaper kills workers idle past a configurable window
  (`config.WorkerIdleTimeout()`: `>0`→seconds, `0`→5-minute default, `<0`→disabled).
- **Orphan-sweep on startup** — `worker.ReapOrphanWorkers()` (`reap.go`) sweeps
  workers left behind by a hard host crash, identity-guarded (process-group +
  `cercano worker` cmdline + spawner-dead) so it never kills an unrelated process,
  mirroring the Meridian supervisor.
- **Graceful drain** — on SIGTERM the host `Shutdown()` drains the pool, killing all
  cached workers (idempotent). No orphans on clean shutdown (drain) or on hard
  crash (next-startup sweep).

---

## 10. Code pointers

- `internal/server/server.go` — the front door; `streamProcessRequestWithToolLoop`
  (turn handler, ~1978), `AttachConversation` (~1930), `runnerDeps` (~625),
  `beginTurn`/`turnIsCurrent` shims.
- `internal/hostsvc/{config,providers,tools,persistence,permissions,runtimes}/` —
  the six host services.
- `internal/runner/{runner.go,core.go,event.go}` — the execution core.
- `internal/broker/broker.go` — turn fence, fan-out, replay, lossy/lossless subs.
- `internal/worker/{worker.go,proxies.go,wire.go,host.go,spawn.go}` — the worker
  transport, stream proxies, codecs, host runner, and process spawn/kill.
- `internal/worker/{pool.go,reap.go,watchdog.go}` — the per-conversation pool
  (reuse/health/idle-reap/drain), startup orphan-sweep, and worker-side watchdog.
- `internal/server/server.go` — `pickTurnRunner`/`hasMCPTools` (per-turn selection),
  `SelectExecutionMode`, `Shutdown` (pool drain).
- `source/proto/agent.proto` — `service Worker` + envelopes (~1129).
- `cmd/cercano/main.go` — the `worker` subcommand (`runWorkerMode`, ~1614),
  `ReapOrphanWorkers` at startup, SIGTERM→`Shutdown`.
- `internal/meridian/manager.go` — the supervisor pattern spawn/reap reuses.
- `docs/agent/agent-isolation/design.md` + `plan-phase{2,3,4,5,6}.md` — the design
  and per-phase rationale.
