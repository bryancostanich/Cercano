# Virtualized chat viewport design

## Problem

The old chat viewport treated the transcript as one giant rendered string. Per-entry and prefix caches avoided some Markdown rendering, but `chatView.SetEntries` still assembled the full transcript and called Bubble viewport `SetContent`. Bubble viewport then split and scanned the full string. Super-long conversations therefore made ordinary repaint and streaming updates scale with total transcript size.

The `CERCANO - MISTRAL INTEGRATION` conversation is the motivating case: roughly 1,324 visible turns, 708k visible characters, and 6,050 hard lines. A 30-row terminal should not touch thousands of offscreen lines on every repaint.

## Goal

Ordinary chat repaint, scrolling, mouse interaction, and streaming updates must not assemble, split, strip, or scan the full rendered transcript. Work should scale with the visible window plus the active dynamic unit.

## Implemented architecture

The CLI chat view now uses this pipeline:

```text
entries -> render units -> line-indexed transcript layout -> visible rows only
```

`chatView` owns scroll state directly through `virtualScroll`. Bubble viewport no longer owns chat transcript content.

## Core types

### `virtualScroll`

`virtualScroll` mirrors the small scroll surface that `chatView` depends on:

- width and height,
- total line count,
- y offset,
- clamp, bottom, and scroll helpers.

It does not own rendered content.

### `renderUnit`

A render unit is a display-layout unit rather than always one raw entry. Current unit kinds are:

- normal entry,
- contiguous tool group,
- separator row,
- trailing activity line.

Each unit records start entry, end entry, start line, line count, pre-split styled lines, and unit-local fold-arrow metadata.

### `transcriptLayout`

The layout owns the ordered units, exact line counts, and prefix-sum line index. It supports binary search by absolute line, visible range queries, single-line lookup, and unit-local fold hit-testing.

## Rendering flow

`View()` renders exactly the currently visible line range:

1. Read `top := YOffset()` and `height := Height()`.
2. Locate render units intersecting `[top, top+height)`.
3. Materialize only those styled lines.
4. Overlay selection for visible absolute lines.
5. Pad/truncate to viewport width.
6. Append the scrollbar column from virtual total lines.

## Selection and copy

Selection coordinates stay as absolute line and column pairs. Copy gathers plain lines only for the selected range, not by stripping ANSI across the whole transcript. `PlainLines()` remains as an expensive compatibility helper for tests and debugging.

## Tool fold hit testing

Fold arrows are stored as unit-local metadata. `arrowRowAt(absLine, x)` locates the unit for `absLine`, converts to a local line, and checks that unit's local rows. This preserves current click semantics without scanning a global full-transcript row list.

## Cache behavior

Frozen entry and completed tool-group caches store both the rendered block and its pre-split line slice. Layout rebuilds can therefore reuse cached lines directly without repeatedly splitting historical blocks. Dynamic units still render and split only themselves:

- shimmering banner,
- streaming assistant tail,
- animated/in-progress tool groups,
- trailing activity line.

The obsolete assembled transcript-prefix cache was removed because there is no longer a full transcript string to assemble.

## Current guarantees

- `SetEntries` does not assemble the full transcript string.
- `SetEntries` does not call Bubble viewport `SetContent`.
- `View()` does not call Bubble viewport `View`.
- Streaming updates do not split cached historical transcript blocks.
- Selection copy strips only the selected range except through explicit `PlainLines()` compatibility helpers.
- Fold hit-testing uses unit-local row metadata.
- Scrollbar, resize anchoring, selection, tool folds, banner animation, and confirm-prompt scrolling are covered by the existing UI tests.

## Remaining possible follow-up

The first cut still performs an exact layout rebuild over the entry list when `SetEntries` is called. Cached historical units avoid Markdown rendering and repeated splitting, but the layout pass is still linear in the number of display units. If this becomes measurable on much larger transcripts, the next step is incremental layout reuse for unchanged prefix units.
