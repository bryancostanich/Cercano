# Native Prompt Textarea

## Overview

The Cercano CLI prompt needs to behave like a native macOS text field while still
living inside the Bubble Tea TUI. The current prompt uses
`charm.land/bubbles/v2/textarea.Model`, configured as a dynamic-height multiline
input. It gives us wrapping, cursor positioning, and cursor-following behavior,
but it does not expose enough API for native-feeling prompt interaction:

- no text selection model or selection rendering
- no undo/redo stack
- no cursor-independent scroll setter
- mouse wheel scrolling is clamped to the textarea's rendered buffer, including
  trailing end-of-buffer padding rows, not the real editable content

This spec defines a local Cercano prompt textarea widget first, then a later path
for extracting the proven behavior into a focused upstream Bubbles proposal/PR.

## Goals

- Preserve the existing prompt look and behavior:
  - first visual row uses the lime `▶ ` prompt
  - continuation/wrapped rows use a two-space hang indent
  - amber prompt text and muted placeholder
  - no cursor-line highlight or end-of-buffer glyph
  - dynamic height grows from 1 to 6 rows, then scrolls internally
  - `Enter` submits and `Shift+Enter` inserts a newline
- Add native-feeling prompt text selection:
  - mouse drag selection
  - keyboard selection extension
  - visual highlight across wrapped rows and scroll positions
  - typing, paste, backspace, and delete replace/remove selected text
  - `Cmd+C` copies selection when the terminal forwards it
  - `Ctrl+C` copies selection when a prompt selection exists; otherwise it keeps
    the current clear/quit behavior
- Add macOS-style navigation:
  - arrow keys move by character or visual row
  - `Option+Left/Right` moves by word
  - `Cmd+Left/Right` moves to logical line start/end
  - `Cmd+Up/Down` moves to document start/end
  - Shift-modified forms extend the active selection
  - `Cmd+A` selects all prompt text
- Add clean mouse-wheel scrolling over the prompt:
  - wheel only affects the prompt when the pointer is over prompt rows
  - wheel scrolls the view without moving the insertion cursor
  - scroll offset clamps to real visual content rows:
    `0..totalVisualRows-height`
  - no scrolling into blank padding/end-of-buffer rows
- Add undo/redo:
  - `Cmd+Z` undo
  - `Cmd+Shift+Z` redo
  - paste is one undo step
  - selection replacement is one undo step
  - normal char typing coalesces into a single undo group until a boundary
  - adjacent backspace/delete runs coalesce by direction

## Non-Goals

- Do not replace the chat scrollback viewport selection in this track.
- Do not replace Bubble Tea or Lip Gloss.
- Do not add a module-level `replace` to a forked Bubbles dependency as the first
  implementation step.
- Do not require users to remap macOS Terminal.app shortcuts. Terminal.app may
  reserve `Cmd+C`; the prompt should degrade gracefully with `Ctrl+C` for prompt
  selections and paste/copy fallbacks where possible.

## Current Constraints

`bubbles/v2@v2.1.0` textarea owns an unexported internal `viewport.Model`.
Public scroll APIs are getters only:

- `ScrollYOffset()`
- `ScrollPercent()`

There is no public `SetScrollYOffset`, `ScrollUp`, `ScrollDown`, or
`MaxScrollYOffset` API on `textarea.Model`.

The widget's internal renderer intentionally appends `height` end-of-buffer rows
after the real content. The internal viewport correctly clamps to that rendered
buffer, but that is not the same as clamping to the editable visual rows. This is
why raw `MouseWheelMsg` forwarding can scroll into blank-looking padding rows.

Cursor following is still desirable for cursor-affecting actions. The problem is
not cursor following itself; the missing distinction is:

- editing/navigation/selection-caret movement should keep the cursor visible
- pure mouse-wheel view scrolling should be cursor-independent

## Recommended Architecture

Implement a local prompt widget inside the CLI first, shaped like the subset of
`textarea.Model` that `model.go` already consumes. Suggested location:

```text
source/clients/cli/internal/ui/prompt_input.go
```

The widget should not embed `textarea.Model`. It should own:

- raw text buffer
- cursor offset
- optional selection anchor
- dynamic height
- prompt-local scroll offset
- visual layout cache
- undo/redo stacks
- real terminal cursor coordinates

Keep the public surface intentionally familiar:

```go
type promptInput struct { ... }

func newPromptInput() promptInput
func (p *promptInput) Focus() tea.Cmd
func (p *promptInput) Blur()
func (p promptInput) Value() string
func (p *promptInput) SetValue(string)
func (p *promptInput) InsertString(string)
func (p *promptInput) SetWidth(int)
func (p promptInput) Height() int
func (p promptInput) View() string
func (p promptInput) Cursor() *tea.Cursor
func (p promptInput) Update(tea.Msg) (promptInput, tea.Cmd)
```

Add prompt-specific APIs where the root model needs them:

```go
func (p promptInput) HasSelection() bool
func (p promptInput) SelectedText() string
func (p promptInput) ScrollYOffset() int
func (p promptInput) MaxScrollYOffset() int
func (p *promptInput) ScrollView(delta int)
func (p *promptInput) MouseDown(x, y int)
func (p *promptInput) MouseDrag(x, y int)
func (p *promptInput) MouseUp(x, y int)
```

Keep compatibility methods that nearby code may use:

```go
func (p promptInput) Line() int
func (p promptInput) LineCount() int
func (p *promptInput) CursorStart()
func (p *promptInput) CursorEnd()
```

## Layout Model

Layout is the heart of the widget. It should operate on real editable content,
not rendered padding.

Each layout pass converts the text buffer into visual rows:

```go
type promptRow struct {
    Start int    // rune offset inclusive
    End   int    // rune offset exclusive
    Text  string // row text without prompt prefix
}
```

Rules:

- `textWidth = max(1, width - promptWidth)`
- hard newlines create new logical lines
- long logical lines wrap into visual rows
- wide runes use display width, not byte count
- the empty buffer still produces one visual row
- dynamic height is:
  `clamp(totalVisualRows, MinHeight, MaxHeight)`
- max scroll is:
  `max(0, totalVisualRows - height)`

Rendering should produce exactly `height` rows. Padding rows may be rendered when
content is shorter than the visible height, but those rows must not be part of
the scrollable content model.

## Cursor Behavior

Cursor-following should be retained for cursor-affecting actions:

- typed text
- paste
- delete/backspace
- keyboard navigation
- keyboard selection extension
- mouse click/drag selection
- resize/rewrap when needed

Pure prompt wheel scrolling is the exception. Wheel changes only `scrollY`, and
does not move `cursor`. If the cursor becomes offscreen after wheel scrolling,
`Cursor()` may return nil or the root view may omit the cursor until the next
cursor-affecting action. The next edit/navigation action should call
`ensureCursorVisible()`.

Visual up/down navigation should preserve a goal column. The goal column resets
on horizontal movement, text edits, mouse clicks, and selection changes.

## Selection

Selection should be offset-based:

```go
selectionAnchor int // -1 means no selection
cursor          int // selection caret when anchor is active
```

Selection range is `min(anchor,cursor)..max(anchor,cursor)`.

Keyboard behavior:

- unmodified navigation clears selection
- Shift navigation extends selection
- if Shift begins selection, anchor is the old cursor position
- typing/paste replaces selection
- backspace/delete removes selection before single-character deletion
- `Cmd+A` selects all

Mouse behavior:

- left click in prompt rows sets cursor and starts a selection drag
- drag updates cursor while preserving anchor
- drag above/below the prompt auto-scrolls by one row per motion event
- release finalizes selection
- an empty click clears selection

Rendering:

- apply a selection style per visible row
- selection must work across hard newlines, soft wraps, and scroll offsets
- selection should not affect raw copied text

## Keyboard Mapping

Use Bubble Tea v2 key fields, not only strings, because Kitty-enhanced terminals
may report richer modifier metadata.

Treat Command as:

```go
key.Mod.Contains(tea.ModSuper) || key.Mod.Contains(tea.ModMeta)
```

Mapping:

| Gesture | Behavior |
| --- | --- |
| `Left` / `Right` | move one rune |
| `Up` / `Down` | move one visual row, preserving goal column |
| `Option+Left` / `Option+Right` | previous/next word |
| `Cmd+Left` / `Cmd+Right` | logical line start/end |
| `Cmd+Up` / `Cmd+Down` | document start/end |
| `Shift+...` | same movement, extend selection |
| `Cmd+A` | select all |
| `Cmd+C` | copy prompt selection when terminal forwards it |
| `Ctrl+C` | copy prompt selection when active; otherwise existing clear/quit |
| `Cmd+Z` | undo |
| `Cmd+Shift+Z` | redo |

The root model should stop stealing `Shift+Up` and `Shift+Down` for chat
scrollback while the prompt owns focus, because those are selection gestures.
Chat scrollback can keep page keys and explicit viewport shortcuts.

## Undo/Redo

The prompt needs a real undo manager because stock `textarea.Model` has none.

Use snapshots initially. Prompt text is small enough that this is simpler and
safer than command-delta undo for V1:

```go
type promptSnapshot struct {
    Text            string
    Cursor          int
    SelectionAnchor int
}

type undoRecord struct {
    Kind   string
    Before promptSnapshot
    After  promptSnapshot
}
```

Undo groups:

- paste: one group
- selection replacement: one group
- `Shift+Enter` newline: one group
- typing: coalesced while contiguous and uninterrupted
- backspace: coalesced while adjacent and same direction
- delete: coalesced while adjacent and same direction

Undo boundaries:

- cursor movement
- selection change
- mouse click/drag
- paste
- newline
- switching delete direction
- any edit after undo clears redo

Selection/cursor restoration after undo should be deterministic and tested. For
V1, restoring the exact pre-edit cursor and selection snapshot is acceptable.

## Root Model Integration

In `source/clients/cli/internal/ui/model.go`:

- replace `textarea.Model` with `promptInput`
- preserve existing constructor styling and prompt text
- preserve `Enter` submit and `Shift+Enter` newline behavior
- route `tea.PasteMsg` into the prompt
- route normal `tea.KeyPressMsg` into the prompt after existing TUI-level gates
- when prompt has a selection, route `Ctrl+C` to prompt copy before root
  clear/quit handling
- hit-test prompt rows for mouse events:
  - wheel over prompt -> `input.ScrollView(...)`
  - click/drag/release over prompt -> prompt selection
  - wheel outside prompt -> chat viewport scroll
  - scrollbar drag remains chat viewport behavior

Prompt top can be computed from the same rows used in `View()`:

1. header/divider/splash
2. chat viewport height
3. optional recap row
4. prompt border
5. optional slash suggestions

## Upstream Bubbles Path

After the local widget proves out, split learnings into upstreamable pieces.

Likely PR order:

1. Add cursor-independent textarea scroll APIs:
   - `MaxScrollYOffset() int`
   - `SetScrollYOffset(int)`
   - `ScrollView(delta int)`
   - clamp to real visual content, not end-of-buffer padding
2. Add selection primitives:
   - selection anchor/caret
   - selected text accessor
   - selection rendering hooks/styles
3. Add undo/redo as a separate proposal if maintainers want it in core.

The local Cercano widget should avoid depending on private Bubbles internals so
the upstream proposal can be informed by behavior and tests rather than a
fragile fork.

## Testing Plan

### Unit Tests: Layout

- empty prompt height is 1
- hard newlines grow height
- long lines soft-wrap at text width
- height caps at 6
- deleting text shrinks height
- resize rewraps and clamps scroll
- wide Unicode runes preserve cursor/display columns

### Unit Tests: Scrolling

- wheel over prompt scrolls prompt view
- wheel over prompt does not move cursor
- wheel clamps at top and bottom
- bottom clamp is `totalVisualRows-height`, not rendered padding
- no blank EOB rows become scrollable
- wheel outside prompt scrolls chat viewport

### Unit Tests: Navigation

- left/right one-rune movement
- up/down visual-row movement with goal column
- wrapped-line up/down movement
- `Option+Left/Right` word movement
- `Cmd+Left/Right` logical line start/end
- `Cmd+Up/Down` document start/end
- unmodified movement clears selection

### Unit Tests: Selection

- `Shift+Left/Right` extends by rune
- `Shift+Up/Down` extends by visual row
- `Shift+Option+Left/Right` extends by word
- `Shift+Cmd+Left/Right` extends to logical line start/end
- `Shift+Cmd+Up/Down` extends to document start/end
- `Cmd+A` selects all
- selection renders across wrapped rows
- mouse drag selects text
- dragging outside prompt auto-scrolls
- typing replaces selection
- paste replaces selection
- backspace/delete remove selection
- copy returns the exact selected plain text

### Unit Tests: Undo/Redo

- typing `abc`, then `Cmd+Z`, clears all coalesced typing
- paste multiline text, then `Cmd+Z`, removes the whole paste in one step
- `Cmd+Shift+Z` redoes the paste
- replacing a selection is one undo step
- `Shift+Enter` newline is one undo step
- backspace runs coalesce
- delete runs coalesce
- new edit after undo clears redo
- undo restores cursor/selection snapshots

### Integration Tests

- prompt still submits on `Enter`
- `Shift+Enter` inserts newline and grows height
- root `Ctrl+C` copies prompt selection before clear/quit
- root `Ctrl+C` still clears non-empty prompt when no prompt selection exists
- prompt height changes still resize chat viewport correctly
- slash suggestions still render above prompt
- prompt mouse events do not interfere with chat scrollbar drag

### Manual Checklist

Run:

```bash
cd source/server
go build -o bin/cercano ./cmd/cercano/
bin/cercano --mdtest
```

Verify:

- type a long multiline message
- prompt grows from 1 to 6 rows
- after 6 rows, prompt scrolls internally
- mouse wheel over prompt scrolls cleanly both directions
- wheel does not move insertion cursor
- wheel does not scroll into blank padding rows
- wheel outside prompt scrolls chat buffer
- `Enter` submits
- `Shift+Enter` inserts newline
- macOS navigation works:
  - `Option+Left/Right`
  - `Cmd+Left/Right`
  - `Cmd+Up/Down`
- Shift-selection variants work
- mouse selection highlights correctly
- `Cmd+C` copies selection in terminals that forward it
- `Ctrl+C` copies prompt selection in macOS Terminal fallback flow
- `Cmd+Z` undoes
- a paste reverts in one `Cmd+Z`
- `Cmd+Shift+Z` redoes
- resize the terminal while prompt has wrapped text and selection

## Risks and Open Questions

- Grapheme clusters: V1 can be rune-based, but emoji/combining marks may need a
  grapheme-aware buffer before upstreaming.
- Terminal key reporting varies. `Cmd` behavior should work where Kitty keyboard
  enhancements are forwarded; Terminal.app may reserve `Cmd+C`.
- Plain-text copy should not include ANSI styling.
- Selection rendering over wide runes needs dedicated tests.
- The upstream Bubbles API may prefer exported methods on `textarea.Model` over a
  new widget or a broader selection model.
