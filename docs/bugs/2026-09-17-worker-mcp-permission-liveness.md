# Worker MCP gating uses StartTurn-pinned permissions (mid-turn tightening window)

Known gap, recorded 2026-09-17. Introduced by the worker MCP proxy
(`worker-mcp-proxy`, commit `1078f4a4850c`). Found by adversarial review of the
claim "worker-side gating for proxied MCP tools is equivalent to host-side
gating" — the claim was refuted on this point and holds on every other.

## Symptom

A permission change made **during** an in-flight worker turn does not affect
that turn. Tightening applies on the next turn instead of immediately.

Concretely, if a turn is running in a worker and you then:

- leave bypass mode (`/strict`, `/permissive`), or
- remove an `mcp_allow` pattern from `~/.config/cercano/permissions.yaml`,

the remainder of that turn keeps gating on the values captured when the turn
started. An in-process turn honors the change on its very next tool call.

The window is therefore **strictly more permissive in a worker than in-process**,
and it closes when the turn ends.

## Root cause

`agent.RunToolLoop`'s gate reads permissions per decision, deliberately:

```go
// mode is re-read per call so a mid-turn change takes effect immediately
```

In-process that read hits `permissions.yaml` through a file-backed
`PermissionStore`. A worker has no access to that file, so the host ships the
mode (`StartTurn.permission_mode`, pre-existing) and the MCP allowlist
(`StartTurn.mcp_allow`, added with the proxy) once, at turn start. The worker's
store is file-less, so its per-decision read returns the same pinned values for
the life of the turn.

This is not a regression in the permission code. It is the cost of moving
MCP-involving turns into the worker: before the proxy, those turns ran
in-process (`pickTurnRunner` diverted them) and re-read the file normally.

## Scope — what is NOT affected

The other three gating properties were checked and hold:

- **Origin.** Worker proxy tools implement `agenttools.Originer` and report
  `OriginMCP`, so `GateDecisionForMCP` receives `isMCP=true` and treats them as
  third-party. `TestProxyToolReportsMCPOriginThroughOriginOf` fails if `Origin()`
  is removed (verified by mutation).
- **Allowlist content.** The host's patterns travel in `StartTurn` and are
  reconstituted via `NewStaticPermissionStoreWithMCPAllow`. Without this the
  worker reports *nothing* allowlisted and over-prompts —
  `TestWorkerWithoutShippedAllowlistWouldRegress` pins this.
- **Mode value.** Already transported; the worker never defaults it.

Only *liveness within a turn* is affected — not correctness at turn start.

## Severity

Low, and fail-safe in the common direction:

- Turn boundaries are short; the window is bounded by one turn.
- Mid-turn permission edits are rare.
- Loosening mid-turn is also delayed, which is the safe direction.
- The unsafe direction (delayed *tightening*) requires the operator to change
  permissions while a worker turn is actively running.

It is recorded rather than fixed because closing it properly means changing a
shared permission-broadcast path — beyond the scope of the proxy work.

## Inert scaffolding already present

The receiving half is implemented and tested, but **nothing sends the message**:

- `proto.PermissionUpdate` (`HostToWorker.perm_update`, field 10).
- `agent.PermissionStore.ApplyRuntimeUpdate(mode, mcpAllow)` — no-ops on
  file-backed stores so a push cannot race the file watcher.
- The worker recv-loop arm at `internal/worker/worker.go` (`GetPermUpdate`),
  which applies the update through an `atomic.Pointer` published by `buildDeps`.
- `TestWorkerStoreReflectsMidTurnModeTightening` and
  `TestWorkerStoreReflectsMidTurnAllowlistRemoval` in
  `internal/worker/mcp_liveness_test.go`.

So the fix is host-side only: decide a trigger and call
`sndr.send(&proto.HostToWorker{Msg: &proto.HostToWorker_PermUpdate{...}})` on the
in-flight turn stream. Anyone finishing this should delete this file.

## Fix options considered

1. **Hook the existing broker broadcast** (`internal/hostsvc/permissions`)
   and push to live worker streams on any permission change.
   Covers every change source — RPC, file edit, allowlist edit — automatically.
   Requires extending a currently mode-only, mode-deduped broadcast to carry the
   allowlist (the dedupe must not swallow allowlist-only changes) and tracking
   in-flight worker streams on `workerRunner`. Correct by wiring discipline.
   *Preferred.*

2. **Worker queries the host per gate decision.** Correct by construction — no
   window, and no change source can be forgotten. Costs a stream round trip on
   every W/X tool call and makes a slow or blocked host stall each gated call.
   Gated calls already wait on a human prompt, so the latency is tolerable; the
   added failure mode is the real objection.

Rejected: sending an update only when the host is *already* answering a
permission request. It looks live but misses the case that motivated this
entry — a tool that stops being allowlisted never triggers a request, so the
tightening is never delivered.
