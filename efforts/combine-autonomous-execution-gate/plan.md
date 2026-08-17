# Combine Autonomous Execution Gate

## Phase 1 — Share autonomous entry implementation

Objective: Preserve existing `suggest_autonomous` behavior while making its ledger/profile-entry behavior reusable by the approved-plan gate.

Files to modify:
- `source/server/internal/capabilities/builtins/autonomous.go`
- `source/server/internal/capabilities/builtins/autonomous_test.go` or related builtins tests

Tests to write/run:
- Unit test helper behavior through existing `suggest_autonomous` tests.
- Verify direct autonomous entry still saves the ledger and enters the `autonomous` profile.

- [x] Extract a small helper for saving an autonomy run and entering the `autonomous` profile.
  - [ ] Preserve existing validation that a goal is required.
  - [ ] Preserve `sourceKind = "direct_user_request"` for direct `suggest_autonomous` calls.
  - [ ] Preserve `sourceKind = "accepted_plan"` when source plan/spec paths are provided.
- [x] Refactor `suggest_autonomous.Execute` to use the helper without changing direct `/auto` behavior.
- [x] Keep error messages clear when conversation storage or profile switching is unavailable.

## Phase 2 — Make `request_autonomous_execution` the combined approved-plan gate

Objective: Change `request_autonomous_execution` from a prompt that tells the model to call `suggest_autonomous` into the single approved-plan autonomous-entry gate.

Files to modify:
- `source/server/internal/capabilities/builtins/autonomous_execution_choice.go`
- `source/server/internal/capabilities/builtins/autonomous_execution_choice_test.go` or related builtins tests

Tests to write/run:
- Unit test that approval executes the helper, saves an accepted-plan autonomy run, and enters `autonomous`.
- Unit test that missing goal is rejected before profile entry.
- Unit test that source plan/spec paths are preserved.

- [x] Extend `request_autonomous_execution` args/schema with brief fields: `reason`, `goal`, `done_when`, `constraints`, and `review_points`.
- [x] Make `goal` required for approved-plan autonomous execution.
- [x] On Execute, call the shared autonomous-entry helper with `sourceKind = "accepted_plan"` semantics.
- [x] Return text that says autonomous mode has started for the approved plan; do not instruct the model to call `suggest_autonomous`.
- [x] Preserve effort, summary, spec path, and plan path in result text for traceability.

## Phase 3 — Update prompt rendering and protocol guidance

Objective: Ensure the user sees one calm, human-readable combined prompt and the model receives no instructions to trigger the second gate.

Files to modify:
- `source/clients/cli/internal/ui/model.go`
- `source/clients/cli/internal/ui/confirm_test.go`
- `source/server/internal/capabilities/builtins/request_plan_approval.go`
- `source/server/internal/protocols/catalog.go`
- Relevant protocol/catalog tests under `source/server/internal/protocols/`

Tests to write/run:
- UI confirm prompt test for `request_autonomous_execution` showing plan plus brief details and no raw JSON.
- Protocol/content tests proving approved-plan autonomous flow no longer says to call `suggest_autonomous` afterward.

- [x] Change the `request_autonomous_execution` prompt title to "Plan approved. Execute it autonomously with this run brief?".
- [x] Surface plan summary, goal, done-when, constraints, review points, spec path, and plan path in confirm details.
- [x] Update `request_plan_approval` result/description to tell the model to call `request_autonomous_execution` with the drafted brief, not to call `suggest_autonomous` afterward.
- [x] Update autonomous/planning/executing protocol text so approved-plan autonomous execution is one gate.
- [x] Keep `suggest_autonomous` wording for direct autonomous requests.

## Phase 4 — Verify and checkpoint

Objective: Prove the duplicate gate is removed without regressing direct autonomous entry or planning handoff behavior.

Files to modify:
- No new feature files unless tests reveal gaps.

Tests to write/run:
- Targeted builtins tests for autonomous capabilities.
- Targeted UI confirm prompt tests.
- Targeted protocol tests.
- Relevant package tests for capabilities, agent, runner, protocols, and CLI UI.

- [x] Run `go test ./source/server/internal/capabilities/builtins`.
- [x] Run targeted autonomous-related tests under `source/server/internal/capabilities/...`.
- [x] Run targeted confirm prompt tests under `source/clients/cli/internal/ui`.
- [x] Run targeted protocol tests under `source/server/internal/protocols`.
- [x] Run broader dependent package tests as needed.
- [x] Commit the completed implementation with a conventional-commit message.
