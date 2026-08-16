# Plan: reusable task-panel scrollbars

## Approved direction

Extract a reusable orientation-aware scrollbar/scroll-region primitive and migrate the task pane to it first. Do not patch task-pane scrollbars with another local-only state machine.

The expanded pane should collapse from a clear rail/header toggle target, not from arbitrary body clicks. Body clicks should remain available for scrolling, scrollbar interaction, and future row interactions.

| Axis | Patch task pane locally | Extract reusable scrollbar/scroll-region primitive |
|---|---|---|
| Cost | Low-to-medium: add local drag fields, hit tests, and pointer math to task-pane/model code. | Medium: add a small reusable helper and update task pane to use it, plus unit tests for helper and pane behavior. |
| Risk | Medium: likely fixes today but preserves duplicate behavior and future drift. | Medium: more files touched, but helper can be tested independently and reused later. |
| Reward | Fast symptom fix. | Fixes the root design smell: rendering, hit testing, click mapping, and drag behavior live in one shared component. |
| Side effects | Keeps separate scrollbar conventions alive. | Creates a clean seam for future panes and eventual chat/content migration. |
| Best reason | Fastest if the task pane were temporary. | Correct architecture for a persistent UI component. |
| Main drawback | Hackier; duplicates behavior again. | More up-front work. |

## Implementation steps

Status note: `plan_set_status` is unavailable in this tool schema, so this plan tracks execution manually.

- [x] 1. Add reusable scrollbar geometry
- [x] 2. Add task-pane scrollbar geometry/hit helpers
- [x] 3. Add task-pane drag state to the model
- [x] 4. Wire mouse events
- [x] 5. Fix horizontal keyboard behavior if needed
- [x] 6. Tests
- [x] 7. Verify
- [x] 8. Checkpoint

Follow-up execution note: after implementation, live feedback identified two additional root causes in the same task-pane input path: horizontal trackpad gestures emit `MouseWheelLeft`/`MouseWheelRight`, which were not handled, and the collapsed task-pane toggle accepted any X coordinate because `taskPaneToggleHit` returned true for every click row while collapsed. Both are covered by tests now.

### 1. Add reusable scrollbar geometry

Extend or replace `source/clients/cli/internal/ui/scrollbar.go` with a small orientation-aware primitive.

Suggested shape:

```go
type scrollbarOrientation int
const (
    scrollbarVertical scrollbarOrientation = iota
    scrollbarHorizontal
)

type scrollbarState struct {
    Total    int
    Viewport int
    Offset   int
    Length   int
}

type scrollbarMetrics struct {
    ThumbStart int
    ThumbSize  int
    MaxOffset  int
    Overflow   bool
}
```

Core helpers should be pure and unit-testable:

- compute thumb start/size for vertical or horizontal bars;
- render a glyph row/column from metrics;
- map a click/drag coordinate on the bar to an offset;
- clamp offsets.

Keep existing `scrollbarColumn` and `scrollOffsetFromClick` as wrappers if that minimizes chat/content churn, but put the shared math underneath them.

### 2. Add task-pane scrollbar geometry/hit helpers

In `task_pane.go`, centralize the panel geometry currently spread across render logic:

- pane screen bounds;
- header rows;
- body viewport rows;
- content width;
- vertical bar column when present;
- horizontal bar row when present;
- max vertical/horizontal offsets.

Use the reusable scrollbar helper to produce vertical and horizontal scrollbar state.

Add hit helpers such as:

- `taskPaneVerticalScrollbarHit(x, y)`;
- `taskPaneHorizontalScrollbarHit(x, y)`;
- or one `taskPaneScrollbarAt(x, y)` returning axis + state.

The hit helpers must use terminal coordinates and account for the task pane being anchored to the right side of the viewport.

### 3. Add task-pane drag state to the model

Add model/task-pane state for active scrollbar drag:

- no active task-pane drag;
- vertical drag;
- horizontal drag.

This can live in `Model` if it follows existing content scrollbar drag style, or inside `taskPaneState` if the state is pane-specific. Prefer pane-specific if it keeps ownership clearer.

### 4. Wire mouse events

Update `Model.Update` mouse paths:

- On `MouseClickMsg`:
  - keep content pages first;
  - keep pending-confirm special behavior;
  - keep task-pane rail/header toggle target;
  - if expanded and click hits a task-pane scrollbar, set drag state and scroll to clicked offset;
  - if expanded and click is in task-pane body, consume it for now so it does not start chat selection behind the pane;
  - otherwise continue existing prompt/link/tool/chat handling.

- On `MouseMotionMsg`:
  - if task-pane scrollbar dragging is active, update `ScrollY` or `ScrollX` from pointer position and return;
  - otherwise preserve existing prompt/chat/content drag handling.

- On `MouseReleaseMsg`:
  - clear task-pane drag state before falling through to input/chat release handling when appropriate;
  - ensure release outside the pane still ends drag.

- On `MouseWheelMsg`:
  - preserve vertical wheel scrolling over expanded task pane;
  - support distinct horizontal wheel events only if the current Bubble Tea version exposes them clearly.

### 5. Fix horizontal keyboard behavior if needed

Verify right/left key handling for task-pane horizontal scroll:

- It should not require focus in the prompt if the pane is expanded and scrollable.
- It should not steal left/right from prompt text editing when the prompt has content or cursor movement should happen in the prompt.
- It should clamp `ScrollX` to max horizontal offset.

If the existing behavior already matches this, keep it.

### 6. Tests

Add focused tests in `task_pane_test.go` and pure scrollbar tests:

- pure horizontal scrollbar thumb geometry and click-to-offset mapping;
- pure vertical mapping still matches existing behavior;
- task-pane vertical scrollbar click sets `ScrollY`;
- task-pane vertical scrollbar drag updates `ScrollY` and release clears drag;
- task-pane horizontal scrollbar click sets `ScrollX`;
- task-pane horizontal scrollbar drag updates `ScrollX` and release clears drag;
- expanded rail/header click collapses the pane;
- body click does not collapse the pane and does not start chat selection behind it;
- existing wheel and keyboard horizontal scroll tests continue to pass.

### 7. Verify

Run:

```sh
cd source/clients/cli && go test ./internal/ui -count=1
cd source/clients/cli && go test ./... -count=1
```

If visible golden output changes unexpectedly, inspect before updating. Scrollbar rendering should ideally remain visually compatible unless the shared helper intentionally changes glyph placement.

### 8. Checkpoint

Commit explicit paths with a conventional subject, likely:

`fix(cli): reuse scrollbar behavior in task pane`

Body should mention root cause, pointer interactions added, tests, and verification commands.
