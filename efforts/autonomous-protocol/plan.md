# Plan: Autonomous Protocol

Implement autonomous mode as a planning-mode-like conversation profile with a durable per-conversation autonomy ledger, lightweight approved run brief, structured design-decision logging, and final decision review.

Each phase should keep server and CLI builds green. Prefer targeted tests first, then broader module tests after interface changes.

## Phase 0 — Grounding and schema design

- [ ] Confirm the exact profile/session-control path from planning mode:
  - profile broker;
  - session profile change event;
  - slash command;
  - built-in tools;
  - CLI pending confirmation title/details;
  - mode chip rendering.
- [ ] Confirm conversation store migration conventions and how `conversations.db` schema changes are handled.
- [ ] Choose the ledger storage shape during implementation, biased toward the smallest durable structure that supports this effort:
  - active brief;
  - brief revisions;
  - run state;
  - captured decisions;
  - final review state.
- [ ] Keep the ledger conversation-scoped. Do not introduce a global job manager.

Expected tests:

- Unit-level store tests for creating, reading, and updating autonomy ledger data.

## Phase 1 — Add autonomous profile and status chip

- [ ] Add an autonomous profile alongside the existing planning profile in the server profile broker.
- [ ] Add autonomous posture text to the active profile context:
  - act against the approved brief;
  - continue without asking for normal in-scope decisions;
  - use the design-decision protocol for meaningful forks;
  - call `capture_decision` before committing to meaningful in-scope choices;
  - stop only for high-risk boundary decisions;
  - request final review when done.
- [ ] Extend session profile change plumbing so the CLI receives and stores `autonomous` just like `plan`.
- [ ] Update `renderPermissionModeChip` to render `mode: autonomous | <permission-mode>`.
- [ ] Keep planning chip behavior unchanged.

Expected tests:

- Server profile tests for `autonomous` profile activation/deactivation.
- CLI rendering test for autonomous chip with strict/permissive/bypass.
- Regression test that planning chip still renders as before.

## Phase 2 — Add autonomous session-control tools and `/auto`

Add parallel tools instead of overloading planning tools.

- [ ] Add `suggest_autonomous` built-in:
  - arguments: reason, brief fields (`goal`, `done_when`, `constraints`, `review_points`), optional source plan/spec paths;
  - X-tier/session-control confirmation;
  - on approval, create/update the autonomy ledger and set profile to `autonomous`.
- [ ] Add `auto_exit` built-in:
  - exits autonomous mode without successful completion;
  - marks ledger state as abandoned/cancelled;
  - preserves decisions and brief history.
- [ ] Add `request_autonomous_exit` built-in:
  - used when the agent believes the run is complete;
  - starts final review/exit confirmation rather than immediately dropping the profile.
- [ ] Add CLI slash command `/auto [goal]`:
  - with a goal, submit a user turn that causes the assistant to draft a brief and call `suggest_autonomous`, or directly call the server if that matches existing slash architecture;
  - without a goal, ask the model to draft/ask for a brief.
- [ ] Extend CLI session-control prompt helpers:
  - `suggest_autonomous`: `Start autonomous mode with this run brief?`
  - `request_autonomous_exit`: `Autonomous run complete — review decisions and exit autonomous mode?`
  - `auto_exit`: `Leave autonomous mode?`
- [ ] Ensure `y/n/d/c` behavior matches planning mode:
  - yes applies transition;
  - no declines/drops;
  - details shows brief/summary;
  - chat/typed steering lets the user adjust the brief before approval.

Expected tests:

- Built-in tool tests for approved/denied autonomous start.
- CLI confirmation title/details tests for autonomous tools.
- `/auto` slash command parsing tests.
- Regression tests that planning session-control prompts are unchanged.

## Phase 3 — Persist autonomy ledger and brief revisions

- [ ] Add durable ledger storage attached to conversation ID.
- [ ] Store active run brief:
  - `goal`;
  - `done_when`;
  - `constraints`;
  - `review_points`.
- [ ] Store append-only brief revisions:
  - revision number;
  - actor;
  - timestamp;
  - reason;
  - full replacement brief.
- [ ] Store run state:
  - proposed;
  - running;
  - review_pending;
  - completed;
  - abandoned;
  - blocked, if needed.
- [ ] Preserve optional source references for plan bridge:
  - source kind;
  - plan path;
  - spec path;
  - source conversation ID if useful.
- [ ] On reconnect/resume, restore enough state for the profile chip and final-review flow to be correct.

Expected tests:

- Create ledger on autonomous start.
- Revise brief and verify revision history is append-only.
- Resume conversation and recover active autonomous state.
- Abandon run preserves ledger.

## Phase 4 — Add structured decision capture

- [ ] Add `capture_decision` built-in, available/encouraged in autonomous mode.
- [ ] Require compact design-decision-protocol fields:
  - `decision_point`;
  - `options[]` with title, cost, risk, reward, side effects, hack flags;
  - `counterarguments[]` for non-chosen options;
  - `recommendation`;
  - `chosen_path`;
  - `why_cleanest`;
  - `reversibility` (`easy`, `moderate`, `hard`, `effectively_irreversible`);
  - `stop_required` and optional `stop_reason`.
- [ ] Persist decision entries in the autonomy ledger with sequence number and timestamp.
- [ ] Emit a lightweight stream/progress event if there is an existing good event path; otherwise keep it ledger-only for V1.
- [ ] Update autonomous profile text so meaningful forks require using the design-decision protocol and `capture_decision` before proceeding.
- [ ] Make the protocol clear that ordinary low-level implementation choices do not need logging; meaningful forks do.

Expected tests:

- Tool schema validation rejects incomplete decision entries.
- Captured decisions persist and read back in order.
- Decision entries preserve hack flags/counterarguments/reversibility.

## Phase 5 — Planning-mode bridge

- [ ] After successful `request_plan_approval`, allow the assistant to offer autonomous execution by calling `suggest_autonomous`.
- [ ] Update planning/executing profile instructions to make this bridge explicit:
  - normal execution remains valid;
  - autonomous execution is optional;
  - assistant should derive a concise brief from accepted `spec.md` / `plan.md` when appropriate.
- [ ] Avoid overloading `request_plan_approval`; it should continue to mean leaving planning mode and starting execution under human approval.
- [ ] Ensure a plan-derived brief is still user-approved before autonomous mode starts.

Expected tests:

- Prompt/protocol tests or golden text tests covering the bridge instruction.
- Session-control flow test showing plan approval followed by autonomous suggestion.
- Regression test that plan approval without autonomous suggestion behaves as before.

## Phase 6 — Final decision review and autonomous exit

- [ ] Implement final-review state after `request_autonomous_exit` approval.
- [ ] Present captured decisions one by one, in order, with enough detail for review:
  - decision point;
  - chosen path;
  - cleanest-option rationale;
  - hack flags, if any;
  - reversibility;
  - options/counterarguments available via details.
- [ ] Support outcomes:
  - accept decision;
  - ask for details;
  - ask the agent to revise work based on changing a decision;
  - accept all remaining, if this fits existing UI style without adding too much surface.
- [ ] When review completes, mark ledger completed and exit the autonomous profile.
- [ ] If the user asks to revise a decision, keep autonomous mode active and have the agent update work; capture a new decision/revision if needed.

Expected tests:

- Completed autonomous run enters review-pending state.
- Review all decisions then exits autonomous profile.
- Asking to revise keeps profile active and does not mark completed.
- No-decision run can still complete and exit cleanly.

## Phase 7 — Autonomous protocol catalog and guardrails

- [ ] Add a built-in `autonomous-mode` or `autonomous-run` protocol document if the profile text should remain compact.
- [ ] Ensure active autonomous posture says:
  - work to the approved brief;
  - correctness beats cleanliness beats future cost;
  - delegate mechanical work;
  - verify at the right tier;
  - use design-decision protocol for meaningful forks;
  - log and continue for in-scope reversible/bounded decisions;
  - stop only for high-risk boundaries;
  - do not push/merge;
  - checkpoint only if existing checkpoint tooling and user policy allow it.
- [ ] Add tests locking the high-risk threshold language so it does not drift into asking for every decision.

Expected tests:

- Protocol catalog test includes autonomous protocol.
- Steering/profile text test includes decision logging and high-risk stop threshold.

## Phase 8 — Integration pass

- [ ] Run targeted server tests for profile, capabilities, conversation store, tool loop, and protocol catalog.
- [ ] Run targeted CLI tests for slash command, confirmation prompts, status chip, and review UI.
- [ ] Run module-level `go test ./...` in server and CLI if interfaces/protobuf/store schema changed broadly.
- [ ] Manually exercise or add an integration test for:
  - `/auto <goal>`;
  - approve brief;
  - capture a decision;
  - request exit;
  - review decision;
  - exit autonomous mode.

## Open implementation notes

- Keep the kickoff brief small. If the generated brief feels like a plan, it is too heavy.
- Prefer adding autonomous-specific tools over making planning tools polymorphic.
- Do not add job lifecycle now. The durable ledger should be designed so a future job manager can wrap it later.
- The final review does not have to solve rich diff/rollback UX in V1. It only needs to let the user inspect decisions and ask the agent to revise if needed.
- If implementation discovers that final review requires more UI design than expected, keep the first version transcript-driven but backed by the structured ledger.
