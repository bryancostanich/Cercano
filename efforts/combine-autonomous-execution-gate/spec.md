# Combine Autonomous Execution Gate

## Problem / motivation

After a plan is approved, Cercano currently asks two consecutive `y/n/d/c` questions for what is effectively one decision:

1. `request_autonomous_execution` asks whether the approved plan should be executed autonomously.
2. `suggest_autonomous` immediately asks whether to start autonomous mode with the run brief.

The screenshot that triggered this effort shows both prompts stacked in the same flow: "Plan approved. Would you like me to execute it to completion autonomously?" followed by "Start autonomous mode with this run brief?". This is redundant and makes the approval model feel noisy even though the user is approving one thing: execute this approved plan autonomously under this brief.

The current code mirrors that redundancy. `request_plan_approval` instructs the model to call `request_autonomous_execution`; `request_autonomous_execution` is an X-tier gate whose `Execute` only tells the model to draft a brief and call `suggest_autonomous`; `suggest_autonomous` is then the second X-tier gate that saves the autonomy ledger and enters the `autonomous` profile.

## Goals

- Replace the two-step post-plan autonomous prompt with one combined prompt: "Plan approved. Execute it autonomously with this run brief?"
- Show the user the actual autonomous brief at the single approval boundary: reason, goal, done-when checklist, constraints, review points, and source plan/spec paths.
- On approval, save the autonomy run ledger and enter the `autonomous` profile immediately from `request_autonomous_execution`.
- Keep `suggest_autonomous` available for direct autonomous requests, such as `/auto`, where there is no prior approved plan.
- Update protocol guidance and tests so agents no longer call `suggest_autonomous` after `request_autonomous_execution` for approved-plan flows.

## Non-goals

- Do not remove `suggest_autonomous`; it remains the direct autonomous-entry capability.
- Do not change the final autonomous exit/review flow.
- Do not change the `request_plan_approval` gate itself beyond its follow-up instruction text.
- Do not introduce a hidden implicit approval of `suggest_autonomous` after another gate; the single prompt must be explicit and must show the actual brief.

## Constraints

- The combined prompt must remain X-tier and use the existing `y/n/d/c` confirmation gate.
- The user must approve the actual autonomous brief before the session enters the `autonomous` profile.
- The autonomy ledger behavior currently implemented in `suggest_autonomous` must be preserved for approved-plan autonomous runs.
- The UI prompt must stay non-destructive in tone and must not show raw JSON arguments.
- `source_plan_path` and `source_spec_path` must continue to identify accepted-plan autonomous runs.

## Decisions

### Decision 1 — Collapse approved-plan autonomous execution into `request_autonomous_execution`

Chosen option: **Single combined autonomous-execution gate**.

This option makes `request_autonomous_execution` the canonical approved-plan autonomous handoff. The capability will accept the run-brief fields that `suggest_autonomous` already accepts (`reason`, `goal`, `done_when`, `constraints`, `review_points`) plus the approved-plan source fields (`effort`, `summary`, `plan_path`, `spec_path`). Its X-tier confirmation prompt will show both the plan context and the brief. If approved, `Execute` will save the autonomy ledger and enter the `autonomous` profile directly.

| Decision axis | Single combined autonomous-execution gate | Auto-accept second gate after first approval | Keep two gates but improve wording |
|---|---|---|---|
| User experience | Best: one prompt contains the execution question and the run brief. | Looks convenient but is surprising because the second approval becomes implicit. | Slightly clearer wording, but still asks two questions for one decision. |
| Code shape | Clean: one capability owns approved-plan autonomous entry, while `suggest_autonomous` remains for direct requests. | Hacky: requires hidden continuation state saying the next `suggest_autonomous` is pre-approved. | Smallest implementation but leaves the duplicated protocol intact. |
| Safety | Strong: the user sees and approves the actual autonomous brief before profile entry. | Weaker: safety depends on guaranteeing the brief was visible at the earlier prompt. | Strong but noisy: safety is preserved through redundant approvals. |
| Protocol clarity | Strong: after plan approval, one optional autonomous bridge asks for execution style and brief approval together. | Muddy: two tools still exist, but one sometimes silently stands in for the other. | Clear mechanically, but semantically redundant. |
| Side effects | Requires capability schema, execution, UI prompt, protocol text, and tests to change together. | Adds coupling between two gates and risks future permission bugs. | Low code churn but does not solve the UX problem. |
| Main drawback | More implementation work than wording-only cleanup. | Hidden approval coupling is a hack and weakens explicit consent. | Fails the goal of removing duplicate prompts. |

The strongest argument against the chosen option is that it duplicates some logic from `suggest_autonomous`, especially autonomy ledger persistence and profile entry. The cleaner implementation response is to extract or share the small ledger/profile-entry helper rather than keep two separate copies. The semantic boundary remains correct: `request_autonomous_execution` is for approved-plan autonomous entry; `suggest_autonomous` is for direct autonomous entry.
