# `/c` Turn Previews + Expand — Design

**Status:** Design approved. Implementation not started.

Enhances the `/c` context viewer's turn list: assistant turns show a few lines
(not a single 120-char preview), any turn with more content than its collapsed
view gets a clickable expand arrow, and clicking (or a keyboard toggle) expands it
to the full content.

## Why

`ContextTurn` currently carries only a single-line, flattened, ~120-char
`preview` (`contextTurnView`), so the CLI can't show multiple lines or expand. The
turn list reads as a flat row-per-turn list; you can't actually see what an
assistant turn said. This adds richer content + expand/collapse.

## Decisions (from brainstorming)

- Assistant turns: **3 lines** collapsed. All other turns: **1 line**.
- `body` is capped server-side at **4 KB** (upfront, no lazy fetch RPC).
- Expand via **click on the arrow AND a keyboard toggle**.

## 1. Data — richer `ContextTurn`

`source/proto/agent.proto` `ContextTurn` gains (regenerate stubs):

```proto
  string body      = 6; // un-flattened content (newlines preserved), capped at 4 KB
  bool   truncated = 7; // true if body was capped
```

`contextTurnView` (`internal/server/context_turns.go`):
- Build `body` from the turn's content **without flattening** (keep newlines): for
  text turns, `t.Content`; for tool turns, a readable form (tool name + pretty args
  for `tool_use`; the result content for `tool_result`). Cap at 4096 bytes (on a
  rune boundary, like `ctTruncate`), set `truncated` when cut.
- Keep `preview` (flattened one-liner) as-is for the collapsed single-line rows.

`agentclient.ContextTurn` gains `Body string` and `Truncated bool`; the wrapper
maps `t.GetBody()`/`t.GetTruncated()`.

## 2. Rendering — `contextView.renderTurn`

The turns region renders each turn as one or more lines. For a turn:
- **Collapsed lines:** wrap `body` to the panel width; take the first **3** lines
  for assistant turns, **1** for others. (Fallback to `preview` if `body` empty.)
- **Overflow → arrow:** a turn "overflows" if its wrapped `body` has more lines
  than the collapsed count, or `truncated`. Overflowing turns render a leading
  marker: **▸** (collapsed) / **▾** (expanded); non-overflowing turns render a
  two-space gutter (so columns align).
- **Header line:** `<marker> [role|kind] ≈tokens` then the first content line; the
  remaining collapsed/expanded content lines hang-indent beneath.
- **Expanded:** show all wrapped `body` lines; if `truncated`, append a dim
  `…(truncated)` line.
- Marked-for-delete styling (the `✗` dim treatment) still applies, layered on top.

The renderer also produces, alongside the lines, a **row→turn map** (which screen
rows belong to which turn id, and which row carries the arrow) for click
hit-testing (§4).

## 3. Expand state

`contextView` gains `expanded map[string]bool` (turn id → expanded) and a
`focusedTurn int` (index into `snapshot.Turns`, `-1` = none) for keyboard nav.
`toggleExpand(id)` flips the map entry. Auto-refresh (the 1.5s snapshot reload)
preserves `expanded` (keyed by id, survives a turns reload).

## 4. Interaction

**Mouse (click the arrow):**
- Content pages currently receive only scrollbar-drag clicks (`contentScrollbarAt`).
  Add: when `/c` is active and a `MouseClickMsg` is NOT on the scrollbar, route it
  to `contextView.handleClick(x, y)`.
- `handleClick` maps the screen `y` (minus the page's top screen row, plus the
  turns `scrollOffset`) to a content line, looks it up in the row→turn map, and —
  if the click is on that turn's arrow cell (the leading marker column) — calls
  `toggleExpand(id)`. Clicks elsewhere are ignored (v1).

**Keyboard:**
- `tab` / `shift+tab` move `focusedTurn` to the next/previous **expandable** turn
  (skipping non-overflowing ones); the focused turn renders with a subtle
  highlight. `enter` or `space` toggles the focused turn. (`tab` is intercepted in
  `handleContextViewKey` before the prompt bar, so it doesn't conflict with typing
  an instruction.)
- Existing `/c` keys (typing → prompt bar, `pgup`/`pgdn` scroll, `esc` close,
  `ctrl+r` refresh) are unchanged.

## 5. Error / edge

| Case | Behavior |
|---|---|
| `body` empty | fall back to `preview`; no arrow |
| Turn fits in its collapsed count and not truncated | no arrow, no expand |
| Click not on an arrow | ignored |
| Expanded turn taller than the region | scrolls within the turns region (existing scroll) |
| Turns reload (auto-refresh) | `expanded`/focus preserved by id; ids gone → dropped |

## 6. Testing

- **Server:** `contextTurnView` builds a multi-line `body` (newlines preserved) for
  a multi-line text turn; caps at 4 KB on a rune boundary and sets `truncated`;
  `preview` stays single-line. `GetConversationTurns` returns `body`/`truncated`.
- **CLI render:** an assistant turn with 6 body lines renders 3 + a `▸`; toggling
  `expanded` renders all 6 + `▾`; a 1-line turn renders no arrow; `truncated`
  shows the `…(truncated)` line when expanded.
- **CLI interaction:** `handleClick` on an arrow cell toggles that turn (row→turn
  map resolves the right id); a click off the arrow is a no-op. `tab` advances
  focus to the next expandable turn; `enter` toggles the focused turn.

## Out of scope

Lazy full-content fetch (we cap at 4 KB); syntax-highlighting expanded tool JSON;
per-turn copy. Click targets other than the arrow.

## Key file references

| Concern | Location |
|---|---|
| ContextTurn proto + handler | `source/proto/agent.proto`; `internal/server/context_turns.go` (`contextTurnView`) |
| Turn rendering | `internal/ui/context_view.go` (`renderTurn`, `turnsLines`, `renderScrollableContent`) |
| Mouse routing / scrollbar hit-test | `internal/ui/model.go` (`MouseClickMsg`, `contentScrollbarAt`) |
| /c key routing | `internal/ui/model.go` (`handleContextViewKey`) |
| agentclient ContextTurn | `source/server/pkg/agentclient/client.go` |
