# Autonomous Control and Progress Guards

Harden session-control prompt semantics, autonomous state management, append-only autonomy persistence, and long-turn progress visibility. The work intentionally fixes the control/state causes instead of reducing the global tool-loop iteration cap.

## Phase 1 — Session-control classification and terminal tool-loop semantics

Objective: introduce one server-side session-control predicate and use it to stop the current tool loop when a session-control gate is redirected, denied-with-message, or returns an execution error.
Files: `source/server/internal/agent/profile.go` or a nearby new helper file, `source/server/internal/agent/toolloop.go`, `source/server/internal/agent/*test.go`.
Tests: regression where `request_autonomous_execution` receives `FollowUpDenial` and the scripted next model response tries to call another tool; assert the second model/tool step does not run. Regression where a session-control tool returns an execution error; assert the current turn ends. Regression that ordinary tool `FollowUpDenial` still continues and produces an in-turn response.

- [x] Add a named server-side session-control predicate for mode/session handoff tools
- [x] Make `FollowUpDenial` terminal for session-control tools in the tool loop
- [x] Make execution errors terminal for session-control tools in the tool loop
- [x] Preserve current ordinary-tool denial-with-message continuation behavior
- [x] Add focused tool-loop regression tests for session-control redirect termination
- [x] Add focused tool-loop regression tests for session-control execution-error termination
- [x] Add focused regression coverage proving ordinary tool steering still works

## Phase 2 — CLI session-control prompts are explicit gates

Objective: remove free-text steering and `[c]hat about this` from session-control confirmation prompts while preserving ordinary tool chat/steer behavior.
Files: `source/clients/cli/internal/ui/model.go`, `source/clients/cli/internal/ui/confirm_test.go` or related UI tests.
Tests: session-control confirm prompt does not render `[c]hat`; typed free text while a session-control prompt is pending does not call `DenyToolCallWithMessage` and instead shows a local instruction to press `y`, `n`, or `d`; ordinary tool prompts still support `[c]hat` and steering.

- [x] Remove `[c]hat about this` action from session-control prompt construction
- [x] Block free-text `steerPendingConfirm` for session-control prompts
- [x] Show a clear local message explaining that session-control prompts require `y`, `n`, or `d`
- [x] Preserve slash-command handling while a session-control prompt is pending
- [x] Preserve free-text steering for ordinary tool confirmations
- [x] Add UI tests for session-control prompt actions and typed-text behavior
- [x] Add UI tests confirming ordinary tool steering is unchanged

## Phase 3 — Append-only autonomy ledger schema and store API

Objective: replace one-row-per-conversation upsert semantics with append-only per-run records and one active-run invariant.
Files: `source/server/internal/conversation/autonomy.go`, `source/server/internal/conversation/store.go`, conversation migration/schema files if separate, `source/server/internal/conversation/*test.go`.
Tests: migration preserves existing rows as historical runs; creating multiple completed/abandoned runs for one conversation is allowed; creating a second `running` or `review_pending` run fails; active-run lookup returns only `running` or `review_pending`; latest-run/list behavior is deterministic; old one-row data is copied with decisions/review/brief intact.

- [x] Inspect existing conversation schema/migration mechanism and identify where to add the autonomy migration
- [x] Add `run_id` to the autonomy run model
- [x] Implement append-only schema migration for `autonomy_runs`
- [x] Add index for deterministic latest/list lookup by conversation and updated time
- [x] Add unique active-run index for `running` and `review_pending` states per conversation
- [x] Replace upsert-style save with explicit create/update operations
- [x] Add `CreateAutonomyRun` store method
- [x] Add `GetActiveAutonomyRun` store method
- [x] Add update-by-run-id store support for state, decisions, and review fields
- [x] Add latest/list helper only if required by existing callers or tests
- [x] Update existing store callers to use active-run semantics instead of one-row semantics
- [x] Add migration and store tests for historical preservation and active-run uniqueness

## Phase 4 — Strict autonomous state-machine enforcement

Objective: enforce strict preconditions on all autonomous state-changing capabilities using the append-only active-run API.
Files: `source/server/internal/capabilities/builtins/autonomous.go`, `source/server/internal/capabilities/builtins/autonomous_execution_choice.go`, `source/server/internal/capabilities/builtins/capture_decision.go`, related tests.
Tests: missing conversation id/store errors; enter rejects when an active run exists; enter creates a new run when no active run exists even if historical completed/abandoned runs exist; `capture_decision` requires `running`; `request_autonomous_exit` requires `running` and transitions to `review_pending`; `complete_autonomous_review` requires `review_pending` and transitions to `completed`; `auto_exit` requires `running` or `review_pending` and transitions to `abandoned`; invalid transitions leave state unchanged.

- [x] Add shared helpers for requiring conversation id and conversation store
- [x] Add shared helper for requiring active autonomous run in specific states
- [x] Update autonomous entry to create a new append-only run and reject existing active runs
- [x] Update `capture_decision` to require active `running` run and update by run id
- [x] Update `request_autonomous_exit` to require active `running` run and update by run id
- [x] Update `complete_autonomous_review` to require active `review_pending` run and update by run id
- [x] Update `auto_exit` to require active `running` or `review_pending` run and update by run id
- [x] Ensure profile switching is ordered safely around durable state changes
- [x] Add strict-state tests for every valid and invalid transition
- [x] Update existing autonomous capability tests that assumed permissive no-op behavior

## Phase 5 — Profile rehydration and session-control integration audit

Objective: ensure session profile rehydration and control-tool semantics use the new active-run model consistently across direct server, worker, MCP, and CLI flows.
Files: `source/server/internal/server/server.go`, `source/server/internal/worker/*.go` as needed, `source/server/internal/capabilities/mcpadapter/*.go` if affected, related tests.
Tests: `GetSessionProfile` rehydrates autonomous only from an active `running` or `review_pending` run; completed/abandoned historical runs do not rehydrate autonomous; request-autonomous-execution rejection does not save a run or enter profile; request-autonomous-execution approval creates exactly one active run and enters profile; stale review-pending behavior is explicit and tested.

- [x] Update profile rehydration to call `GetActiveAutonomyRun`
- [x] Verify completed and abandoned historical runs do not activate autonomous profile
- [x] Verify `review_pending` remains autonomous unless a distinct future review profile is designed
- [x] Audit worker profile bridges for conversation id propagation under the new store API
- [x] Audit MCP/capability adapter paths for strict conversation id requirements
- [x] Add integration tests for approved autonomous entry creating active run and switching profile
- [x] Add integration tests for rejected/redirected autonomous entry leaving no active run and not continuing execution

## Phase 6 — Long ordinary-turn progress visibility

Objective: improve high-level UI visibility for long non-autonomous tool-heavy turns using existing stream events, without adding a hard cap or forced semantic yield.
Files: `source/clients/cli/internal/ui/model.go`, possible status/footer rendering files, UI tests.
Tests: footer/status includes useful current-tool and tool-count/progress information during tool-heavy turns; state clears at turn completion; ordinary event rendering remains stable; no runner/tool-loop execution semantics change.

- [x] Inventory existing tool lifecycle events consumed by the CLI
- [x] Add lightweight per-turn tool activity counters/state in the UI model
- [x] Render current tool plus useful progress context in the footer/status area
- [x] Clear/reset progress state when a turn completes or is interrupted
- [x] Add UI tests for progress state updates from tool start/complete events
- [x] Add UI tests for progress reset after completion
- [x] Confirm no changes were made to `tool_loop.max_iterations` or forced-yield semantics

## Phase 7 — Verification and cleanup

Objective: run targeted and then package-level tests, remove temporary compatibility code, and document follow-ups if any design issue remains.
Files: affected package tests and any effort notes.
Tests: targeted tests for agent tool loop, built-in capabilities, conversation store/migration, server session profile, worker routing if touched, and CLI UI; broader package tests for affected directories.

- [x] Run targeted agent/tool-loop tests
- [x] Run targeted autonomous capability tests
- [x] Run targeted conversation store/migration tests
- [x] Run targeted server session-profile/autonomous integration tests
- [x] Run targeted CLI UI tests
- [x] Run broader affected package tests
- [x] Inspect git diff for accidental tool-loop cap changes or unrelated edits
- [x] Update effort notes or follow-up list for semantic-yield/watchdog work if needed
- [ ] Commit the completed unit with a conventional checkpoint subject and explanatory body
