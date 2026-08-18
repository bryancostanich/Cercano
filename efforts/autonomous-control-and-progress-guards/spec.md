# Autonomous Control and Progress Guards Spec

## Problem / motivation

Cercano's autonomous-mode and planning handoff flow currently treats some session-control actions too much like ordinary tools. That creates a bad failure mode around approval prompts: a user can type free text such as "approved" or "do it" while a session-control confirmation is pending, the CLI sends that text as a denial-with-message, the server records it as a `FollowUpDenial`, and the tool loop continues the same model turn. The model can then interpret the text as steering and proceed with implementation even though the mode transition or approval boundary did not actually succeed.

The observed `CERCANO - SUB AGENT CONTEXT LIMIT` failure followed this shape: `request_autonomous_execution` did not enter autonomous mode, but the model continued in the same turn, pulled the autonomous protocol, and executed plan work. This is an authority-boundary problem, not a tool-iteration-limit problem.

A separate observation from `CERCANO - MOAR UX` showed a long ordinary implementation turn after an explicit "do it" request. That conversation did not have an autonomy ledger row and did not call autonomous capabilities. It is not autonomous leakage. It is a long normal tool loop with insufficient high-level visibility. This effort should improve that visibility without treating it as the same control-state bug and without lowering the global tool-loop iteration cap.

The autonomy ledger also needs hardening. It is currently modeled as one mutable row per conversation. That conflates active run state with historical audit data and allows later autonomous runs to overwrite earlier run briefs, decisions, and review summaries. Autonomous state-changing capabilities also need stricter preconditions: if a capability depends on autonomous run state, missing or wrong state should fail loudly rather than silently no-op.

## Goals

- Make session-control confirmation prompts explicit approval/decline gates rather than free-text steering points.
- Preserve free-text `chat about this` steering for ordinary tool confirmations.
- Ensure denial-with-message for session-control tools terminates the current tool loop instead of letting the model continue execution in the same turn.
- Ensure execution errors from session-control tools also terminate the current tool loop.
- Enforce strict autonomous state-machine preconditions for autonomous state-changing capabilities.
- Replace the one-row-per-conversation autonomy ledger with append-only per-run records.
- Preserve existing autonomous run data during migration.
- Enforce at most one active autonomous run per conversation.
- Improve visibility for long ordinary tool-heavy turns using UI progress/status, without lowering `tool_loop.max_iterations` and without adding a disguised hard cap.
- Add focused regression tests for the failed-control-gate flow, strict state transitions, append-only persistence, migration, and progress visibility.

## Non-goals

- Do not lower the global `tool_loop.max_iterations` default or use iteration limits as the fix.
- Do not remove `[c]hat about this` or denial-with-message steering for ordinary non-session-control tools.
- Do not implement a semantic yield/watchdog policy that forcibly stops normal turns after an arbitrary tool count.
- Do not treat all X-tier tools as session-control tools; scope the special terminal behavior to named session-control capabilities.
- Do not keep a halfway autonomy ledger model that still overwrites previous runs after completion.

## Constraints

- Session-control tools must remain easy to recognize in both CLI rendering and server/tool-loop behavior.
- Server-side enforcement must protect non-CLI clients and stale clients, not just the current CLI.
- Ordinary tool redirect behavior must remain unchanged: a user can still deny an ordinary tool with a message and get an in-turn model response.
- Autonomous state-changing tools must require a conversation id and a conversation store. Missing either is an invalid invocation.
- An autonomous run state transition must be validated against the current active run state before mutation.
- Append-only ledger migration must preserve old rows as historical run records.
- The database must enforce only one active run per conversation for states `running` and `review_pending`.
- Profile rehydration must derive from the active autonomous run, not from stale completed or abandoned history.
- UI progress work must be presentation-oriented and must not alter the tool-loop execution contract.

## Decisions

### 1. Session-control steering gets both CLI and server guardrails

Chosen option: **Both CLI and server guardrails**.

Session-control prompts should not accept free-text steering in the CLI, and the server/tool-loop must also guard against clients that still send denial-with-message for session-control tools.

| Axis | CLI-only: remove chat/steer from session-control prompts | Server-only: make session-control FollowUpDenial terminal | Both CLI and server guardrails |
|---|---|---|---|
| Cost | Low: small CLI confirm handling and tests. | Low to medium: server predicate and tool-loop tests. | Medium: touches CLI, server predicate, tool-loop tests, and confirm tests. |
| Risk | Protects the main CLI but leaves headless, stale, or future clients able to send denial-with-message for control gates. | Protects all clients, but the CLI can still present misleading UX where typed `approved` appears accepted as natural language before the server stops. | Lowest runtime risk: prevents accidental steering in the primary UI and protects all clients at the server boundary. |
| Reward | Fixes the visible user-input ambiguity. | Fixes the authority boundary. | Fixes both cause layers: ambiguous input and unsafe continuation. |
| Side effects | Removes `[c]hat about this` only for session-control prompts. | Free-text redirect on a session-control prompt no longer gets an immediate model answer in the same turn. | Same side effects as both individual fixes, aligned around explicit `y/n/d` control gates. |
| Hack flags | None if limited to the existing session-control concept. | Possible duplication if the server grows an unclear separate list. | None if the predicate is clear and tests pin the set. |
| Best reason | Fastest visible UX fix. | Strongest minimum safety fix. | Correct layered safety. |
| Main drawback | Incomplete protection. | UX remains confusing. | More files and tests touched. |

Strongest case for the rejected options: CLI-only is the smallest visible fix if only the official CLI matters; server-only correctly puts runtime safety in the server. Both are incomplete alone.

### 2. Session-control execution errors terminate the current turn

Chosen option: **Terminal for any session-control execution error**.

If a session-control tool executes and returns an error, the tool loop should append the tool result and end the current turn. The model should not be allowed to self-correct or pivot into implementation in the same stream after a failed mode/state transition.

| Axis | Allow self-correction like ordinary tools | Terminal only for state/permission errors | Terminal for any session-control execution error |
|---|---|---|---|
| Cost | Low. | Medium: requires typed/classified errors. | Low to medium: reuse the session-control predicate. |
| Risk | High: failed mode transitions can still be followed by more tool calls. | Medium: classification can be wrong or brittle. | Low for control safety; medium ergonomic cost for retrying validation mistakes. |
| Reward | Preserves ordinary model self-repair behavior. | Balances safety and convenience if classification is reliable. | Makes session-control tools true boundaries. |
| Side effects | Leaves the unsafe continuation path open. | Adds a distinction future capability authors must maintain. | Some failed prompts require a fresh turn to retry. |
| Hack flags | Treats control-state tools as ordinary operational tools. | String-based classification would be a hack. | None if scoped to session-control tools. |
| Best reason | Lowest churn. | Best theoretical ergonomics. | Best semantic correctness. |
| Main drawback | Does not close the hole. | Complexity and brittleness. | Less same-turn auto-repair. |

Strongest case for the rejected options: same-turn self-correction is convenient for malformed arguments, and typed state-error classification could be ideal if the code already had that taxonomy. It does not today, and session-control safety is more important.

### 3. Autonomous state validation is strict everywhere

Chosen option: **Strict everywhere for autonomous state-changing tools**.

Any capability that mutates or depends on autonomous run state must require a conversation id and a conversation store. Missing either is an error, not a compatibility no-op.

| Axis | Keep permissive/no-op behavior | Strict only when store + conversation id exist | Strict everywhere for autonomous state tools |
|---|---|---|---|
| Cost | Low. | Medium. | Medium: add helpers and update tests to provide real state or expect errors. |
| Risk | High: stale or missing state can silently pass. | Medium: invalid synthetic calls can still hide bugs. | Low: invalid invocation fails loudly. |
| Reward | Compatibility. | Practical production safety. | Clean invariant: autonomous state tools cannot pretend to work without state. |
| Side effects | Leaves correctness holes. | Leaves an escape hatch. | Test harnesses must be explicit. |
| Hack flags | Silent no-op control mutation is a hack. | Compatibility compromise. | None. |
| Best reason | No churn. | Lower blast radius. | Correct semantic boundary. |
| Main drawback | Does not fix state integrity. | Not strict enough. | More test updates. |

State-machine rules:

| Operation | Required prior state | Resulting state |
|---|---:|---:|
| `suggest_autonomous` / `request_autonomous_execution` approval path | no active run | `running` |
| `capture_decision` | `running` | `running` with appended decision |
| `request_autonomous_exit` | `running` | `review_pending` |
| `complete_autonomous_review` | `review_pending` | `completed` |
| `auto_exit` | `running` or `review_pending` | `abandoned` |

Invalid states must return clear errors.

### 4. The autonomy ledger becomes append-only per run

Chosen option: **Create a clean append-only schema with one active-run invariant**.

The current one-row-per-conversation ledger must be replaced rather than hardened as a halfway step.

| Axis | Keep one-row active ledger for now | Implement append-only per-run ledger now |
|---|---|---|
| Safety against failed gate continuation | Still requires CLI/tool-loop fix. | Still requires CLI/tool-loop fix. |
| Active-state correctness | Can be improved but remains tied to a temporary model. | Cleaner: active run is explicit and validated. |
| Historical audit | Poor: later runs overwrite earlier completed/abandoned data. | Strong: each autonomous run remains inspectable. |
| Persistence/API cost | Medium-low. | High: migration, store API changes, active-run lookup, tests. |
| Long-term architecture | Temporary compromise. | Correct final shape. |
| Risk | Lower short-term blast radius. | Higher migration/regression risk, but better final model. |
| Best reason | Faster immediate safety patch. | Avoid building new semantics on a known-wrong data model. |
| Main drawback | A known halfway step. | Larger effort. |

The append-only schema should look like:

```sql
autonomy_runs
  run_id TEXT PRIMARY KEY
  conversation_id TEXT NOT NULL
  state TEXT NOT NULL
  source_kind TEXT NOT NULL
  brief_json TEXT NOT NULL
  decisions_json TEXT NOT NULL
  review_json TEXT NOT NULL
  created_at INTEGER NOT NULL
  updated_at INTEGER NOT NULL
```

Required indexes:

```sql
CREATE INDEX autonomy_runs_conversation_updated_idx
  ON autonomy_runs(conversation_id, updated_at DESC);

CREATE UNIQUE INDEX autonomy_runs_one_active_idx
  ON autonomy_runs(conversation_id)
  WHERE state IN ('running', 'review_pending');
```

Store API should distinguish creation, active-run lookup, and updates by run id. Existing one-row data should migrate into one historical run per conversation. If an old row is `running` or `review_pending`, it remains active. If it is `completed` or `abandoned`, it remains history and does not block a new run.

### 5. Long ordinary tool turns get UI progress visibility, not forced yield

Chosen option: **UI-only progress visibility as a final phase**.

The `MOAR UX` case is not autonomous leakage. It should be addressed by improving high-level visibility for long tool-heavy turns using existing tool lifecycle events, without changing execution semantics.

| Axis | Leave long-turn UX out of this effort | UI-only progress visibility | Enforced semantic yield/checkpoint |
|---|---|---|---|
| Cost | Low. | Medium: CLI state/render tests around tool count/current tool/elapsed or recent activity. | High: runner/tool-loop/watchdog policy and boundary tests. |
| Risk | Low implementation risk, but leaves opacity unresolved. | Low to medium: display-only changes should not affect execution semantics. | Medium-high: can become a disguised tool cap or interrupt valid atomic work. |
| Reward | Keeps safety work focused. | Improves perceived control without semantic risk. | Gives actual pause/control boundaries. |
| Side effects | Users still see long turns with limited status. | Better live feedback; the agent still continues until it chooses to stop. | Requires new rules about when the agent pauses. |
| Hack flags | Not a hack if documented as non-goal/follow-up. | None if presentation-only. | Hack risk if implemented as arbitrary N-tool stopping. |
| Best reason | Avoid scope creep. | Addresses the observation safely. | Solves deeper semantic-yield issue. |
| Main drawback | Does not address one motivating observation. | Visibility only. | Too much policy design for this effort. |

A future effort may design semantic yield/watchdog behavior around solved units and checkpoint boundaries, but this effort should not introduce arbitrary forced yields.
