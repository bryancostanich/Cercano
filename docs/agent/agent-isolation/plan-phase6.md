# Agent Execution Isolation — Phase 6 Implementation Plan (Worker Equivalence + Lifecycle Hardening)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make worker-process execution PRODUCTION-READY so `default = worker` is safe: (A) close the three behavior divergences the Phase-5 whole-branch review found (backup-profile failover, watchdog, MCP tools) so a turn in a worker is behavior-equivalent to in-process; (B) harden the worker lifecycle (per-conversation reuse, health/restart, idle-reap, startup orphan-sweep, graceful drain). Phase 5 (unmerged) + Phase 6 then land on main TOGETHER — the default only becomes live when worker mode is genuinely equivalent + hardened, so no silent regression ever reaches main.

**Architecture:** Phase 5 built the worker (spawn-per-turn) behind a `workerRunner` implementing `runner.TurnRunner`, with the worker proxying host-owned state (events/permission/persist/credential) over a bidi gRPC stream. Phase 6 fills the worker's execution `Deps` to match in-process (backup provider, watchdog), routes MCP-configured turns to in-process (MCP tools are host-side), and replaces spawn-per-turn with a supervised per-conversation worker pool. This continues on the `feat/agent-isolation-phase5` branch; the whole worker stack (5+6) merges when done. See `docs/agent/agent-isolation/architecture.md` + `design.md`.

**Tech Stack:** Go 1.21+ (module `cercano/source/server`), the Phase-5 `internal/worker`, the Meridian manager's supervision machinery, gRPC/protobuf (additive only).

## Load-bearing design decisions (review before executing)

1. **Worker lifecycle → per-conversation reuse (design-doc intent; supersedes Phase-5 spawn-per-turn).** A conversation's worker persists ACROSS its turns (warm process, warm provider connection) instead of spawning fresh per turn. The `workerRunner` gets-or-creates a worker keyed by conversation id; a follow-up turn reuses it. This amortizes spawn + provider-build cost (real interactive latency) and matches the design doc's "one worker per conversation." It adds lifecycle: idle-reap when a conversation goes quiet, health/restart of the persistent worker, cleanup on conversation end. If you'd rather keep spawn-per-turn (simpler, but pays spawn cost every message and doesn't match the design), say so before Task B1 — but the recommendation is per-conversation reuse.
2. **MCP → auto-fallback to in-process (user-approved), NOT proxied (yet).** MCP tools run on host-side MCP servers; the worker can't call them. Rather than silently dropping them, the mode selector uses **in-process** for a Cercano instance that has MCP tools configured (detected via `agenttools.OriginOf(tool)==OriginMCP` in the host registry). Coarse (any MCP configured → in-process for that instance) but never silently breaks an MCP user. Proxying MCP tool execution to the host (like permissions) is a cleaner future refinement — noted, not this phase.
3. **Crash-mid-turn → recover the HOST, do NOT auto-retry the turn.** On worker death mid-turn the host already surfaces `codes.Unavailable` (Phase 5). Phase 6 "recovery" means the host cleanly returns to health (turn released/fenced, the dead worker reaped, a fresh worker available for the NEXT turn) — NOT re-running the crashed turn. Auto-retry is unsafe: the crashed turn may have already executed tools (side effects); re-running would double-execute. The client retries explicitly if it wants. If you want auto-retry semantics, flag it — but the recommendation is surface-and-recover, not replay.
4. **Idle-reap window is CONFIGURABLE, not a hardcoded magic number.** A per-conversation worker with no activity is reaped after a configurable idle window (config field, sensible default). Do not bury an arbitrary constant; expose it. (Optionally also reap when no surface is attached AND no turn is active — a more semantic boundary; see Task B3.)

## Scope

- IN (Part A — equivalence, precondition for safe default): backup-profile failover in the worker; watchdog in the worker; MCP-configured → in-process selector.
- IN (Part B — lifecycle hardening): per-conversation worker reuse + supervisor; health-check + restart of a dead persistent worker; idle-reap; startup orphan-sweep (reap stale workers from pidfiles); graceful drain of all workers on host shutdown.
- OUT (future): MCP tool-execution proxying to the host (auto-fallback covers correctness now); worker pooling across DIFFERENT conversations / a shared warm pool; auto-retry of a crashed turn.

## Global Constraints

- Module `cercano/source/server`. Gate every task: `go build ./... && go vet ./... && gofmt -l <changed> is empty && go test -race ./internal/server/... ./internal/worker/... ./internal/runner/... ./internal/broker/... -count=1 && go test ./...` all green.
- **After Part A, worker mode is behavior-equivalent to in-process** for backup/watchdog, and MCP configs use in-process — so `default = worker` no longer silently regresses anyone. `TestBothModes_Parity` should be EXTENDED to cover a backup-configured turn and a watchdog-enabled turn (both modes identical).
- `internal/runner` MUST NOT change (it is transport-agnostic; the worker fills Deps, it doesn't change the runner).
- The existing test suite stays in-process + fast (no spawned processes) — worker-mode + lifecycle tests opt in explicitly (bufconn where possible; one real-process test per lifecycle behavior).
- Reuse the Meridian manager's health/reap/restart patterns (`internal/meridian/manager.go`) — do not reinvent supervision.
- Commit messages: never the word "Claude".

---

## Part A — Worker equivalence (makes default = worker safe)

### Task A1: Backup-profile failover in the worker

The in-process path wraps active+backup via `providers.wrapBackup` → `fallback.New`. The worker only receives the active profile. Fix: snapshot BOTH profiles; build the fallback in the worker.

**Files:** `internal/worker/wire.go` (`SnapshotConfig`/`ConfigFromSnapshot`), `internal/worker/worker.go` (`buildWorkerProviders`), `source/proto/agent.proto` (if the snapshot needs a second profile — likely ConfigSnapshot carries the active profile fields inline; extend to carry the backup profile fields too), tests.

- [ ] **Step 1:** Extend `ConfigSnapshot` to carry the BACKUP profile's fields (mirror the active-profile fields already there) — additive proto, regen with the pinned toolchain (`export PATH=$PATH:$(go env GOPATH)/bin`; from `source/proto/`: `protoc --go_out=../server --go_opt=module=cercano/source/server --go-grpc_out=../server --go-grpc_opt=module=cercano/source/server agent.proto`; no-op regen first = zero churn). `SnapshotConfig` populates both active + backup; `ConfigFromSnapshot` rebuilds a `CloudProfiles` slice with both.
- [ ] **Step 2:** In `buildWorkerProviders`, when a backup profile is present, build the backup provider (same credential-proxy path — fetch its credential via the stream) and wrap `fallback.New(primary, backup, ...)` mirroring `providers.wrapBackup`. Grep `wrapBackup` for the exact wrap (the `fallback.New` args + the stage callback).
- [ ] **Step 3:** Extend `TestBothModes_Parity` (or a new test) with a backup-configured turn: assert the worker builds the fallback composite (same failover behavior as in-process). Confirm the worker fetches BOTH credentials via the proxy.
- [ ] **Step 4:** Gate + commit: `feat(worker): backup-profile failover in the worker (parity with in-process)`.

### Task A2: Watchdog in the worker

In-process passes a live watchdog; the worker sets `Watchdog: nil`. Fix: build a local watchdog in the worker from the snapshotted watchdog config.

**Files:** `internal/worker/worker.go` (`buildDeps` — the `Watchdog` accessor), reusing the host's `buildWatchdogFrom` logic (`internal/server/watchdog_wire.go`), tests.

- [ ] **Step 1:** The `ConfigSnapshot` already carries the watchdog config (WatchdogConfig fields) + models config. In the worker's `buildDeps`, build a `*watchdog.Watchdog` from them — mirror `buildWatchdogFrom(wc, mc)` (`watchdog_wire.go:24` → `watchdog.New(Config{...}, checks, oneShot)`). The `oneShot` model-calling lane uses the worker's OWN provider (the worker resolves it locally) — wire it to the worker's resolved provider/model. Set `Deps.Watchdog = func() *watchdog.Watchdog { return wd }` (the live-accessor shape the runner expects) instead of `nil`.
- [ ] **Step 2:** Consider host-side watchdog state: the watchdog's echo/challenge events already cross as `EventWatchdog` (the runner emits them, they reach the client via the broker). Escalation counters are per-turn/per-conversation — a local worker watchdog tracks them within the turn, equivalent for a single turn. Note any host-side watchdog state that does NOT cross (if any) as a residual limitation.
- [ ] **Step 2b:** Only build the watchdog when enabled (`WatchdogConfig.Enabled`) — nil/off otherwise, matching the host's default-off behavior exactly.
- [ ] **Step 3:** Test: a watchdog-enabled turn in worker mode gates a tool call the same way in-process does (a challenge/block event reaches the client). Both-modes parity for a watchdog-enabled turn.
- [ ] **Step 4:** Gate + commit: `feat(worker): run the watchdog in the worker (parity with in-process)`.

### Task A3: MCP-configured → in-process (auto-fallback selector)

MCP tools are host-side; the worker excludes them. Fix: the mode selector uses in-process when MCP tools are configured, so MCP users aren't silently degraded.

**Files:** `internal/server/server.go` (`SelectExecutionMode`), tests.

- [ ] **Step 1:** In `SelectExecutionMode`, before choosing worker mode, check whether the host tool registry contains any MCP-origin tool (`s.toolSvc.Registry().All()` → `agenttools.OriginOf(t)==agenttools.OriginMCP`). If yes, keep the in-process runner (log a one-line reason: "MCP tools configured — using in-process execution; worker MCP proxying is a future refinement"). Otherwise select worker.
- [ ] **Step 2:** Test: a Server whose registry has an MCP tool selects in-process even with `ExecutionMode=worker`; a Server with no MCP tools selects worker. Document this in the arch doc's limitations section (MCP → in-process for now).
- [ ] **Step 3:** Gate + commit: `feat(server): use in-process execution when MCP tools are configured (worker MCP fallback)`.

**After Part A:** worker mode is behavior-equivalent (backup, watchdog) or safely routed (MCP). `default = worker` is now safe. Part B optimizes + hardens the lifecycle.

---

## Part B — Lifecycle hardening

### Task B1: Per-conversation worker reuse + supervisor

Replace spawn-per-turn with a supervised per-conversation worker that persists across turns.

**Files:** `internal/worker/pool.go` (new — the per-conversation worker registry/supervisor), `internal/worker/host.go` (`workerRunner` gets-or-creates via the pool instead of spawn-per-turn), tests.

- [ ] **Step 1:** A `workerPool` keyed by conversation id: `Acquire(ctx, convID) (*workerHandle, error)` returns a warm worker (reusing an existing healthy one for that conversation, or spawning + caching one). The `workerHandle` gains a "in-use" guard so two concurrent turns on one conversation don't collide (Phase-4 turn-exclusivity already ensures one live turn per conversation, so serialize on that; the pool just keeps the process warm between turns). Under `mu`, thread-safe.
- [ ] **Step 2:** `workerRunner.RunTurn` uses `pool.Acquire(convID)` instead of `spawnWorker`; on the turn's end it RETURNS the worker to the pool (warm) rather than killing it. On worker death mid-turn (crash) → evict from the pool + return `codes.Unavailable` (unchanged crash behavior); the next turn spawns a fresh one.
- [ ] **Step 3:** Preserve everything Phase 5 proved: crash isolation (a pooled worker dying still yields `codes.Unavailable`, host survives), both-modes parity, credential proxy. The existing crash + parity tests must pass with pooling.
- [ ] **Step 4:** Gate + commit: `feat(worker): per-conversation worker reuse (warm process across turns)`.

### Task B2: Health-check + restart

Detect a dead/unresponsive pooled worker and respawn.

**Files:** `internal/worker/pool.go`, tests.

- [ ] **Step 1:** `Acquire` health-checks a cached worker before handing it out (cheap: process alive + the gRPC conn is READY / a lightweight ping). If unhealthy → evict + spawn fresh. This makes a between-turns worker death transparent (the next turn gets a fresh worker) rather than failing.
- [ ] **Step 2:** Reuse the Meridian health-watcher pattern where it fits. Test: a worker killed BETWEEN turns → the next `RunTurn` transparently gets a fresh worker + succeeds (not an error).
- [ ] **Step 3:** Gate + commit: `feat(worker): health-check pooled workers and respawn dead ones`.

### Task B3: Idle-reap

Reap per-conversation workers whose conversations have gone idle.

**Files:** `internal/worker/pool.go`, `pkg/config/config.go` (idle window field), tests.

- [ ] **Step 1:** Add a configurable idle window (`config` field, sensible default — NOT a buried magic constant). A background sweeper in the pool reaps workers whose last-turn time exceeds the window. OPTIONAL stronger boundary: also require "no surface attached to the conversation" (query the broker) before reaping — a worker with an attached surface may still get a turn. Decide + note which policy you implement.
- [ ] **Step 2:** Test: a worker idle past the window is reaped (process killed, pool entry removed); an active/recently-used one is not. Use an injected clock or a tiny test window (not a real sleep of the default).
- [ ] **Step 3:** Gate + commit: `feat(worker): idle-reap per-conversation workers (configurable window)`.

### Task B4: Startup orphan-sweep

On host startup, reap stale worker process groups from leftover pidfiles (a hard-killed host leaves orphans).

**Files:** `internal/worker/spawn.go` (pidfile dir + reap), `cmd/cercano/main.go` (call the sweep at startup), tests.

- [ ] **Step 1:** A `ReapOrphanWorkers()` that scans the worker pidfile dir (`$TMPDIR/cercano-workers`), and for each pidfile whose process group is still alive AND looks like a cercano worker (mirror Meridian's `groupLooksLikeMeridian` identity check — never kill an unidentified pid), kills the group + removes the pidfile/socket. Call it in `runServerMode` at startup (before serving).
- [ ] **Step 2:** Test the identity-guarded reap (a live-but-not-ours pid is NOT killed; a stale ours-pid group is). Mirror the Meridian reaper tests.
- [ ] **Step 3:** Gate + commit: `feat(worker): reap orphaned workers on host startup`.

### Task B5: Graceful drain on host shutdown

Kill all pooled workers on host shutdown (no orphans on clean exit).

**Files:** `internal/worker/pool.go` (`Shutdown()`), `internal/server/server.go` (call from the host's `Shutdown`/`BeginShutdown`), tests.

- [ ] **Step 1:** `pool.Shutdown()` kills every cached worker's process group + removes pidfiles/sockets. Wire it into the host's existing shutdown path (grep `Shutdown`/`BeginShutdown` in server.go — the runtimes `StopMeridian` is a precedent for where worker cleanup hooks in).
- [ ] **Step 2:** Test: after `Shutdown`, no worker processes remain (pool empty, pidfiles gone).
- [ ] **Step 3:** Gate + commit: `feat(worker): drain all workers on host shutdown`.

---

## Wrap-up (after all tasks)

- [ ] Update `docs/agent/agent-isolation/architecture.md`: worker mode is now equivalent (backup/watchdog closed; MCP → in-process); document the per-conversation lifecycle (reuse/health/idle-reap/orphan-sweep/drain). Update the phase-status table (Phase 6 done).
- [ ] Whole-branch review of the ENTIRE worker stack (Phases 5+6) as one change, then the merge decision — land 5+6 on main together with `default = worker`.

## Self-review

- **Spec coverage:** design Phase 6 = "harden (idle-reap, health/restart, crash-mid-turn recovery, pooling)"; PLUS the 3 equivalence gaps the Phase-5 review surfaced (backup/watchdog/MCP). Part A (A1-A3) closes equivalence; Part B (B1-B5) is the lifecycle. Crash-recovery is decision #3 (surface+recover, not auto-retry) realized by B1's evict-on-crash + B2's respawn.
- **Type/behavior consistency:** `workerRunner` still implements `runner.TurnRunner` (unchanged host handler); `internal/runner` untouched; `ConfigSnapshot` gains backup-profile fields (additive); the mode selector (`SelectExecutionMode`) gains the MCP check. `TestBothModes_Parity` extended for backup + watchdog.
- **Known unknowns (resolve at implementation):** (a) the watchdog `oneShot` lane wired to the worker's provider (A2 — confirm the worker can build it); (b) whether idle-reap keys purely on time or also on broker surface-attachment (B3 — pick + note); (c) the health-check probe shape (B2 — process-alive + conn-state, or a lightweight RPC ping); (d) whether backup credential also proxies (A1 — yes, same path). Flag, don't hide.
- **Deferred:** MCP tool-execution proxying (auto-fallback covers correctness); cross-conversation warm pool; auto-retry of crashed turns.
