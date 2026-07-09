# Agent Execution Isolation — Phase 3 Implementation Plan (Extract `TurnRunner`)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pull one conversation turn's execution out of the `Server` front door into a self-contained `internal/runner` package behind a `TurnRunner` interface that owns all per-turn execution state and holds **zero process-global mutable state** — so the same core runs in-process today and in a worker process later (Phase 5), and the isolation is guaranteed by construction + a guard test.

**Architecture:** The front door (host) keeps proto translation, the per-conversation turn-exclusivity registry (`beginTurn`/`turnGens`), and the client stream. It builds an `EventSink` + `PermissionRequester` + persist callback for a turn, then calls `runner.RunTurn`. The runner resolves its own provider/history from injected Phase-2 services (so it works across a future process boundary), runs the tool loop, emits typed events, and returns a result. See `docs/agent/agent-isolation/design.md`.

**Tech Stack:** Go 1.21+ (module `cercano/source/server`), the Phase-2 `internal/hostsvc/*` services, `agent.RunToolLoop`. No new dependencies.

## The one load-bearing design decision (review before executing)

**The runner resolves its own provider, assembles its own history, and owns cross-tier fallback — it is NOT handed a live provider by the host.** Rationale: Phase 5 runs the runner in a separate process; a live `llm.Provider` (an open network client) cannot cross a process boundary, so a worker must construct its own from a config snapshot + the provider service. Building that in now means the in-process and worker wrappers share one code path. The runner therefore takes the Phase-2 service *interfaces* (`providers.Resolver`, `persistence.Service`, `config.Service`, `tools.Catalog`, `permissions.Broker`, `*watchdog.Watchdog`, `*agent.Agent`) as constructor dependencies; the guard test injects fakes. Consequence: `TurnRequest` carries only user-facing inputs (conversation id, input, images, workDir, generation) — not a provider. This matches the approved design doc (`TurnRequest` carries a config snapshot, not a provider). If you'd rather the host resolve and pass the provider (simpler now, but forces a second code path for workers), say so before Task 2.

## What STAYS on the host (front door) — not this phase

- `StreamProcessRequest` (proto unmarshal, client stream, error mapping).
- The per-conversation turn-exclusivity registry: `turnsMu`, `activeTurns`, `turnGens`, `turnHandle`, `beginTurn`, `turnGenLocked`, `turnIsCurrent`, `hasActiveTurn`. This is per-*conversation* coordinator state (keyed by conversation id, monotonic across turns) — it belongs to the host/broker, which Phase 4 grows. The runner receives the turn's generation number + a "still current?" predicate to fence persistence; it does not own the registry.
- Recap/compaction scheduling and context-usage recording on `s.agent` after a turn (host post-turn bookkeeping).

## Global Constraints

- Module `cercano/source/server`. Build: `cd source/server && go build ./...`. Test: `go test ./... -count=1`; the load-bearing gate is `go test -race ./internal/server/... ./internal/runner/... ./internal/hostsvc/... -count=1`.
- **This phase changes NO client-visible behavior.** Every task's gate is the existing suite green under `-race`. The existing turn/dispatch tests (`toolloop_persist_test.go`, `turn_exclusivity_test.go`, `agentic_*_test.go`, `streaming_test.go`) are the regression net.
- **Load-bearing invariant:** `internal/runner` holds zero process-global mutable state — no package-level vars mutated per turn, no `os.Chdir`, no shared singletons written during `RunTurn`. Guarded by the Task 3 concurrent-runners test.
- `internal/runner` must NOT import `internal/server` (no cycle; the runner is a leaf the host consumes).
- The runner must be **proto-free**: it emits a runner-level typed `Event`, never a `proto.StreamProcessResponse`. The host adapts events → proto. (This is what lets a worker serialize events its own way and lets embedded mode skip proto entirely.)
- Preserve Phase 1 (WorkDir threading, no `os.Chdir`) and the session-scoping fix (`WithSessionID`/`WithIndependentSession`) exactly.
- Commit messages: never the word "Claude" anywhere.

---

## Target package layout

New package `internal/runner`:

| File | Responsibility |
|---|---|
| `runner/runner.go` | `TurnRunner` interface; `Request`, `Result` types; `Deps` (injected services) |
| `runner/event.go` | The typed `Event` vocabulary + `EventSink` interface + `PermissionRequester`, `PersistFunc` callback types |
| `runner/core.go` | `Core` — the in-process `TurnRunner`: resolves provider, assembles history, builds the tool-loop input, runs `agent.RunToolLoop`, cross-tier fallback |
| `runner/core_test.go` | Core behavior + the concurrent cross-talk guard test |

`internal/server` changes: `streamProcessRequestWithToolLoop` shrinks to host glue (beginTurn → build sink/requester/persist adapters → `core.RunTurn` → post-turn bookkeeping → release). `runMainLoop` and its per-turn closure-building move into `runner.Core`. `buildSystemPrompt` moves into the runner (it renders the per-turn system message from workDir + project context).

---

## Task 1: Define the `runner` contract (types + interface + event vocabulary)

Pure contract, no execution logic. Establishes the boundary every later task fills in.

**Files:**
- Create: `internal/runner/runner.go`, `internal/runner/event.go`, `internal/runner/runner_test.go`

**Interfaces:**
- Produces: `runner.TurnRunner`, `runner.Request`, `runner.Result`, `runner.Deps`, `runner.Event`, `runner.EventKind`, `runner.EventSink`, `runner.PermissionRequester`, `runner.PersistFunc`.

- [ ] **Step 1: Write the failing test for the event sink + request types**

`internal/runner/runner_test.go`:
```go
package runner

import (
	"testing"

	"cercano/source/server/internal/llm"
)

// captureSink records emitted events — the in-process EventSink for tests.
type captureSink struct{ events []Event }

func (c *captureSink) Emit(ev Event) { c.events = append(c.events, ev) }

func TestEvent_CarriesTokenAndToolPayloads(t *testing.T) {
	s := &captureSink{}
	s.Emit(Event{Kind: EventToken, Text: "hi"})
	s.Emit(Event{Kind: EventToolUseStart, ToolUseID: "t1", ToolName: "Read"})
	s.Emit(Event{Kind: EventDone, Result: Result{FinalText: "done", InputTokens: 3, OutputTokens: 1}})
	if len(s.events) != 3 {
		t.Fatalf("got %d events, want 3", len(s.events))
	}
	if s.events[0].Kind != EventToken || s.events[0].Text != "hi" {
		t.Errorf("token event wrong: %+v", s.events[0])
	}
	if s.events[2].Result.FinalText != "done" {
		t.Errorf("done event lost result: %+v", s.events[2])
	}
}

func TestRequest_IsProviderFree(t *testing.T) {
	// A Request carries user-facing inputs only — no llm.Provider. The runner
	// resolves the provider itself (worker-compatibility). This compiles-or-not
	// test locks that: Request has no provider field.
	r := Request{ConversationID: "c1", Input: "x", WorkDir: "/repo", Gen: 1}
	_ = r
	var _ []llm.Message // history is assembled by the runner, not passed in
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd source/server && go test ./internal/runner/ -run 'TestEvent_CarriesTokenAndToolPayloads|TestRequest_IsProviderFree'`
Expected: FAIL — `undefined: Event` / package has no files.

- [ ] **Step 3: Write the event vocabulary**

`internal/runner/event.go`:
```go
package runner

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/llm"
)

// EventKind enumerates the runner's proto-free event vocabulary. The host maps
// these to proto.StreamProcessResponse payloads; embedded mode consumes them
// directly. Mirrors the pre-Phase-3 sink closure's cases.
type EventKind int

const (
	EventRouteSelected   EventKind = iota // model badge (which provider/model served the turn)
	EventToken                            // assistant text delta
	EventProgress                         // status message
	EventToolUseStart                     // model planned a tool call
	EventToolUseStop                      // model finalized a tool call (with args)
	EventToolExecStart                    // tool execution began
	EventToolExecComplete                 // tool execution finished
	EventWatchdog                         // protocol-supervision event (challenge/block/echo)
	EventDone                             // turn complete; Result populated
)

// Event is one runner-emitted notification. Only the fields relevant to Kind
// are set. Proto-free by design.
type Event struct {
	Kind EventKind

	// Text: EventToken text delta; EventProgress message.
	Text string

	// Route: EventRouteSelected.
	Model   string
	IsCloud bool

	// Tool lifecycle: EventToolUse*/EventToolExec*.
	ToolUseID   string
	ToolName    string
	ArgsSummary string
	Detail      string
	Summary     string
	StartLine   int
	IsError     bool

	// Watchdog: EventWatchdog.
	WatchdogKind string
	Thread       string

	// EventDone.
	Result Result
	Notice string
}

// EventSink receives runner events as the turn runs. The in-process host
// implements it by mapping each Event to stream.Send(proto...); a worker
// serializes them over its bidi stream.
type EventSink interface {
	Emit(ev Event)
}

// PermissionRequester gates a W/X tool call: the runner asks, the host prompts
// the client and blocks until a decision (or ctx cancel). Separate from
// EventSink because it is request/response, not fire-and-forget. In-process
// this wraps the permission broker; a worker round-trips it over its stream.
type PermissionRequester func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (allow bool, err error)

// PersistFunc persists one turn message. The host fences it by turn generation
// (a superseded turn's writes are dropped), so the runner calls it blind.
// Nil-safe: nil means persistence disabled for this turn.
type PersistFunc func(m llm.Message)
```

- [ ] **Step 4: Write the runner interface + request/result/deps**

`internal/runner/runner.go`:
```go
// Package runner executes one conversation turn in isolation. A Core holds no
// process-global mutable state, so many Cores run concurrently in one process
// (embedded mode) or one Core runs per worker process (Phase 5) — the same
// code path either way.
package runner

import (
	"context"

	"cercano/source/server/internal/agent"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	permissions "cercano/source/server/internal/hostsvc/permissions"
	persistsvc "cercano/source/server/internal/hostsvc/persistence"
	providers "cercano/source/server/internal/hostsvc/providers"
	tools "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/watchdog"
)

// Request carries the user-facing inputs for one turn. It deliberately holds NO
// llm.Provider and NO assembled history — the runner resolves the provider and
// assembles history itself (so it works across a process boundary; see the
// plan's load-bearing decision).
type Request struct {
	ConversationID string
	Input          string
	Images         []agent.InlineImage
	WorkDir        string
	Gen            uint64 // this turn's generation, for the host's persist fence
}

// Result is the turn's outcome.
type Result struct {
	FinalText    string
	Model        string
	IsCloud      bool
	InputTokens  int
	OutputTokens int
	Notice       string // e.g. a cross-tier fallback note
}

// TurnRunner executes one turn. RunTurn emits events via sink, gates W/X calls
// via requester, persists via persist (host-fenced), and returns the result.
// It must hold no process-global mutable state.
type TurnRunner interface {
	RunTurn(ctx context.Context, req Request, sink EventSink, requester PermissionRequester, persist PersistFunc) (Result, error)
}

// Deps are the shared, process-wide services the runner consumes. Injected once
// at construction; a worker builds its own set from a config snapshot.
type Deps struct {
	Providers providers.Resolver
	Tools     tools.Catalog
	Persist   persistsvc.Service
	Config    cfgsvc.Service
	Perms     permissions.Broker
	Agent     *agent.Agent
	Watchdog  *watchdog.Watchdog // nil = disabled
}
```

- [ ] **Step 5: Run the tests, verify pass**

Run: `go test ./internal/runner/ -count=1`
Expected: PASS.

- [ ] **Step 6: Confirm no import cycle / proto-freedom**

Run: `go list -deps ./internal/runner | grep -E 'internal/server|pkg/proto' || echo CLEAN`
Expected: `CLEAN` (runner imports neither the server package nor proto).

- [ ] **Step 7: Commit**

```bash
git add internal/runner/runner.go internal/runner/event.go internal/runner/runner_test.go
git commit -m "feat(runner): define the TurnRunner contract (proto-free events, injected deps)"
```

---

## Task 2: Extract the execution core into `runner.Core`

Move the per-turn execution — provider resolution, history assembly, system-prompt build, the tool-loop closures, `runMainLoop`, and cross-tier fallback — out of `streamProcessRequestWithToolLoop`/`runMainLoop` into `runner.Core.RunTurn`. Repoint the front door to build adapters and call the runner.

**Files:**
- Create: `internal/runner/core.go`
- Modify: `internal/server/server.go` (`streamProcessRequestWithToolLoop` shrinks to host glue; `runMainLoop` + `buildSystemPrompt` move to the runner; the sink/requester/persist closures become adapters that satisfy the runner's `EventSink`/`PermissionRequester`/`PersistFunc`)
- Test: existing `internal/server/toolloop_persist_test.go`, `streaming_test.go`, `agentic_*_test.go`, `turn_exclusivity_test.go` are the regression net.

**Interfaces:**
- Consumes: everything from Task 1 (`runner.Deps`, `runner.Request`, `runner.EventSink`, `runner.PermissionRequester`, `runner.PersistFunc`, `runner.Event`, `runner.Result`).
- Produces: `runner.New(d Deps) *Core`, `(*Core).RunTurn(...)`.

**Method (this is a behavior-preserving MOVE, not a rewrite — the reviewer verifies the diff relocates logic, keeps the suite green):**

- [ ] **Step 1: Build `runner.Core` by moving the execution bodies**

Create `internal/runner/core.go` with `Core` holding `Deps`, and `RunTurn` that reproduces — verbatim, receiver-adapted — the current execution sequence from `streamProcessRequestWithToolLoop` + `runMainLoop`:
1. Resolve the provider (move `resolveMainProvider`/`mainModelFor` logic in, reading `d.Config.Get().LocusMode` + `d.Providers`). Emit `Event{Kind: EventRouteSelected, Model, IsCloud}`.
2. Assemble history via `d.Persist.AssembleHistory(ctx, req.ConversationID)`.
3. Thread the session: `ctx = anthropic.WithSessionID(ctx, req.ConversationID)` — preserve exactly (Phase-2/session-fix behavior).
4. Build the watchdog gate/turnEnd + gateRegistry from `d.Watchdog`/`d.Tools.Registry()`/`d.Config` (move the block verbatim).
5. Build the tool-loop input and call `agent.RunToolLoop` (move `runMainLoop`'s body): `Provider`, `Registry` (gateRegistry or `d.Tools.Registry()`), `Permissions: d.Perms.Store()`, `UserInput: req.Input`, `Images: req.Images`, `Model`, `System: buildSystemPrompt(d, req.WorkDir)`, `WorkDir: req.WorkDir`, `ConversationID: req.ConversationID`, `EventSink`: an internal adapter mapping `agent.LoopEvent` → `sink.Emit(Event{...})`, `PermissionRequester: requester`, `ConvHistory`, `OnTextDelta`: `func(t){ sink.Emit(Event{Kind:EventToken,Text:t}) }`, `OnTurnComplete: persist`, `WatchdogGate`, `WatchdogTurnEnd`.
6. Cross-tier fallback: move the fallback block — on `loopErr` with a cross-allowed locus, re-resolve the fallback provider and re-run the loop. (Fallback lives in the runner now.)
7. Emit `Event{Kind: EventDone, Result: {...}, Notice}` and return the `Result`.

Move `buildSystemPrompt` into `core.go` as an unexported `buildSystemPrompt(d Deps, workDir string) string` (it reads `d.Persist.LoadProjectContext(workDir)` + env grounding). The `agent.LoopEvent` → `runner.Event` mapping is the inverse of the old sink closure — one `switch ev.Kind` with a case per `LoopToolUseStart/Stop`, `LoopToolExecStart/Complete`, `LoopWatchdog*`.

- [ ] **Step 2: Repoint the front door**

In `internal/server/server.go`, rewrite `streamProcessRequestWithToolLoop` to host glue:
```go
func (s *Server) streamProcessRequestWithToolLoop(req *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	ctx, turnGen, releaseTurn := s.beginTurn(stream.Context(), req.GetConversationId())
	defer releaseTurn()

	convID := req.GetConversationId()
	// user-turn persistence up front (unchanged) ...
	persistEnabled := s.agent != nil && convID != "" && s.agent.PersistentStore() != nil
	if persistEnabled { /* EnsureConversation + persist the user turn, as today */ }

	sink := &protoSink{stream: stream} // maps runner.Event -> stream.Send(proto...)
	requester := s.permissionRequester(stream) // existing closure, unchanged
	var persist runner.PersistFunc
	if persistEnabled {
		persist = func(m llm.Message) {
			if s.turnIsCurrent(convID, turnGen) {
				s.persistTurn(ctx, convID, m)
			}
		}
	}

	res, err := s.runner.RunTurn(ctx, runner.Request{
		ConversationID: convID,
		Input:          req.GetInput(),
		Images:         mapInlineImages(req.GetImages()),
		WorkDir:        req.GetWorkDir(),
		Gen:            turnGen,
	}, sink, requester, persist)

	// post-turn host bookkeeping: ScheduleRecap/ScheduleCompaction/RecordContextUsage
	// + stream.Send(FinalResponse) — moved out of the runner, stays here.
	// (err handling: send an error/progress payload as today.)
	_ = res
	return err
}
```
Add a `protoSink` type in the server package: a `runner.EventSink` whose `Emit` switches on `ev.Kind` and calls `stream.Send(&proto.StreamProcessResponse{...})` — this is the OLD sink closure's body, relocated, one proto payload per event kind (RouteSelected, TokenDelta, ToolUseStart/Stop with `summarizeArgs`, ToolExecStart/Complete, WatchdogEvent, and FinalResponse on EventDone OR keep FinalResponse in the handler — pick one and be consistent). Wire `s.runner = runner.New(runner.Deps{Providers: s.providerSvc, Tools: s.toolSvc, Persist: s.persistSvc, Config: s.cfgSvc, Perms: s.permBroker, Agent: s.agent, Watchdog: s.watchdog})` in `NewServer`, and add a `runner runner.TurnRunner` field to `Server`. Delete `runMainLoop` and `buildSystemPrompt` from the server (now in the runner) — or keep thin shims only if a test calls them directly (grep first).

- [ ] **Step 3: Build + run the regression net under -race**

Run: `go build ./... && go vet ./... && go test -race ./internal/server/... ./internal/runner/... ./internal/hostsvc/... -count=1`
Expected: PASS. The existing turn tests (`toolloop_persist_test.go` — persists user + assistant turns; `streaming_test.go`; `agentic_*` via dispatch; `turn_exclusivity_test.go` — supersession fence) prove behavior is preserved. If `turn_exclusivity_test.go`'s persist-fence test fails, the `turnIsCurrent` fence wiring in the persist adapter is wrong — fix the adapter, not the test.

- [ ] **Step 4: Full suite**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/core.go internal/server/server.go
git commit -m "refactor(server): extract turn execution into internal/runner.Core"
```

---

## Task 3: The concurrent cross-talk guard test (the load-bearing invariant)

Prove two `Core`s run concurrently in one process with different workdirs, conversation ids, and sessions and never interfere — the invariant that makes both embedded mode and the Phase-5 worker safe. This test IS embedded mode.

**Files:**
- Create/extend: `internal/runner/core_test.go`
- Modify: `docs/agent/agent-isolation/design.md` (one line noting the invariant is now test-guarded)

**Interfaces:**
- Consumes: `runner.New`, `runner.Core.RunTurn`, and fake implementations of the `Deps` service interfaces.

- [ ] **Step 1: Write the failing concurrent guard test**

`internal/runner/core_test.go` — construct two `Core`s (or call `RunTurn` twice concurrently on cores built with fakes) whose fake `providers.Resolver` returns a scripted provider that (a) records the `WorkDirFromContext(ctx)` it sees and the `SessionIDFromContext(ctx)`, and (b) has each "tool" write a file under the request's WorkDir. Run two turns concurrently under `-race` with `WorkDir` = two distinct `t.TempDir()`s and two distinct `ConversationID`s. Assert: each turn's provider saw its OWN workdir + session id (no cross-read), and each file landed under its OWN tempdir (impossible if any process-global cwd/session were shared). Use fakes for all `Deps` services so the test is hermetic and fast.
```go
func TestCore_ConcurrentTurns_NoCrossTalk(t *testing.T) {
	// two independent cores, two workdirs, two sessions, run concurrently
	// under -race; assert each turn only ever observed its own WorkDir +
	// ConversationID, and wrote only under its own tempdir. (Full fake setup
	// per the Deps interfaces — providers.Resolver returning a scripted
	// provider that records ctx WorkDir/session and writes a marker file.)
}
```
(Author the fakes to satisfy `providers.Resolver`, `tools.Catalog`, `persistence.Service`, `config.Service`, `permissions.Broker` minimally — return-nil / scripted as needed. The scripted provider is the observation point.)

- [ ] **Step 2: Run it under -race, verify it passes**

Run: `go test -race ./internal/runner/ -run TestCore_ConcurrentTurns_NoCrossTalk -count=1`
Expected: PASS. (It passes because the isolation already holds — this test LOCKS it. To prove the test bites, temporarily add a package-level `var lastWorkDir string` the core writes and the provider reads instead of ctx; the assertion must then fail. Revert.)

- [ ] **Step 3: Note the invariant in the design doc**

In `docs/agent/agent-isolation/design.md`, under the Phase 3 / load-bearing-invariant text, add: "Guarded by `internal/runner.TestCore_ConcurrentTurns_NoCrossTalk` — two cores, different workdirs/sessions, concurrent under -race, zero cross-talk."

- [ ] **Step 4: Full suite under -race**

Run: `go test -race ./internal/server/... ./internal/runner/... ./internal/hostsvc/... -count=1 && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/core_test.go docs/agent/agent-isolation/design.md
git commit -m "test(runner): lock the zero-cross-talk invariant with concurrent-cores guard"
```

---

## What Phase 3 deliberately does NOT do

- No worker process / IPC — Phase 5. The runner is in-process only here; the interface is what makes the worker a later transport swap.
- No broker fan-out / multi-surface attach — Phase 4. Turn-exclusivity stays as-is on the host; Phase 4 formalizes the broker around it.
- No change to `agent.RunToolLoop` — the runner wraps it, unchanged.
- No client-visible behavior change — the proto stream is byte-identical; only the internal call path changes.

## Self-review

- **Spec coverage:** design doc Phase 3 = "extract `TurnRunner` (in-process wrapper) + the concurrent-two-runners guard test." Task 1 = contract, Task 2 = extraction, Task 3 = guard test + invariant doc. The runner-owns-provider-resolution decision is called out up top for approval. Turn-exclusivity-stays-on-host is stated in "What STAYS."
- **Type consistency:** `TurnRunner.RunTurn(ctx, Request, EventSink, PermissionRequester, PersistFunc) (Result, error)` used identically in Tasks 1–3; `runner.New(Deps) *Core`; `Event`/`EventKind` vocabulary defined once (Task 1) and mapped in Task 2's `protoSink`.
- **Known unknowns (resolve at implementation, flagged not hidden):** the exact `agent.LoopEvent` kind names + `agent.ToolLoopResult` fields (grep `internal/agent/toolloop.go` before writing the mapping — the plan lists them from the Phase-3 dependency map but the implementer confirms against source); whether `FinalResponse` is emitted as `EventDone` via the sink or sent directly in the handler (Task 2 says pick one — recommend: handler sends FinalResponse, sink handles the streaming events, so the host owns turn-completion bookkeeping + the final send together).
