# History View — Design

**Status:** Design approved 2026-06-24. Implementation not started.

Resuming a conversation (`cercano -r`, or `/history` mid-session) currently opens
the history picker as a bordered `overlay.RowList` panel that replaces the main
content region. That panel has three defects:

1. Long rows wrap and collide (no width-aware truncation; two columns crammed
   on one line).
2. Its scrollbar does not work, and the panel renders **all** rows — so the
   block extends past the bottom of the terminal.
3. The layout cramps the conversation name and its context preview onto one
   line.

This redesign **drops the bordered panel for history** and renders the history
list as content in the **main chat viewport**, which already has a working,
height-bounded scrollbar. The conversation name and context preview move to two
lines, and each row gains a clickable expand arrow that opens a richer preview.

## Goals

- Render history in the main section (no bordered "history block").
- Lead with a markdown `# History` heading, then the conversation list.
- Reuse the main viewport's working scrollbar; never overflow the terminal.
- Two-line rows: name on line 1, indented context preview on line 2.
- A clickable `▸`/`▾` arrow per row that expands an inline drawer showing the
  full recap plus the last few turns.

## Non-goals

- No change to `overlay.RowList` or the `/config` / `/models` panels — they stay
  one-line bordered overlays. History stops using `RowList` entirely.
- No new gRPC: expanded turns load via the existing
  `agentclient.GetConversationTurns`.
- No change to `applyResume` selection semantics beyond what already landed in
  commit `d2a5479` (empty tool-turn skip).

## Architecture

### History mode in the root model

History is a **mode** of the main session model, not a `contentPage`. A new
`historyView` unit (`internal/ui/history_view.go`) owns the mode's state and
renders into the chat viewport:

- `rows []historyRow` — one per conversation (`id`, `name`, `recap`, `meta`,
  `expanded bool`, lazily-fetched `turns []agentclient.ContextTurn`,
  `turnsLoaded bool`).
- `cursor int` — the selected conversation.
- Pure builder `func (h historyView) content(width int) string` — produces the
  viewport body: a markdown `# History` heading followed by each row's lines.

The root model:

- Enters history mode on `-r` (launch) or `/history`. It builds `historyView`,
  sets the viewport content to `h.content(width)`, and flags `historyMode`.
- While `historyMode`, routes keys/mouse to `historyView`, re-renders the
  viewport content after each change, and keeps the existing
  `renderViewportWithScrollbar` for paint (working scrollbar, height clamp).
- Leaves history mode on `enter` (resume the selected conversation via
  `applyResume`, which clears entries and rehydrates) or `esc`/`q` (restore the
  prior chat scrollback).

`historyView` does not own a viewport or scrollbar — it only produces content
lines. Scrolling, height-clamping, wheel, drag, and the scrollbar column are the
main viewport's existing responsibilities, unchanged.

### Row rendering

Collapsed (two lines):

```
 ▸  read the cercano readme and familiarize yourself…   14 turns · 1h ago · opus-4-7
      Familiarized with Cercano's CLI/Agent architecture and identified open MCP host…
```

- Line 1: arrow + name (truncated) + right-aligned meta (`N turns · rel-time ·
  model`). The selected row is highlighted.
- Line 2: indented recap preview, truncated to width.

Expanded (`▾`): below the recap line, an indented drawer shows the full recap
(wrapped) then `recent:` and the last 3 turns as `role · preview` (each clipped).
Until the turn fetch resolves, the drawer shows `loading…`.

### Interaction

| Input | Action |
|---|---|
| `↑` / `↓` | Move selection; viewport auto-scrolls to keep the row visible |
| `→` / `←` | Expand / collapse the selected row |
| click arrow | Toggle expand for that row |
| `enter` | Resume the selected conversation, exit history mode |
| `esc` / `q` | Exit history mode, restore prior chat scrollback |
| wheel / scrollbar drag | Scroll the viewport (existing handlers) |

Mouse mapping: a left click at screen row `Y` maps to a content line via the
viewport `YOffset` and `scrollbarTop` (the same basis the existing
scrollbar-click code uses), then to a row index and whether the arrow cell was
hit.

### Expand fetch (async)

On first expand of a row, `historyView` returns a `tea.Cmd` that calls
`GetConversationTurns(id)` and emits a `historyTurnsLoadedMsg{id, turns}`. The
model stores the turns on the matching row, marks `turnsLoaded`, and re-renders.
Re-expanding a loaded row is instant (cached). The drawer shows the last 3 prose
turns; tool turns are skipped.

## Files

- New: `internal/ui/history_view.go` — mode state, content builder, key/click
  handling, expand fetch.
- New: `internal/ui/history_view_test.go` — pure-helper tests.
- Modify: `internal/ui/model.go` — history-mode branch in `Update` (keys/mouse)
  and the viewport-content path; entry points for `-r` and `/history`.
- Retire: `internal/ui/history_picker.go` (bordered `RowList` panel) and its
  `contentPageHistory` wiring.
- Reuse: the markdown renderer (`# History`, wrapped recap), the chat viewport +
  `renderViewportWithScrollbar`, `agentclient.GetConversationTurns`.

## Testing

Pure, table-driven where possible:

- Content builder: `# History` heading present; one collapsed row → two lines;
  expanded row → drawer lines; long name/recap truncate (no line exceeds width).
- Cursor movement + auto-scroll offset keeps the selected row within the viewport
  window.
- Click hit-test: screen `Y` (given `YOffset`, `scrollbarTop`) → correct row
  index and arrow-hit boolean.
- Tail formatting: last 3 prose turns, tool turns skipped, each clipped.
- Expand fetch: a fake agent returns turns; the loaded-msg path fills the row and
  flips `loading…` → content.

## Acceptance

- `cercano -r` shows `# History` and the list in the main section, no bordered
  block, never past the terminal bottom; the viewport scrollbar scrolls it.
- Each row shows the name and an indented preview on separate lines.
- Clicking a row's arrow (or `→`) expands an inline drawer with the full recap +
  recent turns; `←` / click collapses it.
- `enter` resumes the selected conversation; `esc` returns to chat.
