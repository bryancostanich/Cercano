# Chat View Migration — Design

**Status:** Design approved 2026-06-24 (roadmap + step 1). Steps 2–4 get their own specs.

The `/c` context manager runs on a reusable chat control (`chatPane` + a pluggable
`ChatDriver`) that was always meant to become the one chat surface for every
interface, including the main page. That migration was the original plan's
"Phase 2," gated on approval and never started — and the Phase-1 pane diverged
into a *minimal reimplementation* (`renderChatEntry`: flat markdown, no tool
entries, no streaming deltas, no selection) rather than the main page's real
rendering. The result is two chat renderers.

This effort **extracts the main page's real transcript code into a reusable
`chatView` component and generalizes it**, so there is one implementation that
both the main chat and `/c` drive. It is the "extract and generalize" path: take
the architecture that landed in `/c` and apply it to the main renderer.

## End state

One reusable `chatView` component owns the transcript:

- entry storage + `renderEntry`-grade rendering (rich markdown with tables, code
  rules, and a live streaming tail; tool-call entries; the navy user-prompt fill;
  streaming token append; the animated busy/status line)
- the viewport + grabbable scrollbar + wheel + drag-scroll
- mouse text-selection + clipboard copy
- driven by the `ChatDriver` event model

Two independent `chatView` instances: the **main chat** and **`/c`**. The root
`Model` drops to a thin **host** that keeps the chrome — the shared multi-line
input bar, header / bottom status bar / context meter / living-recap / permission
chip, slash-command dispatch, the shared `confirmRequest` gate, `relayout`,
splash, and history mode. A `mainAgentDriver` forwards `StreamChat` `StreamMsg`s
as events. The thin `renderChatEntry` path is deleted; both consumers run
`chatView`.

### Boundary

| In `chatView` | Stays host chrome |
|---|---|
| transcript + entry rendering | input bar |
| viewport + scrollbar | header, bottom status bar |
| scroll / wheel / drag-scroll | context meter, recap line |
| mouse selection + copy | permission-mode chip |
| busy/status line + queued display | slash dispatch, confirm gate |
| (later) entries + event model | `relayout`, splash, history mode |

## Roadmap (each step is its own spec → plan → ship)

Because we are extracting the main page's *own* code, the **main page is the
first consumer** (parity proven on the real surface); `/c` migrates last.

1. **Extract the transcript view** *(this spec)* — lift the entry rendering +
   viewport + scrollbar into `chatView`; the main page delegates to it. Pure
   render+viewport extraction, byte-identical output, `/c` untouched.
2. **Move scroll + selection in** — relocate scrollbar drag, wheel, drag-scroll,
   and mouse selection + copy from `Model` into `chatView` behind a local-
   coordinate boundary.
3. **Event-drive it** — `chatView` takes ownership of `entries`; extend
   `ChatDriver` / `chatPaneMsg` for streaming deltas + tool entries + rich
   status + confirm; write `mainAgentDriver`; the streaming state machine moves
   behind the driver and `Model` becomes the thin host.
4. **Adopt in `/c`, delete the duplicate** — point `/c` at `chatView`; retire the
   thin `chatPane`/`renderChatEntry`.

---

# Step 1 — Extract the transcript view

**Goal:** Move the main page's transcript rendering and viewport into a new
`chatView` component that the main page delegates to, with **zero behavior
change** (output byte-identical, proven by golden tests). Entry storage, the
streaming state machine, mouse selection, and scrollbar *drag* stay in `Model`
for now — they move in later steps.

## What moves into `chatView`

From `internal/ui/model.go` (and `scrollback_tool.go` / `scrollbar.go`):

- `renderEntry` (`model.go:1631`) + the role/tool dispatch + `entryIndent`
- `renderAssistantMarkdown` (`model.go:1695`), `renderMdBlock` (`model.go:1718`)
  and their helpers (`trimBlankEdgeLines`, `codeRule`, `closeOpenFence`,
  `isHeadingBlock`)
- the per-width `render.Markdown` engine + the block render cache (`model.go:88`)
- the viewport-content build loop currently in `refreshViewport` (`model.go:1592`):
  inter-entry spacing, `viewportPlainLines` mirror, auto-scroll-if-at-bottom
- the base paint in `renderViewportWithScrollbar` (`model.go:2490`): the windowed
  text column + the scrollbar glyph column (`scrollbarColumn`)
- tool-entry rendering is invoked via `renderToolEntry` (`scrollback_tool.go`),
  which stays where it is and is called from `chatView`

`scrollbar.go` (pure geometry) stays a shared package-level helper; `chatView`
calls it.

## What stays in `Model` (step 1)

- the `[]*Entry` slice and all entry mutation (append, streaming token append)
- the streaming state machine (`applyStreamMsg`) and turn telemetry fields
- mouse selection state + the selection overlay application (see seam below)
- scrollbar *drag* / wheel / drag-scroll mouse handling (move in step 2)
- `relayout` (it sizes `chatView` and owns `scrollbarTop`), and all chrome

## `chatView` interface (step 1)

```go
type chatView struct {
    width, height   int
    styles          theme.Styles
    palette         theme.Palette
    viewport        viewport.Model
    md              *render.Markdown
    blockCache      map[string]string // per (block,width) committed-block cache
    plainLines      []string          // mirror for the host's selection hit-testing
    focusedToolIdx  int               // host-set; -1 when no tool entry is focused
    turn            turnStatus        // host-set; drives the pre-token placeholder
}

func newChatView(s theme.Styles, p theme.Palette, w, h int) *chatView

func (c *chatView) SetSize(w, h int)
func (c *chatView) SetEntries(entries []*Entry)   // rebuilds content; preserves at-bottom follow
func (c *chatView) SetFocusedTool(idx int)         // tool-nav highlight (host owns nav in step 1)
func (c *chatView) SetTurnStatus(t turnStatus)     // activity/elapsed/tokens/engine for the busy placeholder
func (c *chatView) View() string                   // windowed transcript + scrollbar column (no selection overlay)

// scroll surface the host's mouse/keys still drive in step 1
func (c *chatView) ScrollBy(delta int)
func (c *chatView) SetYOffset(off int)
func (c *chatView) YOffset() int
func (c *chatView) TotalLineCount() int
func (c *chatView) Height() int
func (c *chatView) AtBottom() bool
func (c *chatView) GotoBottom()
func (c *chatView) PlainLines() []string            // == plainLines, for selection
```

`turnStatus` is a small value the host fills from its turn telemetry:

```go
type turnStatus struct {
    activity string
    start    time.Time
    tokOut   int
    model    string
    cloud    bool
}
```

This is the one real coupling the lift must break cleanly: the pre-token animated
placeholder (`model.go:1668-1676`) reads ~5 `Model` turn fields today. `chatView`
renders it from the injected `turnStatus` instead of reaching into `Model`.

## Host wiring after the lift

- `Model` gains `chat *chatView`, built in `New`, resized in `relayout`
  (`m.chat.SetSize(contentW-2, bodyH)` replacing the direct `m.viewport` sizing).
- `Model.refreshViewport()` becomes: set focus + turn status, then
  `m.chat.SetEntries(m.entries)`. The old content-build code is deleted.
- `Model.renderViewportWithScrollbar()` becomes: `base := m.chat.View()`, then the
  **host applies the selection overlay** on top of `base` (the only part not
  moved in step 1 — see seam).
- Every current reader of `m.viewport.*` (wheel scroll, scrollbar drag hit-test,
  page-nav keys, `AtBottom`/`GotoBottom`, `TotalLineCount`, `YOffset`) routes
  through the `chatView` scroll methods above. `m.viewport` itself moves inside
  `chatView`; `Model` no longer holds it.
- `m.viewportPlainLines` reads become `m.chat.PlainLines()`.

## The selection seam (explicit, step-1 only)

Today the selection highlight is applied per-line inside
`renderViewportWithScrollbar` via `highlightRange` (`selection.go`). In step 1,
`chatView.View()` returns the **base** rendered lines (text column + scrollbar),
and the host overlays selection on top of that string before final composition.
Selection state and the overlay move fully into `chatView` in step 2; step 1
keeps the overlay host-side so the lift stays mechanical. The boundary: `chatView`
exposes the same `PlainLines()` the host already used, so selection hit-testing is
unchanged.

## Regression gate — golden tests

`internal/ui/chat_view_test.go`: a fixture matrix of transcripts, each rendered
both by the **pre-extraction** path (captured as a golden string) and by
`chatView.View()`, asserted byte-identical. Matrix:

- a user entry (navy fill), an assistant prose entry, a system entry
- a tool-call entry (folded) + a focused tool entry
- a mid-stream assistant entry (`Streaming`, empty `Content`) with a `turnStatus`
  → the animated placeholder line (freeze the spinner/sweep by injecting a fixed
  `turnStatus.start` and asserting structure, since the animation is time-based —
  test the non-animated assembly, or compare against the same frozen inputs run
  through both paths)
- markdown with a table, a fenced code block, and an open (live-tail) fence
- narrow (40) and wide (120) widths; scrolled and at-bottom offsets

The golden source is the current `Model` render for identical inputs, so the test
proves the extraction changed nothing. Animation-dependent lines are compared
path-vs-path under identical frozen inputs rather than against a hand-written
golden.

## Files

- Create: `internal/ui/chat_view.go` — the `chatView` type + the moved rendering.
- Create: `internal/ui/chat_view_test.go` — golden/characterization tests.
- Modify: `internal/ui/model.go` — delete the moved functions; add `chat *chatView`;
  rewire `refreshViewport`, `renderViewportWithScrollbar`, `relayout`, and every
  `m.viewport.*` reader to the `chatView` surface.
- Unchanged: `internal/ui/scrollback_tool.go` (`renderToolEntry`), `scrollbar.go`,
  `selection.go`, `internal/ui/chatpane.go` (the `/c` pane — untouched until step 4).

## Out of scope (step 1)

Entry ownership, the `ChatDriver` event model, streaming behind a driver, moving
selection/scrollbar-drag into the component, and any `/c` change. Those are
steps 2–4.

## Testing summary

- Golden parity (above) is the primary gate.
- A focused unit test per scroll method (`ScrollBy`/`SetYOffset`/`AtBottom`/
  `GotoBottom`/`TotalLineCount`) confirming the `chatView` surface matches the
  `viewport.Model` behavior the host relied on.
- Full `go test ./...` green and `go build ./...` clean; manual smoke: `cercano`
  renders chat, scrolls, selects/copies, streams a turn, shows tools — all
  visually identical to before.
