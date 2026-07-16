# Virtualized chat viewport design

## Problem

The current chat viewport still treats the transcript as one giant rendered string. Per-entry and prefix caches avoid some Markdown rendering, but `chatView.SetEntries` still assembles the full transcript and calls Bubble viewport `SetContent`. Bubble viewport then splits and scans the full string. Super-long conversations therefore make ordinary repaint and streaming updates scale with total transcript size.

The `CERCANO - MISTRAL INTEGRATION` conversation is the motivating case: roughly 1,324 visible turns, 708k visible characters, and 6,050 hard lines. A 30-row terminal should not touch thousands of offscreen lines on every repaint.

## Goal

Ordinary chat repaint, scrolling, mouse interaction, and streaming updates must not assemble, split, strip, or scan the full rendered transcript. Work should scale with the visible window plus the active dynamic unit.

## Non-goals for the first cut

- Lazy unknown line counts. The first implementation may render units once on layout rebuild to get exact line counts.
- Rewriting Markdown rendering. Existing entry, tool, and streaming renderers should be reused initially.
- Changing conversation persistence or compaction behavior.

## Target architecture

Replace the current pipeline:

```text
entries -> full rendered transcript string -> Bubble viewport SetContent -> visible rows
```

with:

```text
entries -> render units -> line-indexed transcript layout -> visible rows only
```

`chatView` will own scroll state directly through a small `virtualScroll` type. Bubble viewport should no longer own transcript content.

## Core types

### virtualScroll

`virtualScroll` mirrors the small Bubble viewport surface that `chatView` depends on:

- width and height
- total line count
- y offset
- clamp, bottom, and scroll helpers

It does not own rendered content.

### renderUnit

A render unit is a display-layout unit rather than always one raw entry. Unit kinds should include:

- normal entry
- contiguous tool group
- trailing activity line

Each unit records start entry, end entry, start line, line count, and cached rendered lines.

### transcriptLayout

The layout owns the ordered units, exact line counts, and prefix-sum line index. It supports binary search by absolute line and visible range queries.

## Rendering flow

`View()` should render exactly the currently visible line range:

1. Read `top := YOffset()` and `height := Height()`.
2. Locate render units intersecting `[top, top+height)`.
3. Materialize only those styled lines.
4. Overlay selection for visible absolute lines.
5. Pad/truncate to viewport width.
6. Append the scrollbar column from virtual total lines.

## Selection and copy

Selection coordinates stay as absolute line and column pairs. Copy should gather plain lines only for the selected range, not strip ANSI across the whole transcript. `PlainLines()` may remain as an expensive compatibility helper for tests and debugging.

## Tool fold hit testing

Global `arrowRows` should become unit-local arrow metadata. `arrowRowAt(absLine, x)` locates the unit for `absLine`, converts to a local line, and checks that unit's local rows. This preserves current click semantics without scanning a global full-transcript row list.

## Migration phases

1. Add `virtualScroll` with tests.
2. Add render-unit layout alongside the existing giant-string path.
3. In tests, prove flattened virtual lines match current full transcript output.
4. Switch `View()` to render visible lines from the layout.
5. Remove `viewport.SetContent` and giant transcript assembly from `SetEntries`.
6. Replace `chatView.Update` with local scroll-key handling.
7. Move selection copy to range-based plain-line access.
8. Move fold hit testing to unit-local arrow rows.
9. Delete obsolete prefix/full-content caches.

## Completion criteria

- `SetEntries` does not assemble the full transcript string.
- `SetEntries` does not call Bubble viewport `SetContent`.
- `View()` does not call Bubble viewport `View`.
- Streaming updates do not split the full transcript.
- Selection copy does not strip the full transcript except through explicit compatibility helpers.
- Scrollbar, resize anchoring, selection, tool folds, banner animation, and confirm-prompt scrolling retain existing behavior.
- UI tests pass and a structural regression test proves visible rendering does not visit all offscreen units.
