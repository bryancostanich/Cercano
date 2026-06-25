# Chat View Migration — Step 4 Plan (`/c` adopts `chatView`, retire `chatPane`)

**Branch:** `chat-view` · **Worktree:** `/Users/bryancostanich/git_repos/bryan_costanich/Cercano/.claude/worktrees/chat-view`

All decisions (G1…G7) are DECIDED per `docs/decisions/autonomous_2026-06-24.md` (D4)
and quantified in `docs/features/cli/chat-view/step4-design.md`. This plan bakes
them in — do not re-litigate. Phased into **3 Tasks** (G7=b); each builds green and
`/c` works throughout.

---

## Goal

`/c` (the context manager, `context_view.go`) drives the SAME `chatView`
(`chat_view.go`) the main page now uses. End state: ONE chat component, two
independent instances (main + `/c`), each driven by its own `ChatDriver`. The thin
`chatPane` (`chatpane.go`) + `renderChatEntry` + `chatpane_test.go` are deleted.
The shared event types + `ChatDriver` interface move to a new neutral
`chat_events.go`.

`/c`'s busy "working…" state converges onto the main page's in-transcript
spinner+lime-sweep placeholder (G1=a) — intended UX convergence per the migration
thesis ("`/c` becomes a second agent chat with the same UX as the main page"), NOT
a regression. The old pinned bottom status line disappears from `/c`.

## Architecture

Paths are `source/clients/cli/internal/ui/` unless noted.

- **`chatView`** (`chat_view.go`): the reusable, agent-agnostic transcript
  component — owns `entries`, the transcript state machine (`Apply`), the FIFO
  `queued`, viewport/scroll/scrollbar/selection, and `turnStatus` (the inline
  placeholder telemetry). It does NOT know about `agentclient` or any driver.
- **`ChatDriver`** (interface): `Name()` + `Submit(ctx, input) tea.Cmd`. Two
  implementors: `mainAgentDriver` (streaming) and `contextManagerDriver`
  (single-shot, UNCHANGED).
- **Host (`Model`)**: owns the gate (`pendingConfirm`), the prompt bar, layout,
  and the per-surface routing. For BOTH surfaces the host calls `driver.Submit`
  itself and opens a streaming placeholder; events flow back through `*.Apply`.
- **`contextView`** (`context_view.go`): the `/c` page — stacks a scrollable
  turns-list ABOVE a `chatView` band, each with its own scrollbar; sizes the band
  via `chatView.DesiredHeight()` capped at ½ panel.

### Busy model under G1=a (baked in)

`chatView` has NO `busy bool`. "Busy" ≡ "an open streaming-assistant placeholder
exists" ≡ `cv.chat.streamingTextEntry() != nil` — exactly the predicate the main
host already uses at `model.go:937`. On `/c` submit the host appends
`&Entry{Role: RoleAssistant, Streaming: true}` + sets `turnStatus{activity:"working…"}`;
`chatDoneMsg`/`chatErrorMsg` close the placeholder (the existing `chatView.Apply`
`chatDoneMsg`/`chatErrorMsg` arms flip `Streaming=false`). A small
`contextView.busy()` helper wraps `streamingTextEntry() != nil` for the three host
busy-checks (`model.go:754`, `:943`, `routeChatMsg`).

## Tech Stack

Go, module `cercano/source/clients/cli`. bubbletea v2 (`charm.land/bubbletea/v2`),
`charm.land/bubbles/v2/viewport`, lipgloss v2. Tests are stdlib `testing`. Build/test
run from `source/clients/cli`.

## Global Constraints

- Module path is `cercano/source/clients/cli` — imports use that prefix.
- Commit messages must NOT contain "Claude" anywhere (message, trailers, nowhere).
- **Do NOT push. Do NOT merge to main.** Worktree commits only.
- The step-1 golden (`chat_view_golden_test.go` + `testdata/chatview/`) and
  `scripted_turn.golden` stay **byte-identical** — main chat is untouched by step 4.
  Run them as a gate after every Task.
- `context_manager_driver_test.go` stays **UNCHANGED and green** — the driver
  event contract is the behavior-preservation spine. If `/c`'s event contract is
  preserved, the swap is behavior-preserving by construction.
- Re-pointed `/c` tests keep **observable assertions identical**: entry counts,
  the substrings "removed N turn(s).", "nothing to remove.", the rationale, the
  error line, the queued "⏳" rows, the band heights.
- Each Task ends with `go build ./... && go test ./...` green from
  `source/clients/cli`, plus the parity-gate commands listed in that Task, plus a
  commit.

## Forks for the controller

None. D4 covers G1–G7. The only implementation detail D4 leaves implicit — that
`/c`'s host-side busy checks (`model.go:754`, `:943`, `routeChatMsg`) must read
"open placeholder exists" instead of `pane.Busy()` — is a direct mechanical
consequence of G1=a (host opens the placeholder, `turnStatus` feeds it,
`chatDoneMsg` closes it), not a new architectural decision. Proceeding.

---

## Task 1 — New `chatView` surface + move shared types to `chat_events.go`

**No `/c` change.** `chatPane` is still used by `context_view.go`; the package stays
green. Add the three things `/c` will need (G2 `chatAssistantMsg` arm, G4
`DesiredHeight()`, and confirm the G1 placeholder path already exists in `Apply`)
and relocate the shared event vocabulary to a neutral file (G5).

### 1.1 — Move shared event types + `ChatDriver` to `chat_events.go` (G5=a)

This is a pure relocation (no behavior change); do it first so later edits land in
the right file.

- **Create** `internal/ui/chat_events.go` with `package ui` and these imports:
  ```go
  import (
      "context"
      tea "charm.land/bubbletea/v2"
  )
  ```
- **Cut from `chatpane.go` lines 15–86** (verbatim, comments included) and paste
  into `chat_events.go`:
  - `ChatDriver` interface (`chatpane.go:19-22`)
  - `chatStatusMsg` (`:32-37`), `chatAssistantMsg` (`:38`)
  - `chatAssistantDeltaMsg` (`:45`), `chatProgressMsg` (`:49`)
  - `toolEntryStartMsg`/`toolEntryStopMsg`/`toolEntryExecStartMsg`/`toolEntryExecCompleteMsg` (`:52-60`)
  - `permissionRequiredMsg` (`:64`)
  - `chatDoneMsg` (`:69-75`), `chatErrorMsg` (`:76`), `chatConfirmMsg` (`:82-86`)
  - the doc-comment blocks (`:24-31`, `:40-43`, `:62-63`, `:65-68`, `:78-81`)
- In the moved `ChatDriver` doc-comment, change "plugs an agent into a chatPane"
  → "plugs an agent into a chat surface" and "The pane is agent-agnostic" → "The
  chat surface is agent-agnostic" (the word "pane" is retiring).
- `chatpane.go` now starts its `chatPane` struct at the old line 88; leave its
  `import` block alone for this step (it still compiles — `context`/`time`/`tea`/
  `ansi`/`render`/`theme`/`strings` are all still used by the pane below).

**Test/verify:** no new test. This is mechanical.

```
cd source/clients/cli && go build ./...
```
Expected: clean build (no output). Then:
```
go test ./internal/ui/
```
Expected: `ok  cercano/source/clients/cli/internal/ui`. The move is type-neutral;
all existing references resolve to the same package-level symbols.

### 1.2 — Add the `chatAssistantMsg` whole-append arm to `chatView.Apply` (G2=a)

**Write test first** — append a new file `internal/ui/chat_view_apply_test.go`:

```go
package ui

import (
    "testing"
    "cercano/source/clients/cli/internal/theme"
)

func newTestChatView() chatView {
    return newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), "", "", 78, 20)
}

func TestChatView_Apply_AssistantMsg_WholeAppend(t *testing.T) {
    c := newTestChatView()
    c.Apply(chatAssistantMsg{text: "delete rationale"})
    es := c.Entries()
    if len(es) != 1 {
        t.Fatalf("want 1 entry, got %d", len(es))
    }
    if es[0].Role != RoleAssistant || es[0].Content != "delete rationale" {
        t.Fatalf("want whole-appended assistant entry, got role=%v content=%q", es[0].Role, es[0].Content)
    }
    if es[0].Streaming {
        t.Error("whole-append entry must not be Streaming")
    }
}
```

**Red:**
```
go test ./internal/ui/ -run TestChatView_Apply_AssistantMsg_WholeAppend
```
Expected: FAIL — `chatAssistantMsg` falls through the `Apply` switch (no arm),
0 entries, `want 1 entry, got 0`.

**Implement** — add a case to `chatView.Apply` in `chat_view.go`, immediately
before the `case chatDoneMsg:` arm (`chat_view.go:267`):

```go
	case chatAssistantMsg:
		// Whole-message append (the /c confirm rationale and any non-streaming
		// driver use this instead of delta-extend). A complete assistant entry,
		// not streaming.
		c.AppendEntry(&Entry{Role: RoleAssistant, Content: m.text})
```

**Green:**
```
go test ./internal/ui/ -run TestChatView_Apply_AssistantMsg_WholeAppend
```
Expected: `ok`. Then full package: `go test ./internal/ui/` → `ok`.

### 1.3 — Add `DesiredHeight()` to `chatView` (G4=a)

`/c`'s `regionHeights` needs grow-to-content sizing. Mirror `chatPane.DesiredHeight`
(`chatpane.go:269-279`): content lines + pinned rows (1 if an open placeholder,
plus one per queued item), min 1. With `chatView` the content-line count is the
viewport's total rendered lines; the placeholder row is already inside `entries`
(the streaming placeholder is a real entry), so the "+1 busy" the old pane added
separately is NOT added here — it's already counted by `TotalLineCount()` once the
host appends the placeholder entry. Queued rows ARE separate chrome (rendered above
the input by the host, not in `entries`), so add `len(Queued())`.

**Write test first** — add to `chat_view_apply_test.go`:

```go
func TestChatView_DesiredHeight_GrowsWithContentAndQueue(t *testing.T) {
    c := newTestChatView()
    base := c.DesiredHeight()
    if base < 1 {
        t.Fatalf("DesiredHeight must be >= 1, got %d", base)
    }
    for i := 0; i < 5; i++ {
        c.Apply(chatAssistantMsg{text: "line"})
    }
    c.rebuild() // push entries into the viewport so TotalLineCount reflects them
    grown := c.DesiredHeight()
    if grown <= base {
        t.Errorf("DesiredHeight should grow with content: base=%d grown=%d", base, grown)
    }
    c.Enqueue("queued one")
    if c.DesiredHeight() != grown+1 {
        t.Errorf("each queued message adds one row: got %d want %d", c.DesiredHeight(), grown+1)
    }
}
```

**Red:**
```
go test ./internal/ui/ -run TestChatView_DesiredHeight
```
Expected: FAIL to COMPILE — `c.DesiredHeight undefined`.

**Implement** — add to `chat_view.go` (near `Height()`, `chat_view.go:416`):

```go
// DesiredHeight reports how many rows the chat wants — its rendered content lines
// plus the queued chrome rows the host pins above the prompt. A host (the /c split
// view) uses this to size the chat band so it grows with the transcript instead of
// eating the whole panel. The streaming placeholder, when open, is a real entry and
// is already counted in the content lines.
func (c *chatView) DesiredHeight() int {
	n := c.vp.TotalLineCount()
	n += len(c.queued)
	if n < 1 {
		n = 1
	}
	return n
}
```

**Green:**
```
go test ./internal/ui/ -run TestChatView_DesiredHeight
```
Expected: `ok`.

### 1.4 — Confirm the G1 placeholder/busy path needs no new `chatView` API

The busy placeholder uses only existing surface:
- open: `AppendEntry(&Entry{Role: RoleAssistant, Streaming: true})` +
  `SetTurnStatus(turnStatus{activity: "working…", start: time.Now()})` (both exist).
- the inline spinner+sweep renders from `chat_view.go:555-562` (existing).
- close: `Apply(chatDoneMsg{...})` / `Apply(chatErrorMsg{...})` flip
  `Streaming=false` (existing arms `chat_view.go:267`, `:288`).
- busy predicate: `streamingTextEntry()` (existing, `chat_view.go:163`).

No new `chatView` method needed for G1. (No code change in 1.4; this is the
verification that Task 2's host wiring has everything it needs.)

### 1.5 — Task 1 gate + commit

```
cd source/clients/cli
go build ./...
go test ./...
```
Expected: all `ok`. Specifically confirm the parity goldens are untouched:
```
go test ./internal/ui/ -run 'Golden|ScriptedTurn'
```
Expected: `ok` (byte-identical — main chat unchanged).

**Commit:**
```
git add source/clients/cli/internal/ui/chat_events.go \
        source/clients/cli/internal/ui/chatpane.go \
        source/clients/cli/internal/ui/chat_view.go \
        source/clients/cli/internal/ui/chat_view_apply_test.go
git commit -m "chatView: add chatAssistantMsg arm + DesiredHeight; move shared events to chat_events.go"
```

---

## Task 2 — Swap `contextView` onto `chatView` + rewire host; re-point `/c` tests

Replace `contextView.pane *chatPane` with `chat chatView` and rewire the three host
touch-points (`handleContextViewKey` enter/up/esc, `routeChatMsg`, `regionHeights`/
`View`) plus the two busy-tick checks. Re-point the `/c` tests keeping observable
assertions identical. `chatPane` still EXISTS after this Task (deleted in Task 3),
but `contextView` no longer references it.

### 2.1 — Re-point the `/c` tests onto the `chatView` surface

Do the tests first so they define the target surface, then make them pass by
implementing 2.2. (They will fail to compile until 2.2 lands — that is the red.)

Edit the following test files. Keep every OBSERVABLE assertion identical; only the
accessor changes (`cv.pane.*` → the `chatView` surface). Map:

| old (`chatPane`) | new (`chatView` via `contextView`) |
|---|---|
| `cv.pane = newChatPane(d, s, p, w, h)` | `cv.chat = newChatView(s, p, "", "", w-2, h)` |
| `cv.pane.entries` | `cv.chat.Entries()` |
| `cv.pane.queued` | `cv.chat.Queued()` |
| `cv.pane.Busy()` | `cv.busy()` (new `contextView` helper) |
| `cv.pane.Submit("x")` | `m.submitContextEdit(cv, "x")` (new host helper, see 2.2) |
| `cv.pane.appendAssistant("r")` | `cv.chat.Apply(chatAssistantMsg{text: "r"})` |
| `cv.pane.Apply(chatAssistantMsg{...})` | `cv.chat.Apply(chatAssistantMsg{...})` |

Concretely:

- **`context_view_route_test.go`**
  - `modelWithContextView` (line 40): `cv.chat = newChatView(s, p, "", "", w-2, h)`.
  - `TestContextView_PaneSubmitFromPromptBar`: `!cv.pane.Busy()` → `!cv.busy()`.
  - `TestContextView_ChatConfirmRaisesGate`: loop `for _, e := range cv.chat.Entries()`.
  - `TestContextView_ChatDoneClears`: `cv.pane.Submit("hi")` →
    `m, _ = m.submitContextEdit(cv, "hi")`; `cv.pane.Busy()` → `cv.busy()`.
    (Note: `submitContextEdit` returns `(Model, tea.Cmd)`; reassign `m`.)
  - `TestContextView_UpUnstagesLastQueued`: replace the two `cv.pane.Submit(...)`
    with `m, _ = m.submitContextEdit(cv, "first")` then directly
    `cv.chat.Enqueue("second")` (the second submit was enqueued-while-busy; with
    the placeholder model, simulate the enqueue explicitly so the test stays a unit
    test of the unstage path). Assertion `next.input.Value() == "second"` unchanged.
  - `TestContextView_EscClosesPageAndClearsQueue`: `m, _ = m.submitContextEdit(cv, "first")`,
    then `cv.chat.Enqueue("queued")`; `len(cv.pane.queued)` → `len(cv.chat.Queued())`.
  - `TestContextViewRoute_ProposalRaisesConfirm`: `cv.pane.appendAssistant("delete rationale")`
    → `cv.chat.Apply(chatAssistantMsg{text: "delete rationale"})`.
  - `TestContextViewRoute_DeleteErrorSurfacesScrollback`: `cv.pane.entries` →
    `cv.chat.Entries()` (3 occurrences); assertion substring "rpc: unavailable"
    unchanged.
  - `TestContextViewRoute_EmptyProposalNoConfirm`: `cv.pane.entries` →
    `cv.chat.Entries()` (3×); substring "nothing to remove" unchanged.

- **`context_view_edit_test.go`** (lines 25, 37, 41):
  - `cv.pane = newChatPane(d, s, p, 80, 24)` → `cv.chat = newChatView(s, p, "", "", 78, 24)`.
  - `cv.pane.appendAssistant("removed the tangent")` →
    `cv.chat.Apply(chatAssistantMsg{text: "removed the tangent"})`.
  - `for _, e := range cv.pane.entries` → `for _, e := range cv.chat.Entries()`.

- **`context_view_layout_test.go`** (lines 27, 52, 54):
  - both `cv.pane = newChatPane(&contextManagerDriver{}, cv.styles, cv.palette, cv.width, cv.height)`
    → `cv.chat = newChatView(cv.styles, cv.palette, "", "", cv.width-2, cv.height)`.
  - `cv.pane.Apply(chatAssistantMsg{...})` → `cv.chat.Apply(chatAssistantMsg{...})`,
    followed by `cv.chat.rebuild()` once after the loop so `DesiredHeight` sees the
    lines (the old pane recomputed `contentLines` on demand; `chatView` reads the
    viewport, which `rebuild` populates). Assertions (`len(lines) == totalH`,
    "msg-49" visible, "ctx" present, "turn-" count ≥ totalH/2) unchanged.

Leave `chatpane_test.go` and `context_manager_driver_test.go` UNCHANGED in this Task.

**Red:**
```
cd source/clients/cli && go test ./internal/ui/ -run 'ContextView' 2>&1 | head
```
Expected: compile failure — `cv.chat undefined`, `cv.busy undefined`,
`m.submitContextEdit undefined`. That is the red for 2.2.

### 2.2 — Swap the field + add host helpers + rewire

**`context_view.go`:**

- Struct (`:37-40`): replace
  ```go
  	md     *render.Markdown
  	driver *contextManagerDriver
  	pane   *chatPane
  ```
  with
  ```go
  	md     *render.Markdown
  	driver *contextManagerDriver
  	chat   chatView
  ```
- `newContextView` (`:84`): replace `cv.pane = newChatPane(cv.driver, s, p, w, h)`
  with `cv.chat = newChatView(s, p, "", "", w-2, h)`. (The two reserved columns:
  the band width passed to the chat is `w-2` so the host's scrollbar gutter math
  matches the main page; `regionHeights`/`View` re-set size per frame anyway.)
- `SetSize` (`:90-97`): drop the nil-guard; replace `c.pane.SetSize(w, h)` with
  `c.chat.SetSize(w-2, h)`.
- **Add a busy helper** (anywhere in `context_view.go`):
  ```go
  // busy reports whether a context-edit turn is in flight — i.e. the chat holds
  // an open streaming placeholder (mirrors the main page's notion of busy).
  func (c *contextView) busy() bool { return c.chat.streamingTextEntry() != nil }
  ```
- `View` (`:163-172`): replace the pane block:
  ```go
  	turnsH, paneH := c.regionHeights()
  	turnsBlock := c.renderScrollableContent(strings.Join(c.turnsLinesOnly(), "\n"), turnsH)
  	c.chat.SetSize(c.width-2, paneH)
  	paneBlock := padLines(c.chat.View(), paneH)
  	return turnsBlock + "\n" + paneBlock
  ```
  (No nil-guard: `chat` is a value field, always constructed — same invariant the
  main page's `chat chatView` holds.)
- `regionHeights` (`:176-191`): replace the pane branch:
  ```go
  	totalH := dashboardContentHeight(c.height)
  	if totalH < 2 {
  		totalH = 2
  	}
  	c.chat.SetSize(c.width-2, totalH) // set width so DesiredHeight wraps correctly
  	paneH := clampInt(c.chat.DesiredHeight(), 1, maxInt(1, totalH/2))
  	turnsH := totalH - paneH
  	if turnsH < 1 {
  		turnsH = 1
  	}
  	return turnsH, paneH
  ```

**`model.go`:**

- **Add the submit helper** (near `routeChatMsg`, ~`model.go:1766`). This is the
  symmetric-with-main path (G6=a host calls `driver.Submit`; G1=a host opens the
  placeholder):
  ```go
  // submitContextEdit submits a /c edit instruction: enqueue while busy, else
  // append the user entry + an open streaming placeholder, set the working status,
  // and fire the driver. Mirrors the main page's submit path (sendChatMessage).
  func (m Model) submitContextEdit(cv *contextView, input string) (Model, tea.Cmd) {
  	if cv.busy() {
  		cv.chat.Enqueue(input)
  		return m, nil
  	}
  	cv.chat.AppendEntry(&Entry{Role: RoleUser, Content: input})
  	cv.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
  	cv.chat.SetTurnStatus(turnStatus{activity: "working…", start: time.Now()})
  	cv.chat.rebuild()
  	return m, tea.Batch(cv.driver.Submit(context.Background(), input), progressAnimTick())
  }
  ```
- `handleContextViewKey` esc-arm (`:1707-1709`): replace
  ```go
  		if cv.pane != nil {
  			cv.pane.clearQueue()
  		}
  ```
  with `cv.chat.ClearQueue()`.
- enter-arm (`:1722`): replace `return m, cv.pane.Submit(text)` with
  `return m.submitContextEdit(cv, text)`.
- up-arm (`:1732-1737`): replace
  ```go
  		if m.input.Value() == "" && cv.pane != nil {
  			if msg, ok := cv.pane.unstageLastQueued(); ok {
  ```
  with
  ```go
  		if m.input.Value() == "" {
  			if msg, ok := cv.chat.UnstageLast(); ok {
  ```
- `routeChatMsg` (`:1770-1789`): rewrite onto `chatView`. The confirm arm appends
  the rationale via `Apply(chatAssistantMsg{...})` (G2=a), and on `onNo` it must
  close the open placeholder (the old `clearBusy()` flipped `busy=false`; the new
  equivalent is closing the streaming entry — emit a `chatDoneMsg`-style close).
  Closing on `onNo` is done by dropping/closing the placeholder; simplest is to
  route a `chatDoneMsg{}` through `Apply`, which flips `Streaming=false` on the
  open entry and leaves no extra text (its `text` is empty):
  ```go
  func (m Model) routeChatMsg(msg tea.Msg) (Model, tea.Cmd) {
  	cv, ok := m.content.(*contextView)
  	if !ok {
  		return m, nil
  	}
  	if cm, isConfirm := msg.(chatConfirmMsg); isConfirm {
  		cv.chat.Apply(chatAssistantMsg{text: cm.assistant})
  		cv.chat.rebuild()
  		onYes, onNo := cm.onYes, cm.onNo
  		m.pendingConfirm = &confirmRequest{
  			onYes: func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, onYes },
  			onNo: func(m Model) (Model, tea.Cmd) {
  				m.pendingConfirm = nil
  				cv.chat.Apply(chatDoneMsg{}) // close the open placeholder
  				cv.chat.rebuild()
  				return m, onNo
  			},
  		}
  		return m, progressAnimTick()
  	}
  	cv.chat.Apply(msg)
  	cv.chat.rebuild()
  	if cv.busy() {
  		return m, progressAnimTick()
  	}
  	return m, nil
  }
  ```
  Note: queue auto-drain. The old `chatPane.Apply` returned `drainNext()` on
  done/error. Preserve that: after the `Apply`+`rebuild`, if NOT busy and the queue
  is non-empty, drain the front and re-submit:
  ```go
  	cv.chat.Apply(msg)
  	cv.chat.rebuild()
  	if !cv.busy() {
  		if next, ok := cv.chat.DrainNext(); ok {
  			return m.submitContextEdit(cv, next)
  		}
  		return m, nil
  	}
  	return m, progressAnimTick()
  ```
  (Replace the trailing `if cv.busy()` block above with this drain-aware version.)
- busy-tick checks:
  - `:754`: `if cv.showingProposal || (cv.pane != nil && cv.pane.Busy()) {`
    → `if cv.showingProposal || cv.busy() {`.
  - `:943`: `if cv, ok := m.content.(*contextView); ok && cv.pane != nil && cv.pane.Busy() {`
    → `if cv, ok := m.content.(*contextView); ok && cv.busy() {`.

Confirm `model.go` already imports `context` and `time` (it does — used by
`sendChatMessage`). `tea` is imported.

**Green:**
```
cd source/clients/cli && go build ./... && go test ./internal/ui/ -run 'ContextView'
```
Expected: `ok`. The re-pointed observable assertions (entry counts, "removed N
turn(s)."/"nothing to remove."/rationale/error/queued rows, band heights `len(lines)
== totalH`) all pass against the `chatView` surface.

### 2.3 — Parity gate + commit

```
cd source/clients/cli
go build ./...
go test ./...
```
Expected: all `ok`. Confirm the spine + goldens specifically:
```
go test ./internal/ui/ -run 'ContextManagerDriver|Golden|ScriptedTurn'
```
Expected: `ok` — `context_manager_driver_test.go` UNCHANGED and green; step-1
golden + `scripted_turn.golden` byte-identical.

**Manual `/c` smoke** (build the CLI, drive `/c`):
```
cd source/clients/cli && go build -o /tmp/cercano-cli ./cmd/...   # adjust to real main pkg
```
Then in a live session: open `/c`, type an edit that removes turns → confirm (y) →
expect "removed N turn(s)." in the in-transcript log + the turns list reloads; type
an edit that matches nothing → expect "nothing to remove." Verify the busy state
shows the in-transcript spinner+lime-sweep placeholder (the converged UX), NOT a
pinned bottom status line.

**Commit:**
```
git add source/clients/cli/internal/ui/context_view.go \
        source/clients/cli/internal/ui/model.go \
        source/clients/cli/internal/ui/context_view_route_test.go \
        source/clients/cli/internal/ui/context_view_edit_test.go \
        source/clients/cli/internal/ui/context_view_layout_test.go
git commit -m "context view: drive /c through chatView + contextManagerDriver, retire pane references"
```

---

## Task 3 — Delete `chatPane`, `renderChatEntry`, `chatpane_test.go`

`contextView` no longer references `chatPane` (Task 2). Nothing else in the package
uses it (the shared types moved to `chat_events.go` in Task 1). Delete it.

### 3.1 — Delete the pane code

- After Task 1's move, `chatpane.go` contains ONLY: the `chatPane` struct
  (`:88-103`), `newChatPane` (`:105`), all `chatPane` methods
  (`maxScroll`/`Busy`/`SetSize`/`Submit`/`Apply`/`drainNext`/`unstageLastQueued`/
  `clearQueue`/`appendAssistant`/`clearBusy`/`ScrollBy`/`ScrollTo`/`ScrollState`/
  `clampScroll`/`scrollToBottom`/`contentHeight`/`DesiredHeight`/`contentLines`/
  `View`), and the free function `renderChatEntry` (`:205-214`). All of these are
  now unreferenced → **delete the whole file**:
  ```
  git rm source/clients/cli/internal/ui/chatpane.go
  ```
- **Delete the retiring test** (it exercised the pane's `fakeDriver` + Apply/queue/
  View directly):
  ```
  git rm source/clients/cli/internal/ui/chatpane_test.go
  ```

### 3.2 — Grep gate (no dangling references)

```
cd source/clients/cli
grep -rn 'chatPane\|newChatPane\|cv\.pane\|renderChatEntry' internal/ui/
```
Expected: NO matches. (If `renderChatEntry` is referenced anywhere besides the
deleted pane, stop — that's a missed consumer; but per the inventory it was used
only by `chatPane.contentLines`.)

### 3.3 — Full gate + commit

```
cd source/clients/cli
go build ./...
go test ./...
```
Expected: all `ok`. Confirm the spine + goldens one final time:
```
go test ./internal/ui/ -run 'ContextManagerDriver|Golden|ScriptedTurn|ContextView'
```
Expected: `ok` — driver test unchanged + green; goldens byte-identical; `/c` tests
green on the `chatView` surface.

**Commit:**
```
git add -A
git commit -m "remove chatPane + renderChatEntry + chatpane_test: one chat component"
```

---

## Done criteria

- 3 commits on `chat-view`, each builds + tests green.
- `chatPane`, `newChatPane`, `cv.pane`, `renderChatEntry` fully gone (grep clean).
- `chat_events.go` holds the shared event types + `ChatDriver`; `chat_view.go`
  holds `chatAssistantMsg` arm + `DesiredHeight()`; `/c` drives `chatView` via
  `contextManagerDriver.Submit` + an in-transcript streaming placeholder.
- `context_manager_driver_test.go` UNCHANGED + green. Step-1 golden +
  `scripted_turn.golden` byte-identical. Re-pointed `/c` tests green with identical
  observable assertions. Manual `/c` smoke passes.
- NOT pushed, NOT merged to main.
