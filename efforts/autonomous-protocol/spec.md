# Autonomous Protocol

## Problem / motivation

Cercano already has most of the autonomy substrate: native tool loops, permission modes, sub-agent dispatch, persistence/resume, live attach, headless `cercano run --auto-allow`, built-in protocols, and adversarial review. What is missing is the productized autonomous run protocol: a lightweight, explicit mode that a user or model can enter/exit, that gives the agent a goal and boundaries, lets it make clean in-scope decisions without constant interruption, records those decisions for later review, and returns control to the user through a deliberate final review.

The target interaction should feel like planning mode:

- user can initiate or exit;
- model can suggest initiating or exiting;
- transitions go through the existing `y/n/d/c` session-control confirmation style;
- the status bar shows a mode chip;
- the active profile changes the model's posture;
- the mode can be left deliberately rather than being just a long assistant response.

Autonomous mode differs from planning mode in one crucial way: after the user approves a lightweight run brief, the agent should proceed. It should use its tools, delegate mechanical work, verify, fix failures, and make smart in-scope decisions without stopping for every design fork. Meaningful decisions are captured for end-of-run review. Mid-run interruption is reserved for very high-risk choices or scope/security/destructive boundaries.

## Goals

- Add an `autonomous` per-conversation profile, parallel to planning mode, surfaced in the CLI status chip as `mode: autonomous | <permission-mode>`.
- Add autonomous session-control flows that mirror planning mode:
  - user slash command / natural-language request can initiate;
  - assistant can suggest autonomous mode;
  - assistant can request autonomous exit/final review;
  - user/model can abandon autonomous mode.
- Start each autonomous run from a lightweight approved run brief:
  - `goal`
  - `done_when`
  - `constraints`
  - `review_points`
- Support starting autonomous mode from an accepted planning-mode plan by deriving a run brief from `spec.md` / `plan.md` and asking the user to approve it.
- Persist an autonomy ledger attached to the conversation, including active brief, brief revisions, run state, captured decisions, progress/completion metadata, and final-review status.
- Add a structured decision capture path that forces the design-decision protocol shape for meaningful forks.
- In autonomous mode, the agent should decide and continue for normal in-scope forks, then walk the user through captured decisions at the end.
- Stop mid-run only for high-risk forks: effectively irreversible choices, choices that would invalidate most downstream work if wrong, scope expansion, security/permission/data-loss semantics, destructive operations, push/merge/migration/user-data changes, or cases where the agent cannot honestly identify a clean preferred option.

## Non-goals

- Do not build a separate durable background job manager in this effort.
- Do not add full pause/resume/cancel job lifecycle yet.
- Do not make every design decision a human approval prompt.
- Do not create a heavy objective-document workflow. The kickoff brief must stay easy to review and approve.
- Do not replace existing planning mode; autonomous mode should bridge from it when useful.
- Do not loosen existing tool permission semantics. Permission mode remains orthogonal to autonomous mode.
- Do not push or merge automatically.

## Existing implementation to reuse

- `source/server/internal/agent/profile.go`: existing profile broker and planning profile pattern.
- `source/server/internal/capabilities/builtins/suggest_plan.go`, `request_plan_approval.go`, `plan_exit.go`: session-control tool pattern.
- `source/clients/cli/internal/slash/plan.go`: slash command pattern for profile transition.
- `source/clients/cli/internal/ui/model.go`: planning tool prompt titles/details, pending confirmation routing, session profile state.
- `source/clients/cli/internal/ui/model.go` `renderPermissionModeChip`: status chip already combines profile and permission mode.
- `source/server/internal/agent/toolloop.go`: native tool loop and tool/result persistence hooks.
- `source/server/internal/dispatch/engine.go` and `source/server/internal/hostsvc/tools/tools.go`: sub-agent delegation and grant narrowing.
- `source/server/internal/conversation/store.go`: conversation/sub-agent persistence patterns.
- `source/server/internal/server/attach_test.go`: live attach/replay behavior.
- `source/server/internal/protocols/`: built-in protocol catalog.
- `source/server/internal/capabilities/builtins/review.go`: adversarial review primitive.

## Decisions

### 1. Architecture: hybrid profile + durable autonomy ledger

Autonomous mode is a per-conversation profile for live behavior, plus a small durable autonomy ledger attached to the conversation. The profile drives prompt posture, status chip, and tool/profile fences. The ledger stores the run brief, revisions, state, decisions, and review status.

Rejected alternatives:

- Profile only: too ephemeral for explicit goals and later decision review.
- Full job object now: eventual direction for unattended background runs, but too much machinery before the protocol is proven.

### 2. Goal representation: lightweight run brief + revision history

The kickoff artifact is a small brief, not a full planning document:

```yaml
goal: string
done_when:
  - string
constraints:
  - string
review_points:
  - string
```

The run brief is generated by the assistant from either a direct user request or an accepted planning-mode plan, then approved by the user. Changes are append-only revisions with actor, timestamp, reason, and replacement brief.

### 3. Decision capture: structured decision log, not transcript-only notes

Meaningful autonomous forks are recorded in a structured ledger entry, not just prose. The agent should continue for in-scope decisions and defer human review to the end.

### 4. Decision discipline: force the design-decision protocol shape

The decision capture tool must require compact fields that force the design-decision protocol:

- decision point;
- real options;
- cost, risk, reward, and side effects for each option;
- explicit hack flags;
- strongest counterargument for non-chosen options;
- recommendation/chosen path;
- why it is the cleanest option;
- reversibility/high-risk classification.

The autonomous profile must instruct the agent that meaningful in-scope forks require this protocol before choosing. The agent should not ask the user for normal reversible or bounded decisions; it should log and continue. It stops only when the decision is effectively irreversible, scope/security/destructive, or likely to invalidate most downstream work if wrong.

### 5. Initiation and exit: parallel autonomous tools

Add autonomous session-control tools and slash command instead of overloading planning tools:

- `/auto [goal]`
- `suggest_autonomous`
- `request_autonomous_exit`
- `auto_exit`
- `capture_decision`

Planning mode can bridge into autonomous mode by having the assistant call `suggest_autonomous` with a brief derived from accepted planning artifacts after `request_plan_approval` succeeds.

### 6. Lifecycle: autonomous phase loop inside the current conversation

Do not implement a separate job system yet. Once approved, the conversation enters `autonomous` profile, the ledger state becomes `running`, and the assistant follows the autonomous protocol until complete, blocked, abandoned, or ready for final review. The run can span steering/reconnect/resume because the ledger is durable and attached to the conversation.

## Desired user flows

### Direct user start

1. User types `/auto fix the reconnect approval bug` or asks naturally to work autonomously.
2. Assistant drafts a short run brief.
3. Assistant calls `suggest_autonomous`.
4. CLI shows: `Start autonomous mode with this run brief? [y] yes [n] no [d] details [c] chat`.
5. On `y`, server creates/updates autonomy ledger, sets profile to `autonomous`, and starts/continues the run.

### Model-initiated start

1. Assistant recognizes a task is broad/multi-step or the user requested hands-off execution.
2. Assistant calls `suggest_autonomous` with a draft brief and reason.
3. User approves/declines/steers through the same confirmation path.

### Planning bridge

1. User approves a planning-mode plan with `request_plan_approval`.
2. Assistant may offer autonomous execution.
3. Assistant drafts a brief from `spec.md` / `plan.md` and calls `suggest_autonomous`.
4. User approves the brief; autonomous mode starts.

### Normal autonomous work

1. Agent inspects state and works against the active brief.
2. Agent delegates mechanical tasks to sub-agents when appropriate.
3. Agent uses the design-decision protocol for meaningful forks and calls `capture_decision`.
4. Agent verifies at the right test tier, fixes failures, and continues.
5. Agent stops only for high-risk boundary decisions or blockers.

### Successful exit / final review

1. Agent concludes the brief is satisfied.
2. Agent calls `request_autonomous_exit` with summary and verification result.
3. CLI prompts: `Autonomous run complete — review decisions and exit autonomous mode? [y] review/exit [n] continue [d] details [c] chat`.
4. On approval, the agent walks through captured decisions one by one.
5. User accepts, asks for revisions, or sends the agent back to adjust work.
6. After review completes, profile exits autonomous mode.

### Abandon exit

`auto_exit` leaves autonomous mode without successful completion, preserving the ledger and noting that the run was abandoned.

## Acceptance criteria

- A user can start autonomous mode with `/auto <goal>` and approve a generated lightweight brief through the standard confirmation UI.
- The assistant can suggest autonomous mode by calling a tool, and the CLI renders a human-friendly session-control prompt.
- The status bar shows `autonomous` while the profile is active and combines it with strict/permissive/bypass permission mode.
- An accepted planning-mode plan can be followed by an autonomous-mode offer using a brief derived from the plan.
- The active brief and revisions are persisted and survive reconnect/resume.
- `capture_decision` persists structured decisions that include design-decision-protocol fields.
- The autonomous profile instructs the model to log meaningful in-scope decisions and continue, stopping only for high-risk boundary cases.
- Completing a run prompts for final decision review before exiting autonomous mode.
- Abandoning a run exits the profile and records the abandoned state.
- Existing planning-mode behavior remains unchanged.
