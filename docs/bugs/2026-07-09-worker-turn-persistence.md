# Worker-executed turns were never persisted (silent conversation data loss)

**Date:** 2026-07-09
**Severity:** critical (silent data loss)
**Area:** `runner/core.go`, `server/server.go`, worker execution mode

## Symptom

After rebuilding from `main` (which defaults the execution mode to **worker**) and
restarting the agent, conversations stopped being saved. The SQLite store
(`~/.config/cercano/conversations.db`) froze: the newest committed turn stayed
stuck at the moment of the last in-process turn, even as the live session
continued for hours. On the next restart the entire post-restart conversation
was gone. No error was logged.

Observable fingerprint: the DB write-ahead log (`-wal`) kept growing (writes were
being attempted) while a fresh reader saw no new committed turns, and a live
`cercano worker` child process was running for the affected conversation.

## Root cause

Turn persistence lived entirely in `runner/core.go`, gated on:

```go
persistEnabled := c.d.Agent != nil && req.ConversationID != "" &&
    c.d.Agent.PersistentStore() != nil
```

The runner runs in **both** the in-process host and the worker child. The worker
child builds its deps with `Agent: nil` (`worker/worker.go`) — correctly, because
the persistent store lives on the host, not the child. The child persists by
forwarding each turn message to the host over the stream
(`preloadedHistory.PersistTurn → streamPersistFunc → WorkerToHost_Persist →`
the host's fenced `persist` callback).

But because `persistEnabled` was gated on `c.d.Agent != nil`, it was **false** in
the worker child. So the runner never called `persist(...)` — neither the
up-front user turn nor the per-message `onTurn`. The entire forward-to-host path
was live code that was never invoked. `persistEnabled == false` is the silent
branch (its only log fires on an `EnsureConversation` error, which was never
reached), so nothing surfaced.

Compounding it: conversation rows are created lazily, only by
`EnsureConversation` inside `core.go`. With that skipped in the worker, a
brand-new conversation's row was never created either — so even if persistence
had been attempted, a fresh conversation's writes would have hit a missing-parent
foreign key.

## Fix

1. **`runner/core.go`** — enable persistence whenever a `persist` sink is present
   and the conversation id is non-empty, independent of `c.d.Agent`. Only run the
   local `EnsureConversation` when a local store exists (in-process); in worker
   mode the host ensures the row. Also guard the post-turn recap/compaction
   scheduling (`ScheduleRecap`/`ScheduleCompaction`) on `c.d.Agent != nil` — those
   are host-owned and the earlier `persistEnabled` gate used to imply
   `Agent != nil`, which it no longer does.

2. **`server/server.go`** — for worker turns only, `EnsureConversation` on the
   host before `RunTurn`, so the worker's forwarded writes always have a parent
   row. In-process is unchanged (the runner still ensures the row itself).

## Regression guard

`runner/worker_persistence_test.go`:
`TestCore_PersistsWithoutLocalStore_WorkerParity` runs the runner with
`Deps.Agent == nil` (the worker shape) and a capturing `persist` func, asserting
the user turn is persisted up front. The existing `worker.TestBothModes_Parity`
also exercises the path and caught a follow-on nil-deref during the fix.

## Follow-ups — RESOLVED (commit 1938863f)

Recap, compaction, and context-usage recording did not run for worker-executed
turns (host-owned, skipped by the Agent-less worker child). An audit also found
a fourth: usage/cost telemetry was dropped (`workerResolver.SetUsageSink` is a
no-op and the child provider is never `usage.Wrap`-ped). All four now run
host-side via `Server.workerPostTurn`, invoked for worker turns only right after
`RunTurn`, using the model + aggregate token counts the worker returns in
`TurnDone`:

- `RecordContextUsage` — the context meter advances, so reactive auto-compaction
  triggers again (a long worker conversation was otherwise at risk of growing
  until it overflowed the model context) and the context % is accurate.
- `ScheduleRecap` / `ScheduleCompaction` — auto-titles and background compaction.
- one aggregate usage event per worker turn so cost/usage telemetry does not
  silently zero out (in-process still emits per model call via `usage.Wrap`).

In-process is untouched (the runner already did this), so no double-counting.
Regression: `internal/server/worker_postturn_test.go`.
