# Agent Execution Isolation — Phase 5 Implementation Plan (Worker Process)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run one conversation's turn execution (`internal/runner.TurnRunner`) in a dedicated child process so a turn that hangs, panics, or wedges a tool takes down only that conversation's worker — never the host or any other conversation. The host stays the single owner of all durable/shared state (config, the conversation store, keychain credentials, the broker); the worker is stateless compute that proxies state operations back to the host over a bidi gRPC stream.

**Architecture:** Phase 3 made the runner transport-agnostic (proto-free, provider-free, consumes `Deps` interfaces). Phase 5 adds a second `TurnRunner` implementation — `workerRunner` — that satisfies the SAME interface the host already calls, but instead of executing in-process it spawns `cercano worker`, sends a StartTurn over a bidi stream, and bridges the worker's event / permission / persist messages back to the host callbacks it was given. The worker builds its own execution-only `Deps` (tools + provider from a config snapshot + a host-resolved credential) and runs the unchanged `runner.Core`. Basic crash isolation lands here; robust supervision (idle-reap, health monitors, restart policy, graceful mid-turn recovery) is Phase 6. See `docs/agent/agent-isolation/design.md`.

**Tech Stack:** Go 1.21+ (module `cercano/source/server`), gRPC/protobuf (one new bidi RPC), the Meridian manager's process-supervision machinery (Setpgid/pidfile/reaper), the Phase 3 `internal/runner`, the Phase 4 `internal/broker`.

## What a worker IS (the settled definition — read before deciding anything)

A worker is a child process that runs the execution of ONE conversation's turns — the tool loop, the tool execution (Bash, file edits), and the LLM streaming — in isolation. It holds NO durable state. The host owns config, the conversation database, the keychain, the broker/fan-out, and permission decisions. The worker gets what it needs for a turn from the host and streams results back. This follows directly from the founding decision ("an agent dying must not take down another agent") and the host-decomposition architecture (all services live on the host).

## Load-bearing design decisions (settled with the user — do not re-litigate)

1. **Worker = execution; host = state.** The worker owns only the execution `Deps`: `Providers` (built locally from the config snapshot + credential) and `Tools` (built locally — the tool registry is static). Everything stateful/UI proxies to the host over the stream: history, persist, permission decisions. The worker never opens the database and never reads the keychain.
2. **`workerRunner` implements the existing `TurnRunner` interface — the host turn handler is UNCHANGED.** `streamProcessRequestWithToolLoop` still calls `turnRunner.RunTurn(ctx, req, sink, requester, persist)` with the host's real callbacks (brokerSink → broker.Publish, permBroker requester, persistSvc persist). Only the `turnRunner` field's concrete type swaps (in-process `runner.Core` vs `workerRunner`) by config. The `workerRunner` bridges: worker Event → `sink.Emit`; worker PermissionRequest → `requester(...)` → response back; worker PersistTurn → `persist(m)`.
3. **The host pre-assembles history + project context and sends them in StartTurn** (it owns the store; it does this today at the start of `Core.RunTurn`). The worker's `Deps.Persist` is a stub that returns those preloaded values for `AssembleHistory`/`LoadProjectContext` and stream-proxies `PersistTurn`. This avoids a history round-trip and keeps `runner.Request` unchanged. (Full live-proxy of history is a possible Phase 6 refinement; not needed now.)
4. **The host resolves the API credential from the keychain and passes it to its own child worker in the config snapshot over the local unix socket.** Same machine, same user, a process the host spawned — an acceptable trust boundary (user-confirmed). The worker never touches the keychain.
5. **Basic crash isolation in Phase 5; hardening in Phase 6.** Phase 5: worker death (stream EOF/error) → host fences the turn (gen), surfaces a `codes.Unavailable` error to attached surfaces, host survives, other conversations unaffected; Setpgid + pidfile so a killed host doesn't orphan workers. Phase 6: idle-reap, health monitors, restart policy, mid-turn recovery/resume.

## Scope

- IN: the `cercano worker` subcommand; the bidi worker RPC + wire protocol; the worker-side turn execution (builds execution Deps, runs `runner.Core`, proxies state ops); the host-side `workerRunner` (spawn + drive + bridge + basic crash detection); the config toggle (in-process vs worker); the crash-isolation proof test.
- OUT (Phase 6): idle-reap, health/restart supervision, worker pooling, mid-turn resume, graceful drain. OUT (later): the CLI/VS Code seeing any of this (transparent — the client still talks to the host exactly as today).

## Global Constraints

- Module `cercano/source/server`. Build: `cd source/server && go build ./...`. Gate: `go test -race ./internal/server/... ./internal/runner/... ./internal/broker/... ./internal/worker/... -count=1 && go test ./... -count=1` all green.
- **Client-visible behavior is unchanged in BOTH modes.** The CLI talks to the host identically; a turn produces the same event stream whether it ran in-process or in a worker. The existing turn tests (`streaming_test.go`, `toolloop_persist_test.go`, `turn_exclusivity_test.go`, `attach_test.go`) must pass with the worker runner selected, not just in-process.
- `internal/runner` must NOT change — it is already transport-agnostic. If a change to `runner` seems necessary, STOP and report; the whole Phase 3 design was to avoid it.
- The proto change is ADDITIVE (a new bidi RPC + new messages). Regenerate with the pinned toolchain (protoc-gen-go v1.36.11, protoc-gen-go-grpc v1.6.2; command: from `source/proto/`, `protoc --go_out=../server --go_opt=module=cercano/source/server --go-grpc_out=../server --go-grpc_opt=module=cercano/source/server agent.proto`). Declare new messages at file end to minimize the generated-descriptor diff. Never hand-edit `.pb.go`.
- Reuse the Meridian manager's spawn/reap patterns (`internal/meridian/manager.go`: Setpgid via `realSpawn`, `killGroupOrProcess`, pidfile helpers) — do not reinvent process supervision.
- Commit messages: never the word "Claude".

---

## Target package layout

| Package/file | Responsibility |
|---|---|
| `source/proto/agent.proto` | New `rpc RunTurnWorker(stream WorkerToHost) returns (stream HostToWorker)` (bidi) + the envelope messages (StartTurn, WorkerEvent, PermissionRequest/Response, PersistTurn, TurnDone/TurnError). |
| `internal/worker/worker.go` | Worker-side: the bidi RPC server handler. Receives StartTurn → builds execution `Deps` (local providers+tools, stub+proxy Persist, proxy Perms) → runs `runner.Core.RunTurn` → streams WorkerEvent/PermissionRequest/PersistTurn back → TurnDone. |
| `internal/worker/proxies.go` | The stream-backed `Deps`/callback proxies: an `EventSink` that sends WorkerEvent; a `PermissionRequester` that round-trips; a `PersistFunc` that sends PersistTurn; a `Persist` stub preloaded with history/project-context. |
| `internal/worker/host.go` | Host-side `workerRunner` implementing `runner.TurnRunner`: spawn (Setpgid/pidfile), dial, send StartTurn, drain worker outbound → host callbacks (sink/requester/persist), detect death → error. |
| `internal/worker/spawn.go` | Worker process spawn/kill (thin, reusing Meridian patterns): find the `cercano` binary (agentclient pattern), spawn `cercano worker --socket <path>` with Setpgid, pidfile, wait-for-ready, kill-group on done. |
| `cmd/cercano/main.go` | New `case "worker": runWorkerMode(args)` — parse `--socket`, serve the worker RPC on the unix socket until the stream ends / parent dies. |

`internal/server/server.go`: select `turnRunner` = in-process `runner.Core` OR `workerRunner` by config; everything else unchanged.

---

## Task 1: The worker wire protocol (proto + serialization)

Define the bidi RPC + envelope. This is the contract every other task fills in.

**Files:** `source/proto/agent.proto` (+ regen stubs); no Go logic yet beyond what the stubs generate.

**Interfaces (produced — the wire contract):**
- `rpc RunTurnWorker(stream WorkerToHost) returns (stream HostToWorker)` — bidi. NOTE the direction: the WORKER dials the host? No — the HOST spawns the worker and the worker SERVES; so the host is the gRPC CLIENT and the worker is the gRPC SERVER. Model it as: worker serves `rpc RunTurn(stream HostToWorker) returns (stream WorkerToHost)` — host opens the stream, sends `HostToWorker` (StartTurn, PermissionResponse, Cancel), receives `WorkerToHost` (WorkerEvent, PermissionRequest, PersistTurn, TurnDone, TurnError).
- `message HostToWorker { oneof msg { StartTurn start = 1; PermissionResponse perm_response = 2; Cancel cancel = 3; } }`
- `message WorkerToHost { oneof msg { WorkerEvent event = 1; PermissionRequest perm_request = 2; PersistTurn persist = 3; TurnDone done = 4; TurnError error = 5; } }`
- `StartTurn { string conversation_id; string input; repeated InlineImage images; string work_dir; uint64 gen; ConfigSnapshot config; repeated LLMMessage history; string project_context; }` — reuse existing `InlineImage` if present; `ConfigSnapshot` carries the resolved credential (see decision 4); `LLMMessage` is the serialization of `llm.Message` (see below).
- `WorkerEvent { ... }` — mirrors `runner.Event` fields (kind enum + text/model/tool/watchdog/result fields). Reuse the existing `StreamProcessResponse` payload shapes where practical, or a dedicated flat message — pick whichever makes the host-side `WorkerEvent → runner.Event` mapping a clean inverse of `sendRunnerEvent`.
- `PermissionRequest { uint64 id; string tool_use_id; string name; string args_json; string tier; bool destructive; }` / `PermissionResponse { uint64 id; bool allow; string error; }` — `id` correlates request↔response.
- `PersistTurn { LLMMessage message; uint64 gen; }` / `TurnDone { <Result fields: final_text, model, is_cloud, input_tokens, output_tokens, notice> }` / `TurnError { string message; }`.
- **`LLMMessage` serialization:** `llm.Message` (grep `internal/llm` for its shape — role + content blocks) needs a proto form. If one already exists (grep proto for an LLM/message type used by persistence), reuse it; else add `LLMMessage` mirroring `llm.Message` (role, text/blocks, tool-use/tool-result). This is the one non-trivial serialization — get it faithful (round-trip `llm.Message → proto → llm.Message` must be lossless for the fields the tool loop + persistence use).

- [ ] **Step 1: Add the RPC + messages to `agent.proto`** (new messages at file end). Add `LLMMessage` (or reuse). Add `ConfigSnapshot` (mirror the `config.Config` fields the worker needs per decision 1 + the resolved credential field).
- [ ] **Step 2: Regenerate stubs** with the pinned toolchain (command in Global Constraints). Confirm a no-op regen first reproduces current stubs (zero churn), THEN regen with the additions.
- [ ] **Step 3: Write round-trip serialization tests** in `internal/worker/` (new pkg): `llm.Message ↔ LLMMessage` lossless for all block kinds (text, tool_use, tool_result, image); `runner.Event ↔ WorkerEvent` lossless for every `EventKind`; `config.Config → ConfigSnapshot` carries the needed fields. These lock the wire contract before any transport logic.
- [ ] **Step 4: Update any test doubles** that must satisfy the regenerated `AgentClient`/worker interfaces (grep for mock clients — mirror the Phase 4 `mockAgentClient` fix).
- [ ] **Step 5: Gate + commit.** `go build ./... && go vet ./... && go test ./internal/worker/ ./... -count=1` green. Commit: `feat(proto): worker turn-execution transport (bidi RunTurn + envelope)`.

---

## Task 2: The worker binary — run one turn, proxy state to the host

The worker-side RPC handler: receive StartTurn, build execution Deps, run `runner.Core`, stream results/permission-requests/persists back.

**Files:** `internal/worker/worker.go`, `internal/worker/proxies.go`, `cmd/cercano/main.go` (add `worker` subcommand), tests.

**Interfaces:**
- Consumes: the Task-1 proto; `runner.New(Deps) *Core`, `runner.Core.RunTurn`; `providers`/`tools` constructors (grep how the host builds `providerSvc`/`toolSvc` from config — the worker builds analogous LOCAL instances from the `ConfigSnapshot`; note: some host services pull in more than the worker needs — build only the execution slice).
- Produces: `func runWorkerMode(args []string)` (subcommand entry: parse `--socket`, serve); the worker RPC server type; the proxy types (`streamEventSink`, `streamPermissionRequester`, `streamPersistFunc`, `preloadedHistory`).

- [ ] **Step 1: The proxies** (`proxies.go`, TDD): (a) `streamEventSink` implements `runner.EventSink`; `Emit(ev)` marshals `runner.Event → WorkerEvent` and sends on the stream (non-blocking or bounded — a slow host must not wedge the worker turn; but the host drains promptly, so a modest buffered send-goroutine is fine). (b) `streamPermissionRequester` implements `runner.PermissionRequester`; sends `PermissionRequest{id}`, blocks on a per-id response channel until the host replies `PermissionResponse{id}` or ctx cancels. (c) `streamPersistFunc` implements `runner.PersistFunc`; sends `PersistTurn` (one-way). (d) `preloadedHistory` implements the runner's `TurnHistory` (`Deps.Persist`): `AssembleHistory` returns the StartTurn-provided history; `LoadProjectContext` returns the provided project context; `PersistTurn` delegates to `streamPersistFunc`. Unit-test each proxy against a fake stream.
- [ ] **Step 2: The worker RPC handler** (`worker.go`, TDD): on stream open, first recv must be `StartTurn`. Build `runner.Deps{ Providers: <local from ConfigSnapshot+cred>, Tools: <local registry>, Persist: preloadedHistory, Config: <local cfg from snapshot>, Perms: <a broker built around the streamPermissionRequester's Store? — grep what Deps.Perms.Store()/Mode() the runner actually calls; the worker needs a permissions.Broker whose Wait/Store route through the stream>, Agent: nil, Watchdog: nil }`. Run `runner.New(deps).RunTurn(ctx, req, streamEventSink, streamPermissionRequester, streamPersistFunc)`. On return, send `TurnDone{result}` (or `TurnError`). Route incoming `HostToWorker` msgs: `PermissionResponse` → resolve the pending request; `Cancel` → cancel the turn ctx. Test with an in-process (bufconn) stream + a fake host that scripts permission responses and asserts the worker emits the expected events + persists + done.
- [ ] **Step 3: The subcommand** (`main.go`): `case "worker": runWorkerMode(os.Args[2:])`. `runWorkerMode`: parse `--socket <unix path>`, create a gRPC server on that unix listener, register the worker service, serve until the stream ends or the parent dies (detect parent death: the host uses Setpgid + closes the socket / cancels; the worker should exit when its stream/connection drops and no new one arrives within a short grace — keep simple for Phase 5: exit after the single turn's stream closes). Do NOT load the full agent server — only what a turn needs.
- [ ] **Step 4: Gate + commit.** `go build ./... && go vet ./... && go test -race ./internal/worker/ -count=1 && go test ./... -count=1` green. `cercano worker --socket /tmp/x.sock` starts + serves (smoke-check in a test via bufconn, not a real process yet). Commit: `feat(worker): worker-side turn execution over the transport`.

---

## Task 3: The host-side `workerRunner` — spawn, drive, bridge, detect crash

The host implementation of `runner.TurnRunner` that runs a turn in a worker instead of in-process.

**Files:** `internal/worker/host.go`, `internal/worker/spawn.go`, tests.

**Interfaces:**
- Consumes: Task-1 proto (host is the gRPC CLIENT), Task-2 worker binary, the Meridian spawn patterns, the host's `persistSvc` (to pre-assemble history + project context) + `cfgSvc` + credential resolution (grep how the host resolves the keychain credential today — the provider/secrets path).
- Produces: `func NewWorkerRunner(<host services needed to build StartTurn + answer proxies>) *workerRunner`; `(*workerRunner) RunTurn(ctx, runner.Request, runner.EventSink, runner.PermissionRequester, runner.PersistFunc) (runner.Result, error)` — satisfies `runner.TurnRunner`.

- [ ] **Step 1: `spawn.go`** (reuse Meridian patterns): find the `cercano` binary (agentclient `findCercanoBinary` pattern), pick a unique unix socket path (per turn/conversation, e.g. under `$TMPDIR/cercano-workers/<conv>-<gen>.sock`), spawn `cercano worker --socket <path>` with `SysProcAttr{Setpgid: true}`, write a pidfile (Meridian `writePidFile` pattern), wait for the socket to accept (dial poll, agentclient `waitForPort` analog for unix sockets), return a handle {cmd, conn, socketPath}. `Kill()`: `killGroupOrProcess` + remove pidfile + socket. Test the spawn/kill lifecycle with the REAL built worker binary (an integration-style test that builds `cercano` or invokes the worker subcommand).
- [ ] **Step 2: `workerRunner.RunTurn`** (TDD): (a) pre-assemble: `history := persistSvc.AssembleHistory(ctx, req.ConversationID)`, `projectCtx := persistSvc.LoadProjectContext(req.WorkDir)`, build `ConfigSnapshot` from `cfgSvc.Get()` + resolve the credential; (b) spawn the worker (Step 1) — or reuse a per-conversation worker if one exists (Phase 5: spawn-per-turn is acceptable; note pooling is Phase 6); (c) open the bidi stream, send `StartTurn{req fields, config, history, projectCtx}`; (d) DRAIN worker→host in a loop: `WorkerEvent` → map to `runner.Event` → `sink.Emit`; `PermissionRequest` → call the host `requester(...)` (in a goroutine so a slow human doesn't block the event drain) → send `PermissionResponse`; `PersistTurn` → `persist(m)`; `TurnDone` → capture result, return; `TurnError` → return the error; (e) on `stream.Recv()` EOF/error WITHOUT a TurnDone → the worker CRASHED → kill the worker, return `codes.Unavailable` "worker crashed"; (f) on ctx cancel (supersession) → send `Cancel`, close, kill the worker, return ctx.Err(); (g) always `defer worker.Kill()`.
- [ ] **Step 3: crash-isolation behavior** — confirm: the returned error flows through the host's existing turn handler exactly like the in-process panic path (which already yields `codes.Internal`/a FinalResponse error); the broker turn is released/fenced by the host handler's existing `defer release()`/gen; attached surfaces see the turn end. The host process does NOT exit when a worker dies.
- [ ] **Step 4: Gate + commit.** `go build ./... && go vet ./... && go test -race ./internal/worker/ -count=1 && go test ./... -count=1` green. Commit: `feat(worker): host-side workerRunner — spawn, drive, bridge, crash-detect`.

---

## Task 4: The toggle + both-modes tests + the crash-isolation proof

Wire the config selector and prove the whole thing: identical behavior in-process vs worker, and a worker crash is isolated.

**Files:** `internal/server/server.go` (select `turnRunner` by config), a config field, tests (`internal/server/` + `internal/worker/`).

**Interfaces:**
- Consumes: `NewWorkerRunner` (Task 3), the in-process `runner.New` (today's default), a config field (grep `config.Config` for where to add e.g. `ExecutionMode string // "in_process" | "worker"`, default per design — workers default, but DEFAULT TO in_process for now if the worker path is new/risky and flip the default in a follow-up step once the proof passes; user decision reserved — see note).
- Produces: the selected `turnRunner` in `NewServer`.

- [ ] **Step 1: The selector** — in `NewServer`, choose `s.turnRunner = runner.New(s.runnerDeps())` (in-process) or `worker.NewWorkerRunner(...)` based on the config field. Everything downstream (the turn handler) is unchanged (decision 2). Add the config field + plumb it.
- [ ] **Step 2: Both-modes regression** — parameterize (or duplicate) the load-bearing turn tests to run with the worker runner selected: a turn streams the same events, persists the same turns, supersession still fences, an attacher still sees replay+live. These prove client-visible behavior is identical in worker mode. (Use the real worker subcommand via the Task-3 spawn, or an in-process bufconn worker harness — prefer the bufconn harness for speed + determinism, plus at least ONE end-to-end test that spawns a real `cercano worker` process.)
- [ ] **Step 3: The crash-isolation proof** — `TestWorker_CrashMidTurnIsIsolated`: start a turn in worker mode; kill the worker process mid-turn (or script the worker to exit abnormally); assert (a) the initiator gets a `codes.Unavailable`/error-final, not a hang; (b) the host process is still alive and serving (a second conversation's turn still works); (c) the broker turn was released/fenced (no leaked active turn). This is the phase's raison d'être — make it real.
- [ ] **Step 4: Default decision** — leave the default as `in_process` OR flip to `worker` per the design (workers default). RESERVE the flip for a user decision at review time (flipping the default changes every user's runtime; call it out explicitly rather than defaulting silently).
- [ ] **Step 5: Gate + commit.** Full `-race` + suite green in both modes. Commit: `feat(server): select in-process vs worker turn execution by config + crash-isolation proof`.

---

## Self-review

- **Spec coverage:** design Phase 5 = "worker process + transport, crash isolation." Task 1 = wire contract; Task 2 = worker runs the turn + proxies state; Task 3 = host spawns/drives/bridges/detects-crash; Task 4 = toggle + both-modes-identical + crash-isolation proof. Hardening (idle-reap/health/restart/resume) explicitly deferred to Phase 6 (matches the design's 5/6 split).
- **Type consistency:** `workerRunner` implements `runner.TurnRunner` (same `RunTurn` signature as `runner.Core`) — the host handler is unchanged (decision 2). `StartTurn`/`WorkerEvent`/`PermissionRequest`+`Response`/`PersistTurn`/`TurnDone`/`TurnError` used identically across Tasks 1–4. `runner` package is untouched.
- **Known unknowns (resolve at implementation, flagged not hidden):** (a) `llm.Message` proto serialization faithfulness (Task 1 Step 3 — the one real serialization; grep for an existing proto message form first); (b) exactly what `Deps.Perms` methods the runner calls, so the worker's stream-backed permissions.Broker implements only those (Task 2 Step 2 — grep the runner's `Perms.` uses); (c) how the host resolves the keychain credential to put in ConfigSnapshot (Task 3 Step 2 — grep the secrets/provider path); (d) spawn-per-turn vs reuse-per-conversation (Phase 5 uses spawn-per-turn for simplicity; per-conversation reuse + pooling is Phase 6); (e) the default ExecutionMode flip (Task 4 Step 4 — reserved for the user).
- **Deferred to Phase 6:** idle-reap, health monitors, restart policy, mid-turn resume, worker pooling/reuse, graceful drain-on-shutdown of workers, orphan-reap-on-host-startup (basic pidfile is written in Phase 5; the startup sweep is Phase 6).
