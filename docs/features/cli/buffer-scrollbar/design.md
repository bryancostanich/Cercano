# Buffer Viewport Scrollbar — Design

## Goal

Add a vertical scrollbar to the chat buffer (the scrollback `viewport.Model`) in
the cercano-cli TUI. It shows the current scroll position, appears only when the
content overflows, and can be dragged with the mouse to scroll.

## Decisions (locked)

- **Visibility:** overflow-only. The right-hand column is *always reserved* (so
  chat text never reflows when the bar appears/disappears), but the track + thumb
  are painted only when `TotalLineCount > Height`. When content fits, the column
  is blank.
- **Style:** subtle greys. Thumb `█` in border grey `#6F6F6F` (`palette.Border`),
  track `░` in dim grey `#434343` (`palette.BorderDim`).
- **Interaction:** click/drag to scroll. Clicking or dragging the thumb (or
  clicking the track trough) scrolls the viewport to that position. Mouse wheel,
  pgup/pgdn, and shift+arrows continue to work unchanged.

## Approach

The bubbles v2 `viewport` has no native scrollbar, and its gutter system
(`LeftGutterFunc`) is left-side per-line text, not a draggable thumb — unfit. So
the scrollbar is rendered manually as a one-column vertical bar on the right edge
of the viewport block, with geometry computed from the viewport's live metrics:
`TotalLineCount()`, `Height()`, `YOffset()`/`ScrollPercent()`.

## Components

### 1. `internal/cli/ui/scrollbar.go` (new) — pure geometry, unit-tested

```
// scrollbarThumb returns the thumb's row range within a bar of `height` rows.
// ok=false means no overflow (total <= height) → caller paints a blank column.
func scrollbarThumb(total, height, yOffset int) (thumbTop, thumbSize int, ok bool)
//   thumbSize = max(1, round(height * height / total))
//   maxTop    = height - thumbSize
//   frac      = yOffset / (total - height)   // clamped 0..1
//   thumbTop  = round(frac * maxTop)

// scrollOffsetFromClick maps an absolute screen row to a clamped viewport YOffset.
func scrollOffsetFromClick(clickRow, top, height, total int) int
//   frac   = (clickRow - top) / max(1, height-1)   // clamped 0..1
//   offset = round(frac * (total - height))         // clamped 0..(total-height)
```

Both functions are pure (ints in, ints out) and fully unit-tested. No rendering,
no model state.

### 2. `internal/cli/ui/model.go` — integration

- **`relayout()`**: set viewport width to `contentW - 1` (reserve the bar column).
  Content rewraps one column narrower automatically because `renderEntry` reads
  `m.viewport.Width()`. Store the viewport's absolute top screen row in a new
  field `scrollbarTop = 2 + splashH` (header + divider + splash height) for mouse
  hit-testing. Viewport height for hit-testing is read live via
  `m.viewport.Height()`.
- **New model field**: `scrollbarDragging bool`.
- **`View()`**: replace the bare `parts = append(parts, m.viewport.View())` with a
  composite that appends the bar column to each viewport row. A helper
  `renderViewportWithScrollbar() string` splits `m.viewport.View()` into lines
  (each `contentW-1` wide, viewport-padded) and appends, per row, the styled bar
  glyph from `scrollbarThumb`. Thumb rows → `Border`-styled `█`; other rows →
  `BorderDim`-styled `░`; when `ok == false` (no overflow) → a plain space. Result
  width = `(contentW-1) + 1 = contentW = m.width`. The bar spans only viewport
  rows; dividers/header/input/status stay full width.
- **`Update()`**: add three mouse cases, each gated off when
  `m.editorActive || m.historyActive || m.pendingConfirm != nil` (same gate as the
  existing wheel case, since the viewport isn't the active surface then):
  - `tea.MouseClickMsg`: if the click is on the bar column (`X == m.width-1`) within
    viewport rows (`scrollbarTop <= Y < scrollbarTop + viewport.Height()`), set
    `scrollbarDragging = true` and `m.viewport.SetYOffset(scrollOffsetFromClick(Y, scrollbarTop, height, total))`.
    Otherwise no-op (don't swallow — return `m, nil`).
  - `tea.MouseMotionMsg`: if `scrollbarDragging`, recompute and `SetYOffset` from the
    current `Y` (clamped). Else no-op.
  - `tea.MouseReleaseMsg`: clear `scrollbarDragging`.
  Mouse mode is already `MouseModeCellMotion` (motion reported while a button is
  held), so drag tracking works without further program config.

### 3. Styling

Reuse `theme.Styles` greys (`Border`, `BorderDim`) — no palette changes. Glyphs:
thumb `█`, track `░`, empty space when no overflow.

## Data flow

```
viewport metrics (TotalLineCount, Height, YOffset)
        │
        ├── View(): scrollbarThumb(...) → per-row glyph → composite onto viewport block
        │
        └── relayout(): store scrollbarTop; reserve bar column
mouse event (Click/Motion/Release)
        │
        └── Update(): hit-test bar column → scrollOffsetFromClick(...) → viewport.SetYOffset
```

## Edge cases

- **No overflow** (`total <= height`): `scrollbarThumb` returns `ok=false`; the bar
  column renders as blank spaces; drag hit-tests still compute but `SetYOffset` is a
  no-op clamp (offset 0). No thumb to grab.
- **Thumb min size**: `max(1, …)` guarantees the thumb is always at least 1 row when
  shown, even for very long buffers.
- **AtBottom / AtTop**: `frac` clamps to 0..1, so the thumb sits flush at top/bottom.
- **Narrow terminal**: `contentW` is already clamped to a minimum of 20; reserving 1
  column leaves ≥19 for content. No special-casing.
- **Resize**: `relayout` recomputes width and `scrollbarTop`; the next `View` repaints
  the bar at the new geometry.
- **Overlay / pending-confirm active**: viewport not the active surface; mouse drag
  gated off; the bar still renders if the viewport is shown beneath, which is fine.

## Testing

- **Unit (`scrollbar_test.go`)**: `scrollbarThumb` — no-overflow returns ok=false;
  full-buffer top (yOffset 0 → thumbTop 0); bottom (yOffset = total-height → thumb
  flush at bottom); a mid position; min thumb size for a very large total. And
  `scrollOffsetFromClick` — top row → 0; bottom row → total-height; mid → midpoint;
  out-of-range rows clamp.
- **Manual smoke** (interactive, machine can't drive a live mouse): with an
  overflowing buffer, confirm the bar appears at the right edge; the thumb size and
  position track the content; click in the trough jumps; drag the thumb scrolls;
  the bar vanishes (blank column, no reflow) when content fits; mouse wheel and keys
  still scroll; bar does nothing while `/config`/`/history`/a confirm is open.

## Process

Implemented in worktree `Cercano-scrollbar` (branch `tui-scrollbar`, cut from
`main` at `b0d9f63`). Merged locally after the manual smoke test. Not pushed.
