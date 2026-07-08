# Agent execution isolation — design

**Status:** design approved 2026-07-07 (brainstorm). Target architecture +
migration roadmap. Each phase gets its own implementation plan as we reach it.

## Problem

The agent server is a singleton process. Every conversation's turn executes in
that one address space, sharing mutable execution state. This has produced a
recurring class of bugs we have spent significant effort fixing one at a time:

- **cwd cross-talk** — the turn handler does process-global `os.Chdir(WorkDir)`
  with `defer os.Chdir(prev)`. Concurrent turns from different conversations
  race the process cwd; the `defer` interleaving leaves it permanently drifted.
  A relative-path `Read`/`Write`/`Bash` in one conversation can resolve into
  another's repo. (`server.go` ~2336.)
- **Meridian session cross-delivery** — fixed by scoping the upstream session id
  per conversation (`204fb3cc`), but the root shape was shared routing state.
- **Turn overlap** — fixed by per-conversation turn exclusivity + a generation
  fence (`abf72670`), again shared execution state across turns.

These are all one defect: **conversations sharing mutable execution state in one
process.** There is no crash isolation either — one panic, OOM, or fatal runtime
error takes down every conversation.

Cercano is also **designed to be embeddable** in a host application, which
constrains any solution: it cannot assume it may spawn subprocesses.

## Goals

1. **Isolation by construction** — a conversation's execution state (cwd,
   provider/session, tool loop) is owned per conversation, so the cross-talk
   bug class becomes unrepresentable rather than fixed case by case.
2. **Crash isolation** — one conversation dying cannot take down another (or the
   server), where the deployment allows it.
3. **Multi-surface attach** — the same conversation can be live in several
   surfaces at once (CLI + VS Code), attaching to and detaching from a running
   conversation freely.
4. **Embeddable** — the same execution core runs in-process when Cercano is
   embedded, with full correctness isolation (crash isolation is then the host
   app's choice).

## Non-goals

- Not a distributed/multi-machine system. Workers are local child processes.
- Not a filesystem/resource sandbox for untrusted workspace code (a later,
  separate concern). This is fault + correctness isolation, not a security
  boundary.
- No gratuitous refactoring beyond the seams this work forces.

## Core idea: one execution core, two deployment wrappers

The correctness bugs come from shared *mutable state*, not from the lack of a
process boundary. So the isolation lives in the **execution core**, and the
process boundary is an *additional* wrapper on top — not a second
implementation.

- **`TurnRunner` (the core):** executes a conversation's turns. Owns **all**
  execution state per conversation — cwd carried explicitly (never
  `os.Chdir`), its own provider/session, its own tool loop. **Zero
  process-global mutable state.** This is where correctness lives, and it is
  identical in both deployments.
- **Deployment wrapper (a hosting/transport detail outside the core):**
  - **Worker mode (default):** the host spawns a child process that runs one
    `TurnRunner`, reached over a bidirectional gRPC stream. Adds crash +
    resource isolation.
  - **Embedded / in-process mode:** the host `new()`s a `TurnRunner` per
    conversation and runs it in a goroutine, reached over an in-memory channel
    implementing the same interface. Full correctness isolation; no crash
    isolation.

The broker talks to "a runner behind an interface" and never knows which. The
delta between modes is instantiation + transport — a thin adapter, not a
parallel code path with its own bug surface.

**Load-bearing invariant:** the runner core holds zero process-global mutable
state. This is what makes N in-process runners safe. It is directly testable —
the guard test *is* embedded mode: run two runners concurrently in one process
with different workdirs and sessions, assert no cross-talk. Milliseconds to run,
guards the whole design.

**Consequence for testing:** the in-process path is the **default test path**
(fast, no spawn), so the core + in-process wrapper are the *most*-exercised code,
not the least. Worker mode gets focused integration tests for
spawn/transport/crash-recovery. Neither deployment is an under-tested stepchild.

## Isolation unit

**One worker (or in-process runner) per active conversation.** Smallest blast
radius: a crash kills exactly one conversation. Each conversation gets its own
cwd and its own Meridian session by construction. Spawn lazily on the first
turn; idle-reap when there is no active turn, independent of who is attached.

## Host decomposition (full)

Today one `Server` struct (~30 fields, three mutexes) fuses every
responsibility. It is decomposed into focused services behind clear interfaces,
wired by a thin composition root. The front door *delegates* to them instead of
*being* them.

| Component | Owns (today's fields) | Job |
|---|---|---|
| Front door | `proto.UnimplementedAgentServer` + handlers | terminate client conns, route, proto↔domain. Thin. |
| Config | `configPath`, `currentConfig`/`cfgMu`, `secrets`, perm-broadcast | canonical config + secrets; persist; notify |
| Providers | `cloud/openLLMProvider`, `cloudFactory`, `router`, `coordinator`, `EngineRegistry`, `catalogManager` | given config+locus, hand out the right provider |
| Runtime supervisors | `meridianMgr`, `runtimeManager`, `mcpManager` | spawned-subprocess lifecycle — **workers join this family** |
| Persistence | conversation store, `retentionSweeper`, `compactionGen`, `contextLoader` | durable conversation state; the shared transcript |
| Tool catalog | `toolRegistry`, `capRegistry`, `dispatchEngine` | the tool universe + dispatch |
| Permissions | `permStore`, `pendingDecisions` | policy + the confirm round-trip |
| Broker | (new) + `eventHub`, `turnsMu/activeTurns/turnGens` | route conv→runner, fan-out, attach/detach |
| Runner | `agent.Agent` + the turn loop | execute the turn (the core above) |

**Why full decomposition is load-bearing, not aesthetic:**

1. **It is a precondition for a clean worker boundary.** A worker needs exactly:
   a config snapshot, provider access, the tool catalog, and callbacks for
   permission + persist. Tangled in one struct, you cannot hand a worker a clean
   subset without dragging the god-object (client streams, gRPC server,
   subprocess managers) across the boundary. Decomposition is what makes the
   boundary cuttable.
2. **It is what makes Cercano embeddable.** A host app instantiates the clean
   service set it wants. The god-object is un-embeddable — it drags a gRPC
   server and subprocess managers with it.
3. **The bugs we fought are god-object symptoms.** Each was "a field on the big
   `Server` mutated by two paths." Owned state + explicit interfaces make that
   class hard to write.
4. **Lock soup goes away.** Three cross-cutting mutexes on one struct become
   per-component ownership, each owning its own synchronization.
5. **Testability + navigability** — focused services test in isolation; a
   30-field struct is the "too big to hold in context / edit reliably" signal.

## Host as broker; host owns the store

The host keeps the singleton `:50052` and stays the only endpoint clients talk
to. Per conversation:

- **conversation ↔ runner is 1:1** — one executor, never multiplexing two
  conversations (the isolation guarantee).
- **conversation ↔ surfaces is 1:many** — CLI + VS Code + others attach.

Two fan-out shapes meet at the host: events fan **out** from one runner to many
surfaces; input funnels **in** from many surfaces to one runner (serialized by
the turn-exclusivity fence already shipped).

**Host owns the conversation store.** Runners are stateless executors that stream
completed turns back to the host to persist. One writer, many readers; no
multi-process SQLite lock contention; persistence stays authoritative and
crash-consistent in one place. This is also the shared transcript every surface
reads on attach.

## Multi-surface attach

Attachment is a **host concern, not a runner concern.** A surface attaches to a
*conversation* at the host and gets: a state snapshot (persisted transcript from
the store) + a live subscription to that conversation's event stream. The runner
streams its turn events to the host; the host fans them out to every attached
surface. The runner never needs to know two surfaces exist.

- **Runner lifetime decouples from any surface.** It spawns on a turn, streams,
  idle-reaps — regardless of who is attached. Closing the CLI kills nothing. The
  durable thing is the conversation (store + host-side stream); the runner is a
  transient engine behind it. A surface can attach whether or not a runner is
  currently live.
- **New to build:** per-conversation event fan-out at the host (a scoping of the
  existing `eventHub`), `Attach`/`Detach` RPCs, snapshot-on-attach (transcript +
  "is a turn streaming now"). All host-side; the runner and client core are
  unaffected.

## Transport + permissions

**Transport reuses the protocol we already have, on both sides.** Today the host
is a gRPC server to clients, streaming `TokenDelta`/`ToolUseStart`/
`PermissionRequired`/`Done` down and taking `AllowToolCall`/`DenyToolCall` back.
In the new shape the host is a **broker in the middle**: it plays client toward
the runner (drives a `RunTurn` bidi stream) while still playing server toward the
surfaces. The runner emits the same event vocabulary up; the host consumes each
event and does one of: fan out to surfaces, persist to the store, or mediate a
permission decision — decisions go back down the same bidi stream. Almost no new
proto; in embedded mode the same interface is backed by an in-memory channel.

**Permission round-trip — the attach model answers "which surface."** The host
knows which surface drove the turn (it routed the submit). The confirm prompt
goes to the **driving surface**; first (only) decision wins. Other attached
surfaces show a read-only "waiting on approval" indicator (already receiving the
fan-out). If the driver detaches mid-prompt, fall back to any attached surface;
if none, hold (or auto-deny on timeout).

**Config** is a spawn-time snapshot. Runners are transient (spawn on a
turn-burst, idle-reap), so a snapshot handed over at spawn is almost always
current; a config change applies on the next turn's runner. Host stays canonical
owner. No live config push in v1.

## Failure semantics

- **Worker dies mid-turn** (panic/OOM/killed): host detects via stream close or
  health probe. Host has persisted at the `onTurn` cadence + the in-flight text
  buffer, so it marks the turn failed at a clean boundary, surfaces "turn
  interrupted" to attached surfaces (reusing existing transport-loss messaging),
  reaps the worker (pidfile/group), and the next submit spawns fresh. The
  generation fence already shipped guarantees a dead worker's late events can't
  corrupt live state.
- **Host dies:** workers are its children (Setpgid group). Clean shutdown →
  `DrainThenStop` + kill the group (shutdown fix, this session). Hard-kill →
  workers orphan → the next host reaps them by pidfile, exactly like Meridian.
- **Idle worker:** reaped after an idle window (no active turn), independent of
  attachments. Next turn re-spawns.

## Migration roadmap

Phased so each step ships working software. One design doc (this); each phase
gets its own implementation plan as we reach it — the seam in phase 3 will teach
us things about 4–6.

1. **cwd fix (independent, ship now).** Thread `WorkDir` into tool execution,
   delete `os.Chdir`. Fixes the live correctness bug today, needs none of the
   arch, and forces `WorkDir` explicit in the tool path — the first step of the
   runner owning its execution state.
2. **Decompose the host** into the services above. Pure refactor, no behavior
   change, in-process. The foundation everything else builds on.
3. **Extract `TurnRunner`** with owned per-conversation state + zero
   process-global mutable state, behind an interface, in the in-process wrapper.
   Add the concurrent-two-runners guard test. **The correctness bug class is
   structurally dead here — before workers exist.**
4. **Host broker + multi-surface attach** — per-conversation fan-out,
   `Attach`/`Detach`, snapshot-on-attach. Still in-process runner. **Multi-surface
   feature ships here.**
5. **Worker wrapper + transport** — spawn process, bidi gRPC, reuse the Meridian
   supervisor for lifecycle. **Crash isolation lands here** — a transport swap,
   because the interface and the broker already exist.
6. **Harden** — idle-reap, health/restart, crash-mid-turn recovery,
   permission-driver fallback edges.

Payoff ordering: live cwd bug dies at 1, correctness-by-construction at 3, the
multi-surface feature at 4, crash isolation at 5 — each without breaking what
already works.

## Code pointers

- `source/server/internal/server/server.go` — the `Server` god-object; the
  `os.Chdir` at ~2336; `streamProcessRequestWithToolLoop` (the turn handler to
  extract); `beginTurn`/`turnIsCurrent` (turn-exclusivity fence, already built).
- `source/server/internal/server/shutdown.go` — `DrainThenStop` (host shutdown).
- `source/server/internal/meridian/manager.go` — the supervisor pattern workers
  reuse (Setpgid group, pidfile reap, health watch, idle handling).
- `source/server/internal/agent/toolloop.go` — the tool loop the runner owns.
- `source/server/internal/llm/session.go` — per-conversation session identity
  (already threaded; the runner carries it).
