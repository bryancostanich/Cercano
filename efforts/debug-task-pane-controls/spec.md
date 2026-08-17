# Debug task pane controls

## Problem / motivation

Task pane mouse hit testing and layout bugs are hard to reproduce because the task pane only appears when a conversation has live task state, or when a resumed conversation can hydrate tasks from a plan. That makes interactive UI debugging awkward: a developer cannot reliably show or hide the pane, seed overflow/wrapping/nested task shapes, or mutate task status while inspecting pointer behavior.

We need a small debug-only control surface that lets both a human in the TUI and the agent create and manipulate task pane state during `/d` dev-mode sessions. The controls must not become normal product UI and must not be available outside dev mode.

## Goals

- Add dev-mode-only task pane debug controls for manual TUI use.
- Expose the same controls to the agent through an agent-callable debug surface.
- Use one internal task-pane debug controller so slash commands and agent tools cannot diverge.
- Support showing, hiding, and toggling the task pane.
- Support clearing tasks, seeding named demo task scenarios, and low-level task CRUD/status manipulation.
- Provide `/debug help` in dev mode, and keep normal `/help` clean.
- Reject all debug task controls outside `/d` with a clear error.
- Add tests for gating, command parsing, task mutations, visibility changes, and agent/tool parity.

## Non-goals

- Redesigning normal task pane UI, task persistence, task hydration, or task event streaming.
- Making debug tasks durable across sessions.
- Exposing debug task mutation commands in normal `/help`.
- Allowing debug task controls outside `/d`.
- Changing real agent task semantics or server task storage.
- Fixing task pane hit-test bugs in this effort, except insofar as the new debug controls make them reproducible.

## Constraints

- Debug controls are gated on the current dev-mode state created by `/d`; no separate debug-tools flag will be added in this effort.
- The dev-mode gate should be centralized behind a helper so a future representation change is localized.
- Slash commands and agent-callable debug tools must call the same task-pane debug controller.
- Debug-generated tasks should be clearly synthetic and local to the current UI model.
- Invalid operations, such as setting an unknown status or adding a child to a missing parent, should return visible errors rather than silently corrupting task state.
- Named scenarios should be deterministic to make repros and tests stable.

## Decisions

### Debug command surface

Chosen: implement both dev-only slash commands and agent-callable debug tools, backed by the same internal controller.

| Axis | Dev-only slash commands | Agent tools exposed only in `/d` | Both slash commands and agent tools |
|---|---|---|---|
| What it means | User or assistant types slash commands like `/debug task show`; local TUI state mutates directly. | Agent gets structured debug task operations, gated by dev mode. | Both humans and the agent can drive the same debug task controls. |
| Cost | Low-to-medium. | Medium-to-high because it crosses into tool exposure/gating. | Highest of the three, but manageable if both call one controller. |
| Risk | Low, but it does not let the agent directly manipulate the running UI unless the user types commands. | Medium, because debug mutation tools must be tightly gated. | Medium, mostly from keeping surfaces consistent. |
| Reward | Immediate manual repro controls. | Agent can generate task states for repro while `/d` is active. | Best debugging ergonomics and matches the requested workflow. |
| Chosen rationale | Useful but incomplete by itself. | Useful but too narrow for manual TUI repros. | Chosen because the user explicitly wants both, and sharing the controller keeps behavior consistent. |

### Gating semantics

Chosen: gate on the current `/d` dev-mode state, not a new explicit debug-tools flag.

| Axis | Gate on current dev-mode state | Dedicated debug-tools flag set by `/d` | Dev mode plus explicit enable command |
|---|---|---|---|
| What it means | If `/d` has put the UI in dev mode, debug controls are available. | `/d` sets a separate flag; controls check that. | `/d` is necessary but not sufficient; another command enables task debug controls. |
| Cost | Low. | Medium-low. | Medium. |
| Risk | Some coupling to how dev mode is represented today. | Lower coupling, but duplicate state. | Lowest accidental exposure, highest friction. |
| Reward | Simple and predictable for developers. | More explicit safety boundary. | Very explicit consent. |
| Chosen rationale | Chosen by user preference: debug task controls should be part of being in `/d`, not a separate mode. The implementation should still centralize the check. |

### Task manipulation model

Chosen: support both named scenarios and low-level CRUD/status operations.

| Axis | Low-level task CRUD | Named demo scenarios | Both low-level CRUD and scenarios |
|---|---|---|---|
| What it means | Add/remove/update arbitrary debug tasks by ID. | Seed fixed fixtures such as nested, overflow, wrap, and all-states. | Fast deterministic fixtures plus precise manual tweaks. |
| Cost | Medium. | Low-to-medium. | Medium-high. |
| Risk | More validation needed. | Scenarios may not cover every bug. | Larger surface, but each operation can be small and tested. |
| Reward | Maximum control. | Fast common repros. | Best debugging workflow. |
| Chosen rationale | Chosen because task-pane bugs vary by shape: overflow, wrapping, nesting, status rollup, and hit testing all need different fixtures. |

### Visibility and help

Chosen: keep normal `/help` clean; expose `/debug help` only in dev mode, and show a short hint when entering `/d`.

| Axis | Hidden unless in `/d` | Listed in normal help as dev-only | Separate `/debug help` only |
|---|---|---|---|
| What it means | Normal help stays clean; dev mode can reveal commands. | Everyone sees debug commands with dev-only labels. | `/debug help` lists controls when `/d` is active. |
| Cost | Medium if completions/help are context-aware. | Low. | Low-to-medium. |
| Risk | Commands can be forgotten. | Normal users see confusing mutation commands. | Low; discoverable in the debug context. |
| Reward | Keeps product UX clean. | Maximum discoverability. | Good balance. |
| Chosen rationale | Chosen by user approval. `/d` should hint `Debug controls enabled. Try /debug help.` Outside `/d`, `/debug ...` rejects clearly. |
