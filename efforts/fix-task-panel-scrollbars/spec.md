# Fix task panel scrolling and scrollbar interactions

## Problem

The recent task panel scroll work renders scrollbars but does not provide the interaction model users expect from the rest of the CLI. In daylight and normal use the panel can show vertical and horizontal bars, but horizontal scrolling is incomplete, clicking or dragging those bars does not work, and the expanded panel does not have a reliable hide/toggle target matching the collapsed tab behavior.

This is also a code smell: the task panel has its own ad-hoc scrollbar math and rendering instead of using a reusable scrollbar or scroll-region primitive. That allowed the visual scrollbar to ship without the corresponding hit testing, drag state, click-to-position behavior, and orientation-aware behavior.

## Observed root causes

From the read-only audit:

- `source/clients/cli/internal/ui/task_pane.go`
  - `renderTaskPane` calculates `contentW`, `bodyH`, `needV`, `needH`, max line width, and offsets directly.
  - It renders vertical bars with `scrollbarColumn` and horizontal bars with `horizontalScrollbarRow`, but that is only visual output.
  - The task pane state stores `ScrollY` and `ScrollX`, but no drag target/state exists for either scrollbar.

- `source/clients/cli/internal/ui/model.go`
  - Mouse wheel events over the expanded task pane call `scrollTaskPaneBy` for vertical scrolling only.
  - `MouseClickMsg`, `MouseMotionMsg`, and `MouseReleaseMsg` have scrollbar drag handling for content pages and chat, but not for the task pane.
  - The task pane toggle check runs before chat/prompt handling, but after expansion only the left rail is treated as the toggle target. Body clicks are not meant to collapse the pane, and there is no clear close affordance beyond the rail.

- `source/clients/cli/internal/ui/scrollbar.go`
  - Existing shared code only covers vertical thumb geometry and mapping a vertical click to an offset.
  - There is no reusable orientation-aware scrollbar primitive that can render, hit-test, and map pointer positions for both vertical and horizontal bars.

- `source/clients/cli/internal/ui/task_pane_test.go`
  - Existing coverage verifies rendering of scrollbars, vertical wheel scroll, and right-arrow horizontal key scroll.
  - It does not cover task-pane scrollbar click, vertical drag, horizontal click/drag, release cleanup, or expanded-state collapse via the rail/header.

## Desired behavior

1. The task panel uses a reusable orientation-aware scrollbar/scroll-region primitive rather than bespoke one-off scrollbar behavior.
2. Vertical task-pane scrollbar behavior:
   - renders only when content overflows vertically;
   - click on track/thumb moves to the corresponding scroll position;
   - dragging updates `ScrollY` continuously;
   - mouse release clears drag state.
3. Horizontal task-pane scrollbar behavior:
   - renders only when content overflows horizontally;
   - keyboard left/right still scrolls when the task pane is active/eligible;
   - click on track/thumb moves to the corresponding `ScrollX` position;
   - dragging updates `ScrollX` continuously;
   - release clears drag state.
4. Wheel scrolling over the expanded task pane continues to scroll vertically.
5. If Bubble Tea exposes a distinct horizontal wheel/trackpad event in the current version, support it through the same horizontal scroll path. If it does not, do not fake horizontal wheel behavior.
6. The expanded task pane has a stable hide/toggle target. The approved UX is rail/header toggle only, not arbitrary body-click collapse.
7. Normal pane body interactions, scrollbar dragging, chat selection, prompt interaction, and content-page scrollbars do not interfere with each other.

## Non-goals

- Redesigning task rendering, task hierarchy, task statuses, or task row selection.
- Making the entire expanded panel body click-to-collapse.
- Reworking the main chat scrollback internals unless needed to keep the shared primitive clean.
- Changing task pane availability thresholds or default width.
- Adding a new visible close button in this pass.

## Acceptance criteria

- A reusable scrollbar or scroll-region helper exists and is used by the task pane for both vertical and horizontal geometry/hit mapping.
- Task-pane vertical scrollbar click and drag are covered by tests.
- Task-pane horizontal scrollbar click and drag are covered by tests.
- Task-pane release clears any active drag state and does not leave subsequent pointer movement scrolling the pane.
- Expanded rail/header click collapses the pane; body clicks do not accidentally collapse it.
- Existing task-pane tests still pass.
- Existing chat/content-page scrollbar behavior is not regressed.
- `cd source/clients/cli && go test ./internal/ui -count=1` passes.
- `cd source/clients/cli && go test ./... -count=1` passes.
