# Autonomous run — host-managed continuation

A durable autonomous run keeps driving its own conversation from the host.
When a turn finishes normally while a run is still in state `running`, the
host chains another turn into the same conversation instead of ending the
stream; the chain stops itself whenever the run, the conversation, or a
safeguard says so. There is no client-side loop and no automatic launch on
restart.

## Lifecycle

Continuation is evaluated once per successful turn, in `streamProcessRequestWithToolLoop`
(`source/server/internal/server/server.go`), through `evaluateAutonomyContinuation`
(`source/server/internal/server/autonomy_continuation.go`):

- **Gates (all preserved and checked every turn):**
  - The turn generation is still current — a client `BeginTurn` for a newer
    message (or a superseding stream) supersedes the chain via the existing
    `turnIsCurrent` fence; the broker's per-turn cancellation also stops it.
    A superseded or canceled chain is never relaunched in the background: the
    chain only ever rides the open client stream, and the durable run stays
    resumable by the next explicit request.
  - The conversation is non-empty (not CLI one-shot) and has an active run in
    state `running` at turn start. `completed`, `abandoned`, and
    `review_pending` runs stop the chain and wait for a human; the pause is
    reported as a structured `Progress` note carrying the run ID, never as
    prose that looks like an agent response.
  - An error on the previous turn stops the chain — errors are surfaced, not
    retried by chaining.
  - An explicit `report_autonomous_blocker` record stops the chain (below);
    the pause note carries the run ID and the recorded reason.
  - A permission prompt inside a chain turn blocks that turn at the
    permBroker barrier: while a prompt is pending the tool is not executed,
    the turn cannot complete, and no continuation is chained or announced.
    The structured `PermissionRequired` event reaches the open stream (tool
    ID, name, tier); a resolved decision resumes the turn where it stopped
    and the chain continues normally. This is the pre-existing permission
    gating applied unchanged inside the chain, not new chain-specific
    behavior.
  - A bounded idle safeguard pauses the chain after 3 consecutive turns with
    no evidence of movement. **Ledger progress** — a new run identity or a
    run state change — resets the counter; so does **productive tool
    work** — any successful tool execution outside the curated bookkeeping
    list (`capture_decision`, run/plan entry and exit requests, session
    control, and the blocker call itself), including MCP and future tools.
    Decision bookkeeping alone never resets the counter: it is audit trail,
    not progress. Timestamps alone are never evidence either — ledger rows use
    second-granularity wall clocks that cannot prove a turn did work.
- **The continuation turn** is a normal `runner.RunTurn` on the same
  conversation: the host first persists a synthetic user message tagged
  `<host:autonomous-continuation run_id="…" turn="n"/>`, emits an
  `EventProgress` announcement, then runs the loop again. **The host
  provenance is persisted**: the continuation input is recorded in history
  under the `system` input role (see `Request.InputRole` in
  `source/server/internal/runner/runner.go`), so a resumed conversation
  always shows which turns the host generated. Because providers reject or
  drop mid-history system messages, history replay maps these persisted
  system-role turns to the user role for the model (`agent/history.go`,
  `compactor/compactor.go`) — the provenance lives in the durable record,
  not in what the model is fed. Assistant turns persist identically to the
  initial turn, so a continued run remains resumable and inspectable.
- **Turn-start profile rehydration:** once per request, before its first turn,
  if the active profile is the default and the ledger has an active run in
  state `running` or `review_pending`, the host restores the `autonomous`
  profile directly from `GetActiveAutonomyRun`. Clients no longer need to
  call `GetSessionProfile` to keep the profile across restarts, and nothing
  is launched automatically at startup — continuation only resumes when a
  turn actually runs.

## Explicit blocker pauses (`report_autonomous_blocker`)

The `report_autonomous_blocker` capability is the model's own "I need the
user" signal, available in the autonomous profile:

- **Reason required.** The call refuses to record without a specific reason;
  the reason is what the user must act on.
- **Pause without state mutation.** The run deliberately stays `running` —
  no exit, no `review_pending`, no approval-state change; brief and captured
  decisions survive byte-identical. The gate sees the record and stops the
  chain with a structured pause note naming the run ID and the reason.
- **No automatic restart.** Nothing in the host resumes a blocked run by
  itself: the chain only resumes with the user's next explicit message.
  That request clears the recorded blocker at request start (only for runs
  still in `running`), after which the host chains turns normally again. A
  run parked in `review_pending` — even one carrying a stale blocker record —
  is never resumed or mutated by a user message; the exit review stays the
  human's to complete.

## Testing

`source/server/internal/server/autonomy_continuation_test.go` covers: chaining
while running (including tool round trips inside a continuation turn), stopping
for `review_pending`, mid-turn completion (the tool loop's final
`request_autonomous_exit`/`auto_exit` state change stops the chain), the
idle bound with counter reset on ledger progress, reset on productive tool
work (never on failing tools or decision bookkeeping), error stop, profile
rehydration at request start, end-to-end blocker pause/resume semantics,
stream-cancellation and user-supersession stops, a pending-permission
barrier (`source/server/internal/server/autonomy_permission_wait_test.go`:
the turn blocks on the prompt — no tool execution, no final response, no
continuation announcement — and resumes on resolve), and a gate table for
`evaluateAutonomyContinuation` (turn currency, cancellation, state gating,
the blocker record, new-run progress, the bound, and supersession).
`source/server/internal/capabilities/builtins/report_autonomous_blocker_test.go`
covers the capability directly: reason persistence with approvals preserved,
reason required, `review_pending` refusal, and no-active-run refusal.
