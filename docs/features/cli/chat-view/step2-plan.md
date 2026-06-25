# Chat View Migration — Step 2 Implementation Plan

**Move scroll + mouse selection + scrollbar-drag + drag-scroll from `Model` into
`chatView` behind a local-coordinate boundary.**

Status: plan only (not implemented). Branch: `chat-view`. Worktree:
`/Users/bryancostanich/git_repos/bryan_costanich/Cercano/.claude/worktrees/chat-view`.

---

## Forks for the controller to decide

**None.** Decision **D2** in `docs/decisions/autonomous_2026-06-24.md` fixes the
approach (Option A: `chatView` is a self-contained widget in LOCAL coordinates;
it owns selection / scrollbar-drag / drag-scroll state and hit-tests in its own
(0,0 = top-left) space; the host translates screen→local and forwards; the
step-1 `View(selOverlay)` callback is removed). Two sub-questions D2 leaves open
are settled below by the ranking correctness→cleanliness→future-cost (not new
architectural forks, just the cleanest mechanical realization of D2):

- **Copied-selection status notice.** The host chrome owns the status bar
  (`renderStatus`, `model.go:2431`). `chatView` does not render the status bar.
  Settled: `chatView` owns the *selection state* and decides *when* a copy
  happened; the host owns the *notice string*. The forwarding methods return a
  `copied bool` (and `SelectionActive()` / `SelectionHasRange()` expose state for
  the status bar). The host sets `m.selectionNotice = "copied selection"` when a
  forwarded call reports `copied == true`. `selectionNotice` STAYS on `Model`
  (it is chrome). No notice string crosses into `chatView`.

- **Drag-scroll tick.** Settled: **`chatView` returns the tick `tea.Cmd`; the
  host forwards the tick message back in.** The host cannot synthesize the tick
  loop itself without re-importing the edge math, and `chatView` cannot schedule
  a `tea.Cmd` to itself without the host's `Update` plumbing — so the cmd is
  produced by `chatView` and threaded through the host's reducer. `MouseDrag`
  returns `tea.Cmd` (the tick cmd, or nil); the host has a `case
  dragScrollTickMsg` that calls `c.DragScrollTick() (tea.Cmd, bool)` and returns
  the rescheduled cmd. The `dragScrollTickMsg` type stays package-level in
  `chat_view.go` (moved out of `model.go`). State `dragMouse` / `dragScrolling`
  move into `chatView` (local coords).

---

## Goal

Relocate all chat-viewport *interaction* (mouse text-selection + clipboard copy,
grabbable-scrollbar drag, wheel, and edge drag-scroll) from `Model` into
`chatView`, so `chatView` is a self-contained widget that knows nothing about
screen coordinates. The host keeps layout, translates screen mouse coords →
local (subtract `m.scrollbarTop`, etc.), and forwards to `chatView`. After this
step the host's four mouse handlers shrink to "is this in the prompt / a content
page / the viewport? translate + forward." Behavior is **byte-identical**: the
step-1 golden parity gate stays green, and every existing selection / scrollbar-
drag / drag-scroll test stays green (rewired to drive `chatView`).

## Architecture

- **`chatView` (local coords, owns interaction state).** Adds fields
  `selection textSelection`, `scrollbarDragging bool`, `dragMouse tea.Mouse`,
  `dragScrolling bool`. All hit-testing is in local space: row `0` = the
  viewport's first visible line, column `0` = the viewport's left edge. The
  scrollbar is the local column `Width()-1`-and-past. `View()` applies the
  selection overlay internally (no callback).
- **Host (`Model`).** Keeps `scrollbarTop` (set in `relayout`) and all layout.
  Each mouse handler decides routing (prompt / content page / viewport). For the
  viewport region it computes `localX = mouse.X`, `localY = mouse.Y -
  m.scrollbarTop` and calls a `chatView` method. The host keeps
  `selectionNotice` (status-bar chrome) and `handleSelectionKey`'s host wiring
  (it asks `chatView` to handle the key).
- **Coordinate translation lives in the host** (where the layout knowledge
  already is) — no `SetOrigin` sync obligation (D2 counter-case to Option B).

## Tech Stack

Go, module `cercano/source/clients/cli`. Bubble Tea v2 (`charm.land/bubbletea/v2`),
`charm.land/bubbles/v2/viewport`, `charm.land/lipgloss/v2`,
`github.com/charmbracelet/x/ansi`. Tests: stdlib `testing`, golden files under
`internal/ui/testdata/chatview/`.

## Global Constraints

- Module path `cercano/source/clients/cli`; all files under
  `source/clients/cli/internal/ui/`.
- Commit messages MUST NOT contain the word "Claude" (no Co-Authored-By trailer
  naming it).
- The step-1 golden test (`chat_view_golden_test.go` + `testdata/chatview/*.golden`)
  stays **byte-identical** — selection-inactive render is unchanged. Do NOT run
  `-update`; if a golden diff appears, the change broke parity — fix the code,
  not the golden.
- Existing selection / scroll / drag tests stay green (rewired as needed, same
  assertions): `selection_test.go`, `selection_highlight_test.go`,
  `scrollbar_drag_test.go`, `drag_scroll_test.go`, `scrollbar_test.go`.
- `chatpane.go` is UNTOUCHED (it migrates in step 4).
- Each task ends compiling + tests green (no broken intermediate). Run
  `go build ./...` and `go test ./internal/ui/...` from
  `source/clients/cli/` after every task before committing.
- One hypothesis per change; no behavior change beyond the relocation.

## The new `chatView` mouse / key surface (settled)

All coordinates are **local** (host has already subtracted `scrollbarTop`).
`MouseDown`/`MouseDrag`/`MouseUp` are LEFT-button viewport events the host has
routed here. The host still owns the prompt / content-page / wheel-target
decisions.

```go
// Hit-test: is this local point on the grabbable scrollbar column?
func (c *chatView) ScrollbarHit(localX, localY int) bool

// Hit-test: is this local point inside the viewport text region?
func (c *chatView) MouseInText(localX, localY int) bool

// Left press in the viewport region. If on the bar → start scrollbar drag and
// scrub; else if in text → begin selection; else clear selection.
func (c *chatView) MouseDown(localX, localY int)

// Left drag. Scrubs the bar (if dragging it) or extends the selection (with
// edge auto-scroll). Returns the drag-scroll tick cmd to (re)start the loop, or
// nil. Host forwards the cmd.
func (c *chatView) MouseDrag(localX, localY int) tea.Cmd

// Left release. Finalizes a selection; copied reports whether a non-empty
// selection was auto-copied (host sets the status notice). cmd is the clipboard
// cmd (or nil).
func (c *chatView) MouseUp(localX, localY int) (cmd tea.Cmd, copied bool)

// Wheel scroll within the viewport (host has decided the target is the chat).
func (c *chatView) Wheel(up bool)

// Drag-scroll tick: scroll one line + extend selection if still held at edge;
// returns the rescheduled cmd (or nil) and whether the loop continues.
func (c *chatView) DragScrollTick() (tea.Cmd, bool)

// Key handling for an active selection (esc clears, c/y/cmd-c copies, typing
// clears + passes through). handled=false → host passes the key on. copied=true
// → host sets the status notice. cmd is the clipboard cmd (or nil).
func (c *chatView) HandleSelectionKey(msg tea.KeyPressMsg) (cmd tea.Cmd, handled, copied bool)

// State queries for host chrome + routing.
func (c *chatView) SelectionActive() bool    // == selection.Active
func (c *chatView) SelectionHasRange() bool   // == selection.hasRange(); drives the status bar
func (c *chatView) SelectionDragging() bool   // == selection.Dragging; drives wheel guard
func (c *chatView) ClearSelection()           // host calls on paste / focus change

// View loses its callback (D2): overlay applied internally.
func (c *chatView) View() string
```

State that moves onto `chatView`: `selection textSelection`, `scrollbarDragging
bool`, `dragMouse tea.Mouse`, `dragScrolling bool`. State that STAYS on `Model`:
`scrollbarTop` (layout), `selectionNotice` (status-bar chrome),
`contentScrollbarDragging` (content-page scrollbar, separate gesture).

---

## Task 1 — Move selection state + logic into `chatView`; `View()` applies overlay internally; remove `selOverlay`

**Deliverable:** Selection state (`selection`), all selection methods, and the
overlay live on `chatView`. `View()` takes no callback and overlays selection
itself. The host's selection key wiring forwards to `chatView`. Golden parity
green; selection tests green (rewired).

### 1a. Move `selection` field + selection methods onto `chatView`

In `internal/ui/chat_view.go`, add to the `chatView` struct (after
`focusedToolIdx`):

```go
	selection textSelection
```

In `internal/ui/selection.go`, reparent these receivers `(m Model)` /
`(m *Model)` → `(c *chatView)` and replace `m.chat.X()` / `m.X` accordingly.
Note: inside `chatView` the methods address `c.vp` and `c.plainLines` directly,
and **drop `m.scrollbarTop`** — callers now pass LOCAL coords.

- `mouseInViewportText(mouse tea.Mouse) bool` → `func (c *chatView)
  MouseInText(localX, localY int) bool`:

```go
func (c *chatView) MouseInText(localX, localY int) bool {
	return localX >= 0 && localX < c.Width() &&
		localY >= 0 && localY < c.Height()
}
```

- `beginSelection(mouse tea.Mouse)` → `func (c *chatView) beginSelection(localX,
  localY int)`; drop the `m.selectionNotice = ""` line (notice stays host-side —
  see 1d). Body becomes:

```go
func (c *chatView) beginSelection(localX, localY int) {
	pt := c.selectionPointFromMouse(localX, localY, false)
	c.selection = textSelection{Active: true, Dragging: true, Anchor: pt, Cursor: pt}
}
```

- `updateSelection(mouse, allowScroll)` → `func (c *chatView)
  updateSelection(localX, localY int, allowScroll bool)`; same logic, calls
  `c.selectionPointFromMouse(localX, localY, allowScroll)`.

- `clearSelection()` → `func (c *chatView) ClearSelection() { c.selection =
  textSelection{} }` (exported — host calls it on paste / focus change).

- `selectionPointFromMouse(mouse, allowScroll)` → `func (c *chatView)
  selectionPointFromMouse(localX, localY int, allowScroll bool) selectionPoint`.
  Replace `m.chat.*` with bare `c.*` and replace `mouse.Y - m.scrollbarTop` with
  the already-local `localY`, `mouse.X` with `localX`:

```go
func (c *chatView) selectionPointFromMouse(localX, localY int, allowScroll bool) selectionPoint {
	height := c.Height()
	row := localY
	if allowScroll {
		switch {
		case row < 0:
			c.ScrollUp(1)
			row = 0
		case row >= height:
			c.ScrollDown(1)
			row = height - 1
		}
	}
	row = clampInt(row, 0, maxInt(0, height-1))
	line := c.YOffset() + row
	pl := c.plainLines
	if len(pl) > 0 {
		line = clampInt(line, 0, len(pl)-1)
	}
	return selectionPoint{Line: line, Col: clampInt(localX, 0, c.Width())}
}
```

- `renderSelectionOnLine(line string, contentLine int)` → `func (c *chatView)
  renderSelectionOnLine(line string, contentLine int) string`; body unchanged
  except `m.selection`→`c.selection`, `m.chat.Width()`→`c.Width()`.

- `selectedText()` → `func (c *chatView) selectedText() string`; replace
  `m.chat.PlainLines()` → `c.plainLines`, `m.selection`→`c.selection`.

- `highlightRange`, `beforePoint`, `plainLines`, the `textSelection` /
  `selectionPoint` types, `selectionBg`, `ansiResetRe`,
  `isSelectionCopyKey`, `selectionClipboardCmd`, `pbcopyCmd`, `maxInt`:
  **unchanged** (package-level helpers).

### 1b. `View()` applies the overlay internally; drop the callback

In `chat_view.go`, change `View`'s signature and the overlay call:

```go
func (c *chatView) View() string {
	body := c.vp.View()
	lines := strings.Split(body, "\n")
	...
	for i, line := range lines {
		contentLine := c.vp.YOffset() + i
		line = c.renderSelectionOnLine(line, contentLine) // was: selOverlay(line, contentLine)
		...
```

### 1c. Add `SelectionActive` / `SelectionHasRange` / `SelectionDragging`

Append to `chat_view.go`:

```go
func (c *chatView) SelectionActive() bool   { return c.selection.Active }
func (c *chatView) SelectionHasRange() bool { return c.selection.hasRange() }
func (c *chatView) SelectionDragging() bool { return c.selection.Dragging }
```

### 1d. `HandleSelectionKey` moves onto `chatView`; host keeps the notice

Move `handleSelectionKey` from `selection.go` to `chat_view.go`, reparent and
return `(cmd, handled, copied)` (drop notice writes — host sets them):

```go
func (c *chatView) HandleSelectionKey(msg tea.KeyPressMsg) (tea.Cmd, bool, bool) {
	switch msg.String() {
	case "esc":
		c.ClearSelection()
		return nil, true, false
	case "enter", "c", "y", "ctrl+c":
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if isSelectionCopyKey(msg) {
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if msg.Text != "" {
		c.ClearSelection()
	}
	return nil, false, false
}
```

### 1e. Rewire the host

In `internal/ui/model.go`:

- `renderViewportWithScrollbar` (`:2363`):

```go
func (m Model) renderViewportWithScrollbar() string {
	return m.chat.View()
}
```

- The key handler (`:711`): replace the `m.handleSelectionKey` block:

```go
		if m.chat.SelectionActive() {
			cmd, handled, copied := m.chat.HandleSelectionKey(msg)
			if copied {
				m.selectionNotice = "copied selection"
			}
			if handled {
				return m, cmd
			}
		}
```

  (Note: the old code threaded `next Model` because `handleSelectionKey` had a
  `(m Model)` value receiver returning a copy; now `m.chat` is mutated in place
  via the pointer method on the value field — `m.chat.HandleSelectionKey` takes
  `&m.chat` automatically since `m` is addressable in the reducer. Confirm `m`
  is a local value in `Update`'s `switch` — it is, `Update(m Model)`.)

- `renderStatus` (`:2435`): `m.selection.hasRange()` → `m.chat.SelectionHasRange()`.
- `preparePromptInput` (`:1591`): `m.selection.Active` → `m.chat.SelectionActive()`,
  `m.clearSelection()` → `m.chat.ClearSelection()`.
- `refreshViewport` neighbor (`:1591`) and any other `m.selection.*` /
  `m.clearSelection()` / `m.beginSelection` / `m.updateSelection` /
  `m.selectedText` / `m.mouseInViewportText` / `m.renderSelectionOnLine` /
  `m.selectionPointFromMouse` reference in `model.go`: rewire to `m.chat.*` with
  local-coord translation. **Mouse handlers are fully rewired in Task 4**; for
  Task 1, do the minimal mechanical swap so it compiles (the mouse handlers
  still call e.g. `m.chat.beginSelection(mouse.X, mouse.Y-m.scrollbarTop)`,
  `m.chat.updateSelection(mouse.X, mouse.Y-m.scrollbarTop, true)`,
  `m.chat.MouseInText(mouse.X, mouse.Y-m.scrollbarTop)`,
  `m.chat.ClearSelection()`, `m.chat.SelectionDragging()` /
  `m.chat.selection.Dragging`). The `dragScrollTick` / `atScrollEdge` /
  `dragMouse` paths still reference `Model` fields — they move in Task 3, so
  leave them on `Model` for now and keep them compiling (they reference
  `m.chat.Height()` already).

- Remove the now-unused `m.selection` field from `Model` ONLY after no
  `model.go` reference remains. If Task 3's drag-scroll still reads
  `m.selection.Dragging`, keep the `chatView` accessor `SelectionDragging()` and
  route through it; do not keep a second `selection` on `Model`. Delete
  `Model.selection` in this task.

### 1f. Rewire the tests that drive selection state directly

- `selection_test.go` `newSelectionModel`: it already builds `cv` and sets
  `cv.plainLines`. Change the seam so selection state is set on `cv`, not the
  returned `Model`. Update the helper to accept/return the selection:

  `TestSelectedTextSingleLine` / `TestSelectedTextMultilineReverseDrag`: set
  `cv.selection = …` and assert `cv.selectedText()` (call on the `*chatView`).
  Build via `newTestChatView`-style local `cv`, e.g.:

```go
	cv := newTestChatView(0, 0)
	cv.plainLines = []string{"hello world"}
	cv.selection = textSelection{Active: true,
		Anchor: selectionPoint{Line: 0, Col: 6}, Cursor: selectionPoint{Line: 0, Col: 11}}
	if got, want := cv.selectedText(), "world"; got != want { ... }
```

  `TestSelectionPointFromMouseUsesViewportOffset`: now local-coord — drop
  `scrollbarTop`, call `cv.selectionPointFromMouse(3, 4, false)` (Y already
  local; the old test used `Y:4` with `scrollbarTop:2` → local row 2, line
  `10+2=12`). To preserve the asserted `Line: 12`, pass `localY = 2`:
  `cv.selectionPointFromMouse(3, 2, false)` → `want selectionPoint{Line: 12,
  Col: 3}`.

  `TestRenderSelectionOnLinePreservesPlainText`: set `cv.selection` and call
  `cv.renderSelectionOnLine(line, 0)`.

  `TestMouseReleaseCopiesDragSelection`: this drives `m.Update(MouseRelease…)` —
  it depends on Task 4's host forwarding. For Task 1, set `cv.selection` and
  `m.chat = cv` so it still routes; the `Update` path is rewired in Task 4. To
  keep it green now, set selection on `m.chat`:
  `cv.selection = textSelection{Active: true, Dragging: true, …}` then
  `m := Model{chat: cv, scrollbarDragging: true}`. Assert
  `got.selectionNotice == "copied selection"` (host still sets it) and
  `got.chat.SelectionHasRange()`. Keep `scrollbarDragging` on `Model` (moves in
  Task 2) — adjust in Task 2.

  `TestIsSelectionCopyKeyRecognizesCommandC`: unchanged (`isSelectionCopyKey` is
  package-level).

- `selection_highlight_test.go`: `highlightRange` is package-level — unchanged.

- `chat_view_test.go`: `TestChatView_TurnStatusPlaceholder` and
  `TestChatView_ViewIdentityOverlayMatchesNoSelection` call
  `c.View(func(l string,_ int) string { return l })`. Change both to `c.View()`
  (no overlay arg; identity is now the default for an inactive selection).

- `chat_view_golden_test.go`: `renderFixture` calls
  `m.renderViewportWithScrollbar()` which now calls `m.chat.View()` — no change
  needed there (it's host-side and selection is inactive in fixtures, so the
  internal overlay is a no-op → byte-identical golden).

### Test + commit

```sh
cd source/clients/cli
go build ./...
go test ./internal/ui/... -run 'ChatView|Selection|Highlight' -count=1
go test ./internal/ui/... -count=1
```

Expected: `ok  	cercano/source/clients/cli/internal/ui`. Golden parity
(`TestChatView_GoldenParity`) green = selection-inactive render unchanged.

```sh
git add -A && git commit -m "step2: move selection state+logic into chatView; View applies overlay internally"
```

---

## Task 2 — Move scrollbar-drag hit-test + state into `chatView`

**Deliverable:** `scrollbarDragging` and the bar hit-test / scrub live on
`chatView` in local coords. `scrollbar_drag_test.go` green (rewired). Golden
green.

### 2a. Add state + methods on `chatView`

In `chat_view.go` struct, add `scrollbarDragging bool`. Add (local coords; the
bar is the rightmost column — host has translated, so the local bar test mirrors
the host's old `mouse.X >= m.width-1`, but in local space the viewport's right
edge is `Width()-1`, and the host reserves the bar at the column past the text;
preserve the existing "rightmost column and past" tolerance):

```go
// ScrollbarHit reports whether a local point is on the grabbable scrollbar
// column. The bar sits at the viewport's right edge; accept the last column and
// one past (terminals report the final column as Width()-1 or Width()).
func (c *chatView) ScrollbarHit(localX, localY int) bool {
	return localX >= c.Width()-1 && localY >= 0 && localY < c.Height()
}
```

> **Translation note (host).** Today the host tests `mouse.X >= m.width-1`
> against the *screen* width because the bar is painted at screen column
> `width-1`, while the viewport text width is `contentW-2 = width-... -2`. The
> local X the host forwards is `mouse.X` (the host does NOT subtract a left
> origin — the viewport starts at screen column 0). So screen `width-1` maps to
> local `width-1`, and `c.Width()` (the text width, `contentW-2`) is `< width-1`.
> A click at screen `X = width-1` or `width` is local `X = width-1`/`width`,
> both `>= c.Width()-1` AND past the text. To preserve EXACT behavior, the host
> passes the *raw screen X* as localX for the bar test (it already equals local
> X since the viewport's left origin is column 0). Verify with
> `scrollbar_drag_test.go` (`X:79`, `X:80` at width 80, `c.Width()=79`):
> `ScrollbarHit(79,·)` and `ScrollbarHit(80,·)` must be true,
> `ScrollbarHit(5,·)` false. If `c.Width()-1 = 78` makes a text click at
> X=78 falsely hit the bar, tighten to `localX >= c.Width()` — confirm against
> the test's text-click cases (`X:5`, `X:2`) which are far from the edge.
> **PROBE before fixing:** add a sub-test asserting the four X values above map
> correctly; pick the threshold that passes all of `scrollbar_drag_test.go`.

```go
// ScrollbarScrub jumps the scroll offset to the local click row.
func (c *chatView) scrollbarScrub(localY int) {
	off := scrollOffsetFromClick(localY, 0, c.Height(), c.TotalLineCount())
	c.SetYOffset(off)
}
```

(`scrollOffsetFromClick(clickRow, top, height, total)` with `top=0` because
`localY` is already viewport-relative.)

```go
func (c *chatView) ScrollbarDragging() bool { return c.scrollbarDragging }
func (c *chatView) StopScrollbarDrag()      { c.scrollbarDragging = false }
```

### 2b. Fold the bar press / scrub into `MouseDown` and the drag path

Define `MouseDown` (the unified left-press entry; host calls it for any viewport
left-click):

```go
func (c *chatView) MouseDown(localX, localY int) {
	if c.ScrollbarHit(localX, localY) {
		c.selection.Dragging = false // a bar grab is a scroll, not a selection
		c.scrollbarDragging = true
		c.scrollbarScrub(localY)
		return
	}
	if c.MouseInText(localX, localY) {
		c.beginSelection(localX, localY)
		return
	}
	c.ClearSelection()
}
```

### 2c. Host rewires `tea.MouseClickMsg` (viewport branch only)

In `model.go` `case tea.MouseClickMsg` (`:555-577`), replace the bar hit-test +
`beginSelection` + `clearSelection` block with translate + forward. Keep the
content-page and prompt branches as-is:

```go
		// viewport region (after content-page + prompt routing above)
		m.chat.MouseDown(mouse.X, mouse.Y-m.scrollbarTop)
		return m, nil
```

Drop `m.scrollbarDragging` / `height` / `onBar` / `m.selection.Dragging` /
`scrollOffsetFromClick` / `m.mouseInViewportText` / `m.beginSelection` /
`m.clearSelection` from this branch — all now inside `MouseDown`.

### 2d. Host rewires `tea.MouseMotionMsg` scrollbar branch

In `model.go` `case tea.MouseMotionMsg` (`:603-608`):

```go
		if m.chat.ScrollbarDragging() {
			m.chat.scrollbarScrub(mouse.Y - m.scrollbarTop)
			return m, nil
		}
```

The `m.scrollbarDragging = false` resets at `:590` (pendingConfirm) become
`m.chat.StopScrollbarDrag()`. The selection-drag branch (`:609`) stays for now
(Task 3 moves drag-scroll). `m.selection.Dragging = false` at `:591` →
`m.chat.ClearSelectionDrag()` — add a small setter:

```go
func (c *chatView) ClearSelectionDrag() { c.selection.Dragging = false }
```

### 2e. Host rewires `tea.MouseReleaseMsg` (`:633-644`)

`m.scrollbarDragging = false` → `m.chat.StopScrollbarDrag()`. The selection
finalize block routes through `chatView` in Task 4; for Task 2, keep it
compiling by routing the reads: `m.selection.Dragging` →
`m.chat.SelectionDragging()`, `m.updateSelection(...)` →
`m.chat.updateSelection(mouse.X, mouse.Y-m.scrollbarTop, true)`,
`m.selection.Dragging = false` → `m.chat.ClearSelectionDrag()`,
`m.selection.empty()` → add `func (c *chatView) selectionEmpty() bool { return
c.selection.empty() }`, `m.clearSelection()` → `m.chat.ClearSelection()`,
`m.selectedText()` → `m.chat.selectedText()`. (Fully cleaned up in Task 4.)

### 2f. Wheel guard + remove `Model.scrollbarDragging`

`tea.MouseWheelMsg` (`:504`) `m.selection.Dragging` →
`m.chat.SelectionDragging()`. Delete the `scrollbarDragging bool` field from
`Model` (`:64`) once no `model.go` reference remains. Keep
`contentScrollbarDragging` (`:68`) — separate gesture, untouched.

### 2g. Rewire `scrollbar_drag_test.go`

The tests drive `m.Update(MouseClick/Motion)` and read `m.chat.YOffset()` —
those still work through the host forwarding. Replace direct `m.selection.Dragging`
reads with `m.chat.SelectionDragging()` and `m.scrollbarDragging` with
`m.chat.ScrollbarDragging()`:

- `TestScrollbarDragWinsOverStuckSelection`: `if !m.selection.Dragging` →
  `if !m.chat.SelectionDragging()`.
- `TestScrollbarDragAfterViewportSelectAndCopy`: the `t.Logf` reads → `m.chat.*`.
- `buildDragModel`: builds `cv` then `Model{...}` — fine.

Add the `ScrollbarHit` threshold PROBE sub-test (from 2a) here.

### Test + commit

```sh
cd source/clients/cli
go build ./...
go test ./internal/ui/... -run 'Scrollbar|Drag|Selection|ChatView' -count=1
go test ./internal/ui/... -count=1
```

Expected: all green, including `TestScrollbarGrabAtRightEdge`,
`TestScrollbarDragFresh`, `TestScrollbarDragWinsOverStuckSelection`. Golden green.

```sh
git add -A && git commit -m "step2: move scrollbar-drag hit-test and state into chatView (local coords)"
```

---

## Task 3 — Move drag-scroll + wheel into `chatView`

**Deliverable:** `dragMouse`, `dragScrolling`, `atScrollEdge`,
`dragScrollTick`/`dragScrollTickMsg`, and wheel scroll live on `chatView`.
`drag_scroll_test.go` green (rewired). Golden green.

### 3a. Move drag-scroll state + tick into `chat_view.go`

Move from `model.go` to `chat_view.go`: `dragScrollTickMsg` type,
`dragScrollTick()` func. Add fields to `chatView`: `dragMouse tea.Mouse`,
`dragScrolling bool`. Add:

```go
// atScrollEdge reports whether the last drag pointer (local coords) is past the
// top or bottom edge of the viewport.
func (c *chatView) atScrollEdge() bool {
	row := c.dragMouse.Y // dragMouse stored in LOCAL coords
	return row < 0 || row >= c.Height()
}
```

> **Local-coord note.** `dragMouse` is stored LOCAL (host subtracts
> `scrollbarTop` before forwarding). The old `atScrollEdge` computed
> `m.dragMouse.Y - m.scrollbarTop`; now `dragMouse.Y` is already local, so the
> subtraction is gone. `MouseDrag`/`DragScrollTick` store/read local X/Y.

### 3b. `MouseDrag` (selection extend + edge auto-scroll) returns the tick cmd

```go
func (c *chatView) MouseDrag(localX, localY int) tea.Cmd {
	if c.scrollbarDragging {
		c.scrollbarScrub(localY)
		return nil
	}
	if !c.selection.Dragging {
		return nil
	}
	c.dragMouse = tea.Mouse{X: localX, Y: localY}
	c.updateSelection(localX, localY, true)
	if c.atScrollEdge() && !c.dragScrolling {
		c.dragScrolling = true
		return dragScrollTick()
	}
	return nil
}
```

### 3c. `DragScrollTick` (host forwards `dragScrollTickMsg` here)

```go
func (c *chatView) DragScrollTick() (tea.Cmd, bool) {
	if !c.selection.Dragging || !c.atScrollEdge() {
		c.dragScrolling = false
		return nil, false
	}
	c.updateSelection(c.dragMouse.X, c.dragMouse.Y, true)
	return dragScrollTick(), true
}

func (c *chatView) StopDragScroll() { c.dragScrolling = false }
func (c *chatView) DragScrolling() bool { return c.dragScrolling }
```

### 3d. `Wheel`

```go
func (c *chatView) Wheel(up bool) {
	if up {
		c.ScrollUp(promptWheelDelta)
	} else {
		c.ScrollDown(promptWheelDelta)
	}
}
```

> Confirm the old wheel path: `m.chat.Update(msg)` (`:517`) forwarded the raw
> `tea.MouseWheelMsg` to the viewport, which applies its own default delta. To
> stay byte-identical, KEEP `m.chat.Update(msg)` for the chat wheel (do NOT
> swap to `Wheel`) unless a test pins the delta. `Wheel(up)` is added for `/c`
> reuse symmetry but the host's chat-wheel branch stays `m.chat.Update(msg)`.
> **Decision: keep `m.chat.Update(msg)` for the wheel** (zero behavior change);
> expose `Wheel` for step 4 but don't rewire the chat path. Drop `Wheel` if it
> ends unused after step 4 — note it as provisional.

### 3e. Host rewires the motion / tick / release paths

In `model.go`:

- `case tea.MouseMotionMsg` selection branch (`:609-619`):

```go
		if m.chat.SelectionDragging() {
			cmd := m.chat.MouseDrag(mouse.X, mouse.Y-m.scrollbarTop)
			return m, cmd
		}
```

- `case dragScrollTickMsg` (`:1006-1014`):

```go
	case dragScrollTickMsg:
		cmd, _ := m.chat.DragScrollTick()
		return m, cmd
```

- `case tea.MouseReleaseMsg` (`:628`): `m.dragScrolling = false` →
  `m.chat.StopDragScroll()`.

- Delete `Model.dragMouse`, `Model.dragScrolling` fields (`:74-75`) and the
  `Model.atScrollEdge` method (`:446-451`) — all on `chatView` now.

### 3f. Rewire `drag_scroll_test.go`

`TestDragScroll_ContinuesWhileHeldAtEdge` drives `m.Update(...)` and reads
`m.dragScrolling` / `m.chat.YOffset()`. It seeds `m.selection = textSelection{…}`
directly — change to `m.chat.selection = textSelection{…}` (selection now on
`chatView`). The `aboveTop := tea.Mouse{X:5, Y: m.scrollbarTop-1}` motion: host
forwards local `Y = (scrollbarTop-1) - scrollbarTop = -1` → `atScrollEdge` true
→ scroll up. Reads: `m.dragScrolling` → `m.chat.DragScrolling()`. The
`dragScrollTickMsg{}` sends and the release behavior stay (host forwards). Keep
the same assertions (scrolls up on motion, ticks keep scrolling, release stops).

### Test + commit

```sh
cd source/clients/cli
go build ./...
go test ./internal/ui/... -run 'DragScroll|Drag|Selection|ChatView|Scrollbar' -count=1
go test ./internal/ui/... -count=1
```

Expected: `TestDragScroll_ContinuesWhileHeldAtEdge` green; all green. Golden green.

```sh
git add -A && git commit -m "step2: move drag-scroll, edge tick, and dragMouse into chatView"
```

---

## Task 4 — Host mouse handlers become translate + forward; finalize copy seam

**Deliverable:** The four host mouse handlers are pure translate + forward for
the viewport region; `MouseUp` owns selection finalize + clipboard; the host
only sets `selectionNotice` from the returned `copied`. All interaction tests
green; golden green.

### 4a. `MouseUp` on `chatView`

```go
// MouseUp finalizes a left release in the viewport. copied reports a non-empty
// selection was auto-copied (host sets the status notice). cmd is the clipboard
// cmd or nil.
func (c *chatView) MouseUp(localX, localY int) (tea.Cmd, bool) {
	c.StopDragScroll()
	if c.selection.Dragging {
		c.updateSelection(localX, localY, true)
		c.selection.Dragging = false
		if c.selection.empty() {
			c.ClearSelection()
		} else if text := c.selectedText(); text != "" {
			c.scrollbarDragging = false
			return selectionClipboardCmd(text), true
		}
	}
	c.scrollbarDragging = false
	return nil, false
}
```

### 4b. Host `tea.MouseReleaseMsg` (viewport branch)

Replace the selection finalize block (`:632-645`) with:

```go
		cmd, copied := m.chat.MouseUp(mouse.X, mouse.Y-m.scrollbarTop)
		if copied {
			m.selectionNotice = "copied selection"
		}
		return m, cmd
```

(The `m.input.Dragging()` prompt branch above stays. The `m.contentPageActive()`
branch stays.)

### 4c. Confirm all four handlers are translate + forward

- `tea.MouseWheelMsg`: content-page / prompt branches unchanged; chat branch is
  `cmd := m.chat.Update(msg); return m, cmd` (kept per 3d) with the guard
  `if m.chat.SelectionDragging() { return m, nil }`.
- `tea.MouseClickMsg`: content-page / prompt branches unchanged; chat branch is
  `m.chat.MouseDown(mouse.X, mouse.Y-m.scrollbarTop); return m, nil`.
- `tea.MouseMotionMsg`: content-page / pendingConfirm / prompt branches
  unchanged; chat branch is `if m.chat.ScrollbarDragging() { scrub; return } ;
  if m.chat.SelectionDragging() { cmd := m.chat.MouseDrag(...); return m, cmd }`.
  Simplify the scrollbar scrub to `m.chat.MouseDrag` too (it handles the bar
  internally) — call `m.chat.MouseDrag(mouse.X, mouse.Y-m.scrollbarTop)` once and
  return its cmd, dropping the separate `ScrollbarDragging` host branch IF that
  preserves the priority (bar drag over selection — `MouseDrag` checks
  `scrollbarDragging` first, so it does). **PROBE:** run
  `scrollbar_drag_test.go` after collapsing; if `TestScrollbarDragWinsOverStuckSelection`
  fails, keep the explicit host `ScrollbarDragging` branch. Pick the version
  that passes.
- `tea.MouseReleaseMsg`: per 4b.

### 4d. Finalize `TestMouseReleaseCopiesDragSelection`

Rewire to set selection on `m.chat` and assert through the host:

```go
	cv := newChatView(theme.NewStyles(p), p, 20, 4)
	cv.vp.SetContent("hello world")
	cv.plainLines = []string{"hello world"}
	cv.selection = textSelection{Active: true, Dragging: true,
		Anchor: selectionPoint{Line: 0, Col: 0}, Cursor: selectionPoint{Line: 0, Col: 1}}
	cv.scrollbarDragging = true
	m := Model{scrollbarTop: 0, chat: cv}
	next, cmd := m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	got := next.(Model)
	// cmd != nil; got.selectionNotice == "copied selection";
	// !got.chat.ScrollbarDragging(); got.chat.SelectionHasRange()
```

### 4e. Final sweep for stale `Model` references

Grep and confirm none remain in `model.go` / `selection.go`:
`m.selection`, `m.scrollbarDragging`, `m.dragScrolling`, `m.dragMouse`,
`m.atScrollEdge`, `m.beginSelection`, `m.updateSelection`, `m.clearSelection`,
`m.selectedText`, `m.mouseInViewportText`, `m.handleSelectionKey`,
`m.renderSelectionOnLine`, `m.selectionPointFromMouse`. All must route through
`m.chat.*`. `selection.go` should now hold only package-level helpers
(`textSelection`, `selectionPoint`, `plainLines`, `beforePoint`,
`highlightRange`, `selectionBg`, `ansiResetRe`, `isSelectionCopyKey`,
`selectionClipboardCmd`, `pbcopyCmd`, `maxInt`) — the `(c *chatView)` methods may
live in `selection.go` or `chat_view.go` (keep them where they read cleanest;
prefer `chat_view.go` for the public surface, `selection.go` for the internal
helpers).

### Test + commit

```sh
cd source/clients/cli
go build ./...
go vet ./internal/ui/...
go test ./internal/ui/... -count=1
go test ./... -count=1
```

Expected: `ok  	cercano/source/clients/cli/internal/ui`, full suite green,
golden parity intact (no `-update`). Manual smoke (optional, per design's
testing summary): `cercano` renders, scrolls via wheel + bar drag, selects +
copies, edge drag-scrolls — visually identical.

```sh
git add -A && git commit -m "step2: host mouse handlers translate+forward to chatView; finalize copy seam"
```

---

## Done criteria (step 2)

- `chatView` owns `selection`, `scrollbarDragging`, `dragMouse`, `dragScrolling`;
  `Model` holds none of them.
- `chatView.View()` applies the selection overlay internally; no callback; the
  step-1 `View(selOverlay)` is gone.
- Host mouse handlers are translate + forward for the viewport region; the host
  keeps `scrollbarTop` (layout) and `selectionNotice` (status-bar chrome).
- `selection_test.go`, `selection_highlight_test.go`, `scrollbar_drag_test.go`,
  `drag_scroll_test.go`, `scrollbar_test.go`, `chat_view_test.go` green
  (rewired to drive `chatView`).
- `chat_view_golden_test.go` + `testdata/chatview/*.golden` byte-identical.
- `chatpane.go` untouched. `go build ./...` + `go test ./...` green.
- No commit message contains "Claude".
```
