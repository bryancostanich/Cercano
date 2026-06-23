# Buffer Viewport Scrollbar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a draggable, overflow-only vertical scrollbar to the chat buffer viewport in the cercano-cli TUI.

**Architecture:** A one-column vertical bar painted on the right edge of the scrollback `viewport.Model`, with thumb geometry computed from the viewport's live metrics (`TotalLineCount`, `Height`, `YOffset`). Pure geometry helpers (unit-tested) feed both the renderer (in `View()`) and the mouse drag handler (in `Update()`). No bubbles native widget exists; this is manual.

**Tech Stack:** Go, `charm.land/bubbles/v2/viewport`, `charm.land/bubbletea/v2` (mouse messages), `charm.land/lipgloss/v2`.

## Global Constraints

- Work in worktree `Cercano-scrollbar` (branch `tui-scrollbar`, cut from `main` at `b0d9f63`). All `go` commands run from `source/server`.
- Build: `go build ./...`  Test: `go test ./... -count=1`  Focused: `go test ./internal/cli/ui/ -run TestScrollbar -v` (from `source/server`).
- Do NOT `git push`. Commit locally per task.
- Visibility: overflow-only — the bar column is ALWAYS reserved (no reflow), but the thumb/track paint only when `TotalLineCount > Height`; blank space otherwise.
- Style: thumb glyph `█` in `m.styles.Border` (grey `#6F6F6F`); track glyph `░` in `m.styles.BorderDim` (grey `#434343`); a plain space when no overflow.
- Mouse mode is already `tea.MouseModeCellMotion` (set in `View()`); motion is reported while a button is held. Mouse handlers are gated off when `m.editorActive || m.historyActive || m.pendingConfirm != nil` (same gate as the existing `tea.MouseWheelMsg` case).
- The viewport's absolute top screen row is `2 + splashH` (header row + divider row + splash height), computed in `relayout()`.

---

### Task 1: Pure scrollbar geometry (`scrollbar.go` + tests)

Pure int-in/int-out helpers, fully unit-tested. No model state, no rendering.

**Files:**
- Create: `source/server/internal/cli/ui/scrollbar.go`
- Test: `source/server/internal/cli/ui/scrollbar_test.go`

**Interfaces:**
- Produces:
  - `scrollbarThumb(total, height, yOffset int) (thumbTop, thumbSize int, ok bool)` — `ok=false` when `total <= height` (no overflow).
  - `scrollbarColumn(total, height, yOffset int) []rune` — `height` runes: `'█'` thumb, `'░'` track, `' '` when no overflow. Used by `View()` in Task 2.
  - `scrollOffsetFromClick(clickRow, top, height, total int) int` — clamped target `YOffset`. Used by `Update()` in Task 3.

- [ ] **Step 1: Write the failing tests**

`source/server/internal/cli/ui/scrollbar_test.go`:
```go
package ui

import (
	"strings"
	"testing"
)

func TestScrollbarColumnNoOverflow(t *testing.T) {
	// total <= height → all blanks, no thumb.
	col := scrollbarColumn(5, 10, 0)
	if len(col) != 10 {
		t.Fatalf("len = %d, want 10", len(col))
	}
	if got := string(col); got != strings.Repeat(" ", 10) {
		t.Fatalf("no-overflow column = %q, want 10 spaces", got)
	}
}

func TestScrollbarColumnTop(t *testing.T) {
	// total=40, height=10, yOffset=0 → thumb at top, size = max(1,round(10*10/40))=3.
	col := scrollbarColumn(40, 10, 0)
	want := "███" + strings.Repeat("░", 7)
	if got := string(col); got != want {
		t.Fatalf("top column = %q, want %q", got, want)
	}
}

func TestScrollbarColumnBottom(t *testing.T) {
	// yOffset = total-height = 30 → thumb flush at bottom.
	col := scrollbarColumn(40, 10, 30)
	want := strings.Repeat("░", 7) + "███"
	if got := string(col); got != want {
		t.Fatalf("bottom column = %q, want %q", got, want)
	}
}

func TestScrollbarThumbMinSize(t *testing.T) {
	// Huge total → thumb clamps to size 1, never 0.
	_, size, ok := scrollbarThumb(100000, 20, 0)
	if !ok || size != 1 {
		t.Fatalf("thumb size = %d ok=%v, want size 1 ok=true", size, ok)
	}
}

func TestScrollOffsetFromClick(t *testing.T) {
	// viewport top=2, height=10, total=40 (max offset 30).
	cases := []struct {
		clickRow, want int
	}{
		{2, 0},    // top row → offset 0
		{11, 30},  // bottom row (top+height-1) → max offset 30
		{2 - 5, 0},  // above viewport → clamp 0
		{2 + 100, 30}, // far below → clamp max
	}
	for _, c := range cases {
		if got := scrollOffsetFromClick(c.clickRow, 2, 10, 40); got != c.want {
			t.Errorf("scrollOffsetFromClick(%d,2,10,40) = %d, want %d", c.clickRow, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ui/ -run TestScrollbar -v` (and `-run TestScrollOffset`)
Expected: FAIL — `undefined: scrollbarColumn`, `undefined: scrollbarThumb`, `undefined: scrollOffsetFromClick`.

- [ ] **Step 3: Implement `scrollbar.go`**

`source/server/internal/cli/ui/scrollbar.go`:
```go
package ui

// scrollbar.go — pure geometry for the chat viewport's vertical scrollbar.
// No model state, no rendering: ints in, glyphs/ints out, so the logic is
// fully unit-testable. The renderer (View) and the drag handler (Update)
// both build on these.

// round returns n rounded to the nearest int (ties away from zero) for a
// numerator/denominator division, avoiding float math.
func roundDiv(num, den int) int {
	if den == 0 {
		return 0
	}
	return (num + den/2) / den
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// scrollbarThumb computes the thumb's row range within a bar of `height` rows.
// ok is false when there is no overflow (total <= height) — the caller paints
// a blank column in that case.
func scrollbarThumb(total, height, yOffset int) (thumbTop, thumbSize int, ok bool) {
	if height <= 0 || total <= height {
		return 0, 0, false
	}
	thumbSize = roundDiv(height*height, total)
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > height {
		thumbSize = height
	}
	maxTop := height - thumbSize
	maxOffset := total - height
	yOffset = clampInt(yOffset, 0, maxOffset)
	thumbTop = roundDiv(yOffset*maxTop, maxOffset)
	thumbTop = clampInt(thumbTop, 0, maxTop)
	return thumbTop, thumbSize, true
}

// scrollbarColumn returns `height` runes, one per viewport row: '█' for the
// thumb, '░' for the track, ' ' when there is no overflow. The caller styles
// each rune (thumb → Border grey, track → BorderDim grey, space → plain).
func scrollbarColumn(total, height, yOffset int) []rune {
	col := make([]rune, height)
	thumbTop, thumbSize, ok := scrollbarThumb(total, height, yOffset)
	for i := range col {
		switch {
		case !ok:
			col[i] = ' '
		case i >= thumbTop && i < thumbTop+thumbSize:
			col[i] = '█'
		default:
			col[i] = '░'
		}
	}
	return col
}

// scrollOffsetFromClick maps an absolute screen row to a clamped viewport
// YOffset. top is the viewport's first screen row; height its row count;
// total the content line count.
func scrollOffsetFromClick(clickRow, top, height, total int) int {
	maxOffset := total - height
	if maxOffset <= 0 || height <= 1 {
		return 0
	}
	rel := clampInt(clickRow-top, 0, height-1)
	off := roundDiv(rel*maxOffset, height-1)
	return clampInt(off, 0, maxOffset)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ui/ -run 'TestScrollbar|TestScrollOffset' -v`
Expected: PASS (all five tests). Note `TestScrollbarColumnBottom`: total=40,height=10 → size 3, maxTop 7, yOffset 30 → thumbTop = round(30*7/30)=7 → thumb rows 7,8,9 (flush bottom). ✓

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/ui/scrollbar.go source/server/internal/cli/ui/scrollbar_test.go
git commit -m "feat(tui): pure scrollbar geometry helpers"
```

---

### Task 2: Render the bar in the viewport (`relayout` + `View`)

Reserve the bar column and composite the styled bar onto the viewport block.

**Files:**
- Modify: `source/server/internal/cli/ui/model.go` (`relayout`, `View`, add a `renderViewportWithScrollbar` helper and a `scrollbarTop` field)

**Interfaces:**
- Consumes: `scrollbarColumn(total, height, yOffset int) []rune` (Task 1).
- Produces: model field `scrollbarTop int`; method `func (m Model) renderViewportWithScrollbar() string`.

- [ ] **Step 1: Add the `scrollbarTop` field**

In the `Model` struct in `model.go`, add (next to other layout fields like `width, height`):
```go
	// scrollbarTop is the absolute screen row of the viewport's first line,
	// used to hit-test scrollbar mouse events. Set in relayout().
	scrollbarTop int
```

- [ ] **Step 2: Reserve the bar column and store the top row in `relayout()`**

In `relayout()`, change the viewport width setter from:
```go
	m.viewport.SetWidth(contentW)
```
to:
```go
	m.viewport.SetWidth(contentW - 1) // reserve the right column for the scrollbar
```
And immediately after `splashH` is computed (before `bodyH`), add:
```go
	// Viewport's first screen row = header (1) + divider (1) + splash height.
	m.scrollbarTop = 2 + splashH
```

- [ ] **Step 3: Add the `renderViewportWithScrollbar` helper**

Add to `model.go` (near `renderEntry`/`refreshViewport`):
```go
// renderViewportWithScrollbar renders the chat viewport with a one-column
// vertical scrollbar on its right edge. The bar paints a thumb (█) + track (░)
// in subtle greys only when the content overflows; otherwise the reserved
// column is blank, so the bar appears and disappears without reflowing text.
func (m Model) renderViewportWithScrollbar() string {
	body := m.viewport.View()
	lines := strings.Split(body, "\n")
	height := m.viewport.Height()
	col := scrollbarColumn(m.viewport.TotalLineCount(), height, m.viewport.YOffset())
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		// Guard against any row-count mismatch between the rendered body and
		// the computed column.
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(m.styles.Border.Render("█"))
			case '░':
				b.WriteString(m.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Use the helper in `View()`**

In `View()`, in the `default:` branch of the overlay `switch`, change:
```go
	default:
		parts = append(parts, m.viewport.View())
```
to:
```go
	default:
		parts = append(parts, m.renderViewportWithScrollbar())
```
(Leave the `m.recap` append that follows it unchanged.)

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: clean build; all packages PASS (no behavior tests change here — correctness of the bar is the Task 1 geometry plus a human smoke test). Confirm `go vet ./...` is clean too.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/cli/ui/model.go
git commit -m "feat(tui): render vertical scrollbar on the chat viewport"
```

---

### Task 3: Mouse click/drag to scroll

Add drag state and three mouse cases. The drag math reuses `scrollOffsetFromClick` (already tested in Task 1); the interaction itself is verified by a human smoke test.

**Files:**
- Modify: `source/server/internal/cli/ui/model.go` (add `scrollbarDragging` field; add mouse cases to `Update`)

**Interfaces:**
- Consumes: `scrollOffsetFromClick(clickRow, top, height, total int) int` (Task 1); model field `scrollbarTop` (Task 2).

- [ ] **Step 1: Add the drag-state field**

In the `Model` struct, add (next to `scrollbarTop`):
```go
	// scrollbarDragging is true while the user holds the mouse on the
	// scrollbar; motion events then scrub the viewport scroll position.
	scrollbarDragging bool
```

- [ ] **Step 2: Confirm the v2 Mouse field names**

Run: `go doc charm.land/bubbletea/v2 Mouse`
Confirm the `Mouse` struct exposes integer `X` and `Y` (column, row) fields. Use whatever the doc shows (expected `X`, `Y`). `msg.Mouse()` returns the `Mouse` value for `MouseClickMsg` / `MouseMotionMsg` / `MouseReleaseMsg`.

- [ ] **Step 3: Add the mouse cases to `Update`**

In `Update`'s `switch msg := msg.(type)` block, next to the existing `case tea.MouseWheelMsg:`, add:
```go
	case tea.MouseClickMsg:
		if m.editorActive || m.historyActive || m.pendingConfirm != nil {
			return m, nil
		}
		mouse := msg.Mouse()
		height := m.viewport.Height()
		onBar := mouse.X == m.width-1 &&
			mouse.Y >= m.scrollbarTop && mouse.Y < m.scrollbarTop+height
		if onBar {
			m.scrollbarDragging = true
			off := scrollOffsetFromClick(mouse.Y, m.scrollbarTop, height, m.viewport.TotalLineCount())
			m.viewport.SetYOffset(off)
		}
		return m, nil

	case tea.MouseMotionMsg:
		if !m.scrollbarDragging {
			return m, nil
		}
		if m.editorActive || m.historyActive || m.pendingConfirm != nil {
			m.scrollbarDragging = false
			return m, nil
		}
		mouse := msg.Mouse()
		height := m.viewport.Height()
		off := scrollOffsetFromClick(mouse.Y, m.scrollbarTop, height, m.viewport.TotalLineCount())
		m.viewport.SetYOffset(off)
		return m, nil

	case tea.MouseReleaseMsg:
		m.scrollbarDragging = false
		return m, nil
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./... -count=1 && go vet ./...`
Expected: clean build, all PASS, vet clean. If `mouse.X`/`mouse.Y` don't compile, use the field names from Step 2's `go doc` output.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/ui/model.go
git commit -m "feat(tui): click and drag the scrollbar to scroll"
```

---

### Task 4: Final gate + manual smoke

- [ ] **Step 1: Full gate**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean across the board.

- [ ] **Step 2: Build the runnable binary**

Run: `go build -o bin/cercano ./cmd/cercano/`
Expected: binary at `source/server/bin/cercano`.

- [ ] **Step 3: Manual smoke checklist** (human-driven; can't be automated)

Launch `bin/cercano` against a running agent and fill the buffer past one screen:
- Bar appears at the right edge of the chat area; thumb size ≈ visible/total; thumb position tracks scroll.
- Mouse wheel and pgup/pgdn / shift+arrows still scroll, and the thumb follows.
- Click in the track trough jumps the view to that spot.
- Drag the thumb up/down — the view scrubs with it; release stops.
- Shrink the buffer (e.g. `/clear`) until content fits — the bar disappears (blank column) with NO text reflow; it reappears when content overflows again.
- Open `/config` and `/history` — dragging/clicking the bar does nothing; close them and it works again.
- Resize the terminal — the bar repaints at the new height/position, text stays one column clear of it.

- [ ] **Step 4: Report**

Confirm `go test ./...` green and the smoke results. Do NOT push — leave branch `tui-scrollbar` for the user to review and merge.

---

## Self-Review notes

- **Spec coverage:** geometry (Task 1: `scrollbarThumb`/`scrollbarColumn`/`scrollOffsetFromClick`); overflow-only + reserved column + subtle greys rendering (Task 2: `relayout` width−1, `renderViewportWithScrollbar`); click/drag with overlay gating (Task 3: three mouse cases + `scrollbarDragging`); testing + smoke (Task 4). All spec sections map to a task.
- **Type consistency:** `scrollbarColumn(total, height, yOffset)` and `scrollOffsetFromClick(clickRow, top, height, total)` signatures are identical in Tasks 1, 2, 3. `scrollbarTop` defined in Task 2, consumed in Task 3. `scrollbarDragging` defined and used in Task 3.
- **One verify-at-build point** (Task 3 Step 2): the `Mouse` struct field names — confirmed via `go doc`, with the build as the backstop.
