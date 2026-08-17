# Plan: debug task pane controls

## Status

- [x] 1. Locate slash/dev-mode/tool integration points
- [x] 2. Add a shared debug task-pane controller
- [x] 3. Add dev-mode-gated slash commands
- [x] 4. Expose matching agent-callable debug controls
- [x] 5. Add `/debug help` and `/d` hint
- [x] 6. Add tests
- [x] 7. Verify
- [x] 8. Checkpoint

## 1. Locate slash/dev-mode/tool integration points

Read the relevant code paths before editing:

- `source/clients/cli/internal/slash/` for command registration and parsing.
- `source/clients/cli/internal/ui/model.go` for slash result dispatch and `/d` handling.
- Existing dev-mode state/helpers, currently expected to be represented by the `/d` workdir override path.
- Existing tool exposure/agent-callable command path. Find the smallest existing pattern for a UI-local command exposed to the agent, if any.
- `source/clients/cli/internal/ui/task_pane.go` for `toggleTaskPane`, `applyTaskChange`, task storage, and render assumptions.

Output of this step should be a concrete list of files to edit and tests to extend.

## 2. Add a shared debug task-pane controller

Create a UI-local controller/helper layer that owns all debug task-pane mutations. Suggested shape:

- `debugTaskControlsEnabled() bool` on `Model`, gated on current `/d` dev-mode state.
- `runDebugTaskPaneCommand(args []string) (message string, err error)` or equivalent.
- Structured lower-level operations that can also be called by agent tools:
  - show
  - hide
  - toggle
  - clear
  - seed scenario
  - add root task
  - add child task
  - update status
  - update title
  - remove task subtree

Validation requirements:

- Unknown operation returns an error.
- Unknown scenario returns an error and lists valid scenarios.
- Invalid status returns an error and lists valid statuses.
- Missing parent for `add-child` returns an error.
- Duplicate IDs should either update deterministically or return an error; prefer returning an error for debug clarity unless existing task semantics require update.
- Removing a parent removes descendants and root references cleanly.
- Show/hide/toggle should not create tasks; if there are no tasks, show should either seed a minimal task or return a helpful error. Prefer: show just sets expanded true if task pane is available; `/debug task seed ...` is responsible for creating tasks.

Named scenarios:

- `basic`: one short root task.
- `nested`: phase with nested children.
- `overflow`: enough tasks to force vertical scrolling.
- `wrap`: long titles that exercise wrapping.
- `all-states`: pending, in-progress, done, and blocked examples.

All debug-generated task IDs should be stable and prefixed, for example `debug:basic:1`, to avoid colliding with real task IDs.

## 3. Add dev-mode-gated slash commands

Add slash command parsing for `/debug ...`, initially focused on task pane commands:

```text
/debug help
/debug task show
/debug task hide
/debug task toggle
/debug task clear
/debug task seed <basic|nested|overflow|wrap|all-states>
/debug task add <id> <pending|in_progress|done|blocked> <title...>
/debug task add-child <parent-id> <id> <pending|in_progress|done|blocked> <title...>
/debug task status <id> <pending|in_progress|done|blocked>
/debug task title <id> <title...>
/debug task remove <id>
```

Behavior:

- Outside `/d`, any `/debug ...` command returns: `debug commands are only available after /d` or similar.
- Inside `/d`, commands call the shared controller and display a concise status message.
- Normal `/help` should not list these commands unless existing help is already dev-mode contextual. Do not clutter normal help.

## 4. Expose matching agent-callable debug controls

Find the existing agent-callable command/tool pattern and expose debug task controls through that path. The exact API should use structured arguments rather than parsing slash command strings if possible.

Suggested agent operation names, adjusted to fit existing conventions:

- `debug_task_pane_visibility` with `{ action: "show"|"hide"|"toggle" }`
- `debug_task_pane_seed` with `{ scenario: "basic"|"nested"|"overflow"|"wrap"|"all-states" }`
- `debug_task_pane_mutate` with structured sub-operations for add/add-child/status/title/remove/clear

If the current tool architecture makes multiple tools expensive, use one structured `debug_task_pane` operation:

```json
{
  "op": "seed",
  "scenario": "overflow"
}
```

All agent-callable operations must:

- check the same dev-mode helper;
- call the same controller as slash commands;
- return the same success/error semantics;
- avoid changing persistent server task state.

If exposing true agent tools requires proto/API churn larger than expected, stop and report the trade-off before implementing a partial incompatible surface.

Implementation note: the agent-callable surface is implemented as a CLI-local `/tool debug_task_pane <json>` bridge that intercepts before server RPC invocation and calls the same controller as `/debug task ...`. A true server-side native tool would need a broader UI callback/session-targeting design because server tools do not own TUI state.

## 5. Add `/debug help` and `/d` hint

- `/debug help` should list only the debug task controls and valid statuses/scenarios.
- When `/d` succeeds, append or display a short hint:

```text
Debug controls enabled. Try /debug help.
```

Keep the hint concise so it does not dominate the dev-mode transition message.

## 6. Add tests

Add or update tests for:

- `/debug ...` rejects outside dev mode.
- `/debug help` works inside dev mode and lists task commands.
- `/d` result includes the debug help hint, if current tests inspect the rendered/result text.
- `seed basic` creates a visible task and allows `show` to expand the pane.
- `seed overflow` creates vertical overflow.
- `seed wrap` creates wrapped task lines.
- `add`, `add-child`, `status`, `title`, `remove`, and `clear` mutate local task state correctly.
- invalid status/scenario/parent/id handling.
- agent-callable debug operation and slash command operation produce equivalent state changes.

Prefer controller-level tests for most mutation logic, with a smaller number of UI/slash integration tests.

## 7. Verify

Run focused tests first, then full CLI tests:

```sh
cd source/clients/cli && go test ./internal/slash ./internal/ui -run 'Test.*Debug.*|Test.*TaskPane.*Debug.*' -count=1
cd source/clients/cli && go test ./internal/slash ./internal/ui -count=1
cd source/clients/cli && go test ./... -count=1
```

If agent-callable controls touch server/proto/tool packages, also run the relevant package tests and then the full server suite if interfaces changed:

```sh
cd source/server && go test ./... -count=1
```

## 8. Checkpoint

Commit explicit paths with a conventional subject, likely:

`feat(cli): add dev task pane debug controls`

Commit body should mention:

- dev-mode gating;
- shared controller for slash and agent-callable surfaces;
- supported task scenarios and mutations;
- verification commands.
