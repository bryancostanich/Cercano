# Agent Execution Isolation — Phase 4 Implementation Plan (Conversation Broker + Multi-Surface Attach)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Grow the per-conversation turn-exclusivity registry into a real `internal/broker` that fans one turn's live events out to *many* attached surfaces (not just the client that started the turn), with a running per-turn replay buffer so a surface that joins mid-turn sees the in-flight turn animate from where it currently sits — proven server-side by tests, with no client-app changes this phase.

**Architecture:** Today a turn's events (token deltas, tool calls, watchdog) reach ONLY the caller of `StreamProcessRequest`; the global event hub carries only admin/config events. Phase 4 introduces a per-conversation broker that owns turn-exclusivity + a subscriber fan-out + a current-turn event buffer. The runner publishes events to the broker; every surface — the turn initiator AND any late attacher — is a broker subscriber. A new `AttachConversation` RPC lets a surface subscribe to a conversation it did not start (replay of the in-flight turn, then live). See `docs/agent/agent-isolation/design.md`.

**Tech Stack:** Go 1.21+ (module `cercano/source/server`), gRPC/protobuf (one new RPC), the Phase-3 `internal/runner`. No new external dependencies.

## Scope (decided with the user)

- **Server capability + proof only.** Build the broker, the fan-out, the replay buffer, and the `AttachConversation` RPC; prove multi-surface with server-side tests (two subscribers on one conversation both get the full stream; a mid-turn attacher gets replay + live). **Do NOT change the CLI or VS Code apps** — wiring a real second window is a follow-up phase.
- **Full replay of the live turn.** A surface joining mid-turn receives the current turn's already-emitted events (from the broker's per-turn buffer), then live continuation — no gap, no duplicate. History from *before* the current turn is a client concern (existing persistence RPCs), NOT part of the attach stream.

## Load-bearing design decisions (review before executing)

1. **The broker is proto-free; it fans out `runner.Event`, and each subscriber maps to proto at its own RPC handler.** The runner→proto mapping (today inline in `hostProtoSink`) becomes a shared adapter used by BOTH the `StreamProcessRequest` handler and the `AttachConversation` handler. Rationale: keeps the broker unit-testable without proto, matches the runner's proto-free stance, and is what Phase 5's worker wants (worker publishes events to the host broker; host fans out to all surfaces). If you'd rather the broker fan out `proto.StreamProcessResponse` (simpler RPC handlers, but couples the broker to proto and duplicates nothing), say so before Task 2.

2. **The turn initiator becomes just the first subscriber (unified model).** Instead of the runner emitting directly to the caller's stream, the runner publishes to the broker and the `StreamProcessRequest` handler *attaches* to the broker and drains events to its own stream — exactly like an attacher does. This unifies the drive and attach paths and is the seam Phase 5 needs. It DOES restructure `StreamProcessRequest`'s handler (RunTurn and the drain loop run concurrently; the handler returns when the turn completes). The lower-risk alternative — keep the initiator's direct path and ALSO publish to the broker for attachers (two paths, initiator stays special) — is available if you'd prefer to minimize churn on the hot path. Recommendation: unified. Flag before Task 3 if you want the dual-path.

3. **The replay buffer holds ONLY the current turn's events and resets on `BeginTurn`.** A new turn (including a supersession) clears the buffer — a mid-turn joiner replays the turn currently running, never older turns (those are in persistence). This bounds the buffer to one turn's events.

## What this phase does NOT do

- No CLI / VS Code changes (server capability + proof only).
- No worker process / IPC (Phase 5) — but the broker is designed as the seam the worker publishes through.
- No change to the global admin event hub (`eventHub`/`SubscribeEvents`) — it keeps carrying config/permission/status events, untouched.
- No change to `internal/runner` behavior — the runner still emits the same `runner.Event`s; only where they're delivered changes.

## Global Constraints

- Module `cercano/source/server`. Build: `cd source/server && go build ./...`. Gate: `go test -race ./internal/server/... ./internal/runner/... ./internal/broker/... ./internal/hostsvc/... -count=1 && go test ./... -count=1` all green.
- **Single-surface behavior is unchanged.** The existing `StreamProcessRequest` client (the CLI) must see byte-identical event streams before and after. The existing turn tests (`streaming_test.go`, `toolloop_persist_test.go`, `turn_exclusivity_test.go`) are the regression net.
- `internal/broker` must NOT import `internal/server` (leaf the host consumes). Keep it proto-free (decision 1) — `go list -deps ./internal/broker | grep -E 'internal/server|pkg/proto'` returns nothing.
- Turn-exclusivity semantics are preserved exactly: one live turn per conversation; a new turn supersedes (cancels) the prior; superseded turns' persistence + emission are fenced by generation.
- The proto change is ADDITIVE: a new `AttachConversation` RPC + request message, reusing the existing `StreamProcessResponse`. Do not alter existing RPCs/messages. Regenerate stubs with the repo's existing codegen path (find it — likely a `buf`/`protoc` script under `source/proto` or a Makefile target; do NOT hand-edit generated `.pb.go`).
- Commit messages: never the word "Claude".

---

## Target package layout

New package `internal/broker`:

| File | Responsibility |
|---|---|
| `broker/broker.go` | `Broker` — per-conversation state: generation/turn-exclusivity, subscriber set, current-turn replay buffer. Methods: `BeginTurn`, `IsCurrent`, `Publish`, `Attach`, `HasActiveTurn`. Proto-free. |
| `broker/broker_test.go` | Unit tests: supersession, fence, fan-out to N subscribers, mid-turn attach replay+live (no gap/dup), buffer reset on new turn. |

`internal/server` changes:
- `Server` loses `turnsMu`/`activeTurns`/`turnGens`/`turnHandle`; gains `broker *broker.Broker`. `beginTurn`/`turnIsCurrent`/`hasActiveTurn` become thin delegators (or are replaced at call sites).
- `hostProtoSink` → publishes `runner.Event` to the broker instead of mapping+sending to one stream.
- A shared `func sendRunnerEvent(stream, ev runner.Event) error` (the old hostProtoSink mapping switch, extracted) used by both the `StreamProcessRequest` drain loop and the `AttachConversation` handler.
- New `AttachConversation` RPC handler.

`source/proto/agent.proto`: `rpc AttachConversation(AttachConversationRequest) returns (stream StreamProcessResponse)` + `message AttachConversationRequest { string conversation_id = 1; }`.

---

## Task 1: Extract turn-exclusivity into `internal/broker` (pure refactor)

Move the registry off `Server` into `broker.Broker` with delegators. NO new capability yet — behavior-preserving, existing turn tests stay green. This isolates the risky mechanical move from the new fan-out logic.

**Files:**
- Create: `internal/broker/broker.go`, `internal/broker/broker_test.go`
- Modify: `internal/server/server.go` (remove registry fields/methods, add `broker` field + delegators)

**Interfaces:**
- Produces: `broker.New() *Broker`; `(*Broker).BeginTurn(parent context.Context, conv string) (ctx context.Context, gen uint64, release func())`; `(*Broker).IsCurrent(conv string, gen uint64) bool`; `(*Broker).HasActiveTurn(conv string) bool`.

- [ ] **Step 1: Write `broker.Broker` with the moved registry**

Create `internal/broker/broker.go`: a `Broker` struct holding `mu sync.Mutex`, `active map[string]*turnHandle`, `gens map[string]uint64` (+ `turnHandle{gen, cancel}`), and the moved bodies of `beginTurn`/`turnGenLocked`/`turnIsCurrent`/`hasActiveTurn` (receiver `(b *Broker)`, `s.`→`b.`, verbatim logic). Exported: `New`, `BeginTurn`, `IsCurrent`, `HasActiveTurn`.

- [ ] **Step 2: Port the existing turn-exclusivity tests to the broker**

Move/adapt the exclusivity assertions from `internal/server/turn_exclusivity_test.go` into `broker_test.go` at the broker level (supersession cancels the prior ctx; `IsCurrent` false after supersession; release only clears if still current). Keep the server-level test too if it exercises the delegators.

- [ ] **Step 3: Repoint the front door**

In `server.go`: delete `turnsMu`/`activeTurns`/`turnGens`/`turnHandle`; add `broker *broker.Broker` (construct in `NewServer`). Replace `s.beginTurn(...)`→`s.broker.BeginTurn(...)`, `s.turnIsCurrent(...)`→`s.broker.IsCurrent(...)`, `s.hasActiveTurn(...)`→`s.broker.HasActiveTurn(...)` at all call sites (grep). Keep thin `s.beginTurn` etc. shims ONLY if tests call them directly.

- [ ] **Step 4: Gate**

Run: `cd source/server && go build ./... && go vet ./... && go test -race ./internal/server/... ./internal/broker/... -count=1 && go test ./... -count=1`
Expected: PASS. Confirm `go list -deps ./internal/broker | grep -E 'internal/server|pkg/proto' || echo CLEAN` prints CLEAN. Registry fields gone from `Server`.

- [ ] **Step 5: Commit**

```bash
git add internal/broker/ internal/server/
git commit -m "refactor(server): move turn-exclusivity registry into internal/broker"
```

---

## Task 2: Add fan-out + per-turn replay buffer + Attach to the broker (new capability)

Give the broker a per-conversation subscriber set and a current-turn event buffer, and the `Publish`/`Attach` methods. Proto-free, unit-tested. No front-door wiring yet.

**Files:**
- Modify: `internal/broker/broker.go`, `internal/broker/broker_test.go`

**Interfaces:**
- Consumes: `runner.Event` (import `internal/runner` for the event type — confirm this doesn't create a cycle; runner must not import broker. If it would, define a broker-local `Event` type = a copy, or move `runner.Event` to a shared leaf. Prefer importing `runner.Event` if acyclic.)
- Produces:
  - `(*Broker).Publish(conv string, gen uint64, ev runner.Event)` — fenced by `IsCurrent(conv, gen)`; appends to the conversation's current-turn buffer AND fans out to all subscribers, under a consistent lock order so a concurrent `Attach` sees no gap/dup.
  - `(*Broker).Attach(conv string) (replay []runner.Event, ch <-chan runner.Event, detach func())` — atomically snapshots the current-turn buffer and registers a new subscriber channel; events published after Attach arrive on `ch`, everything before is in `replay`, with no overlap or hole.
  - `BeginTurn` now also **resets** the conversation's replay buffer (new turn ⇒ fresh buffer) and leaves existing subscribers attached (they keep receiving across turns — a supersession is visible to them as the old turn's events stopping and the new turn's starting).

- [ ] **Step 1: Write the fan-out + replay unit tests FIRST**

In `broker_test.go`:
- `TestBroker_FanOutToMultipleSubscribers`: BeginTurn, two `Attach`, `Publish` 3 events (with the current gen), assert BOTH subscribers' channels receive all 3 in order.
- `TestBroker_AttachMidTurnReplaysThenLive`: BeginTurn, Publish 2 events, THEN Attach → assert `replay` has exactly those 2; Publish a 3rd → assert it arrives on the attacher's `ch` (not in replay); assert no event is both in replay and on ch (no dup) and none missing (no gap).
- `TestBroker_BeginTurnResetsBuffer`: Publish under gen N, BeginTurn (→ gen N+1), Attach → replay is empty (previous turn's events gone).
- `TestBroker_PublishFencedByGen`: Publish with a stale gen after supersession is a no-op (nothing delivered/buffered).
- `TestBroker_DetachStopsDelivery`: after `detach()`, further Publish does not deliver to that channel (and doesn't block the publisher).
- Run all under `-race`; the Attach-vs-Publish atomicity is the point.

- [ ] **Step 2: Implement `Publish`/`Attach`/buffer**

Per-conversation state grows to include `buffer []runner.Event` and `subs map[int]chan runner.Event`. `Publish`: under `mu`, if `gens[conv]!=gen` return (fenced); append to `buffer`; for each sub do a non-blocking send (drop-on-full like the admin hub, or a generous buffer — match the admin hub's non-blocking-drop policy so one wedged surface can't stall the turn). `Attach`: under `mu`, copy `buffer` into `replay`, create a buffered channel, register in `subs`, return `(replay, ch, detach)`. `detach`: under `mu`, remove + close the channel. `BeginTurn`: under `mu`, reset `buffer` for that conv (keep `subs`).

- [ ] **Step 3: Gate**

Run: `go test -race ./internal/broker/ -count=1`
Expected: PASS. Then full `go build ./... && go test -race ./internal/server/... ./internal/broker/... -count=1`. CLEAN check still holds.

- [ ] **Step 4: Commit**

```bash
git add internal/broker/
git commit -m "feat(broker): per-conversation fan-out with mid-turn replay buffer"
```

---

## Task 3: Wire the runner→broker→initiator path (unified model, behavior-preserving)

Make the runner publish to the broker and the `StreamProcessRequest` handler attach as the first subscriber. Single-surface behavior must be byte-identical.

**Files:**
- Modify: `internal/server/server.go` (`streamProcessRequestWithToolLoop`, `hostProtoSink`), and extract the runner→proto mapping into a shared `sendRunnerEvent`.

**Interfaces:**
- Consumes: `broker.Publish`, `broker.Attach`, the existing `runner.EventSink`.
- Produces: `func sendRunnerEvent(stream proto.Agent_StreamProcessRequestServer, ev runner.Event) error` (the extracted mapping switch, reused by Task 4). A `brokerSink` implementing `runner.EventSink` whose `Emit` calls `broker.Publish(conv, gen, ev)`.

- [ ] **Step 1: Extract the mapping switch**

Pull the `runner.Event`→`proto.StreamProcessResponse` switch out of `hostProtoSink.Emit` into a free function `sendRunnerEvent(stream, ev)` (same per-kind proto sends, INCLUDING dropping `EventWatchdog{escalate}` to preserve behavior; FinalResponse still sent by the handler, not here). Confirm behavior-identical by keeping the existing streaming tests green.

- [ ] **Step 2: Restructure `streamProcessRequestWithToolLoop` to the unified model**

New shape:
1. `ctx, gen, release := s.broker.BeginTurn(stream.Context(), convID)`; `defer release()`.
2. `replay, ch, detach := s.broker.Attach(convID)`; `defer detach()`. (Initiator is the first subscriber. `replay` is empty here — the turn hasn't published yet.)
3. Build `sink := &brokerSink{broker: s.broker, conv: convID, gen: gen}` (publishes to broker).
4. Run the turn and drain concurrently: start `go func(){ res, err := s.turnRunner.RunTurn(ctx, runReq, sink, requester, persist); ... signal done with res/err }()`; on the main goroutine, drain `ch` (and first the `replay`) calling `sendRunnerEvent(stream, ev)` until the turn-done signal fires AND the channel is drained. Then send FinalResponse from the captured result (or the Model=="" hard-fail bare response), do post-turn bookkeeping, return. Preserve the persist fence + user-turn-up-front exactly.
5. The requester (permission) path is unchanged (still request/response on the initiator's stream) — attachers are observers of permission-required events but the DECISION comes from the initiator; document that only the initiator answers permission prompts (a second surface seeing a permission-required event it can't answer is acceptable for this phase; note it).

- [ ] **Step 3: Gate — single-surface behavior preserved**

Run: `go build ./... && go vet ./... && go test -race ./internal/server/... ./internal/broker/... -count=1 && go test ./... -count=1`
Expected: PASS. `streaming_test.go`/`toolloop_persist_test.go`/`turn_exclusivity_test.go` prove the initiator still sees the same stream, persistence still fenced. If ordering of events changed (e.g. a race between replay drain and live), fix the drain to emit `replay` fully before `ch`.

- [ ] **Step 4: Commit**

```bash
git add internal/server/
git commit -m "refactor(server): route turn events through the broker (initiator is first subscriber)"
```

---

## Task 4: `AttachConversation` RPC + the multi-surface proof

Add the RPC that lets a second surface attach to a conversation it didn't start, and the load-bearing test that proves two surfaces see one turn (with mid-turn replay).

**Files:**
- Modify: `source/proto/agent.proto` (new RPC + request message), regenerate stubs.
- Modify: `internal/server/` (new `AttachConversation` handler).
- Create/extend: `internal/server/attach_test.go` (the multi-surface proof).

**Interfaces:**
- Consumes: `broker.Attach`, `sendRunnerEvent` (Task 3).
- Produces: `func (s *Server) AttachConversation(req *proto.AttachConversationRequest, stream proto.Agent_AttachConversationServer) error`.

- [ ] **Step 1: Add the proto + regenerate**

In `source/proto/agent.proto`: add `rpc AttachConversation(AttachConversationRequest) returns (stream StreamProcessResponse) {}` and `message AttachConversationRequest { string conversation_id = 1; }`. Regenerate with the repo's codegen (find the script/target; run it; do not hand-edit `.pb.go`). Confirm `go build ./...` sees the new stubs.

- [ ] **Step 2: Implement the handler**

`AttachConversation`: `replay, ch, detach := s.broker.Attach(req.GetConversationId()); defer detach()`. Send each event in `replay` via `sendRunnerEvent(stream, ev)`, then loop on `ch`/`ctx.Done()` sending live events until the client disconnects or the stream ends. (Attach is observe-scoped for this phase — no input, no permission answering.) An attach to a conversation with no active turn just blocks on `ch` until a turn starts (or the client leaves) — that's fine.

- [ ] **Step 3: The multi-surface proof test**

`internal/server/attach_test.go` — `TestAttachConversation_TwoSurfacesSeeOneTurn`: start a `StreamProcessRequest` turn on convID with a scripted runner/provider that emits a known sequence (route, N token deltas, a tool-use, final); concurrently (after the first couple of events) call `AttachConversation(convID)` and collect its stream. Assert: (a) the initiator's stream got the full sequence (unchanged); (b) the attacher got a REPLAY of the events already emitted before it attached, THEN the remaining live events — the union equals the full sequence, in order, no gap/dup; (c) both saw the FinalResponse/turn end. Use the server's test harness (bufconn or the existing in-process test server) with fakes. Run under `-race`.

- [ ] **Step 4: Gate**

Run: `go build ./... && go vet ./... && go test -race ./internal/server/... ./internal/broker/... -count=1 && go test ./... -count=1`
Expected: PASS, including the new multi-surface proof.

- [ ] **Step 5: Commit**

```bash
git add source/proto/ internal/server/
git commit -m "feat(server): AttachConversation RPC — a second surface sees a live turn (replay + live)"
```

---

## Self-review

- **Spec coverage:** design doc Phase 4 = "broker grows from the turn registry + multi-surface attach." Task 1 grows the broker (registry move); Task 2 adds fan-out + replay (the user's 'full replay of the live turn'); Task 3 routes the initiator through it; Task 4 adds the attach RPC + the two-surfaces proof. Scope 'server capability + proof, no client changes' honored — no CLI/VS Code edits in any task.
- **Type consistency:** `broker.New() *Broker`; `BeginTurn(ctx, conv) (ctx, gen, release)`; `IsCurrent(conv, gen) bool`; `Publish(conv, gen, runner.Event)`; `Attach(conv) (replay []runner.Event, ch <-chan runner.Event, detach func())` — used identically across Tasks 2–4. `sendRunnerEvent(stream, runner.Event) error` defined in Task 3, reused in Task 4.
- **Known unknowns (resolve at implementation, flagged not hidden):** (a) whether `internal/broker` can import `runner.Event` without a cycle — if not, the event type moves to a shared leaf or the broker defines its own (Task 2 Step 1 decides); (b) the exact proto codegen invocation (Task 4 Step 1 — find the repo's script, don't hand-edit); (c) the non-blocking-vs-buffered send policy for subscribers (Task 2 — match the admin hub's drop-on-full so a wedged surface can't stall a turn); (d) the drain/RunTurn concurrency in Task 3 — the one real restructure; the streaming tests gate it.
- **Deferred to later phases:** real CLI/VS Code attach UX (a follow-up); attachers answering permission prompts / co-driving input arbitration beyond existing supersession (noted in Task 3/4); the worker publishing to this broker across a process boundary (Phase 5 — the broker is the seam).
