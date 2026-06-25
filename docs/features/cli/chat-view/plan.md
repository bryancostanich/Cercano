# Chat View — Step 1 (Extract Transcript View) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the main page's transcript rendering and viewport out of the root `Model` into a new `chatView` component that `Model` delegates to, with **zero behavior change** proven by byte-identical golden tests.

**Architecture:** A new `chatView` type owns the `viewport.Model`, the markdown engine, and the entry-rendering methods (`renderEntry`/`renderAssistantMarkdown`/`renderMdBlock`). `Model` keeps the `[]*Entry` slice, the streaming state machine, and mouse selection; it hands entries to `chatView` via `SetEntries`, injects the animated-placeholder inputs via a `turnStatus` value, and passes its selection overlay into `chatView.View(...)` as a callback. `refreshViewport` and `renderViewportWithScrollbar` stay as thin `Model` methods that delegate.

**Tech Stack:** Go 1.21+, `charm.land/bubbletea/v2` + `charm.land/bubbles/v2/viewport`, `charm.land/lipgloss/v2`, `github.com/charmbracelet/x/ansi`, the in-repo `render` (markdown/SplitBlocks) + `theme` packages.

## Global Constraints

- Module: `cercano/source/clients/cli`. Build: `go build ./...`. Test: `go test ./... -count=1`. Run from `source/clients/cli/`.
- Commit messages must NOT contain the word "Claude"; no Co-Authored-By trailers naming Claude.
- **Zero behavior change.** The main page's rendered output must be byte-identical before and after. The golden test from Task 1 is the gate; it must stay green through Task 2 without regeneration.
- Do NOT touch `internal/ui/chatpane.go` (the `/c` pane), `scrollbar.go`, `scrollback_tool.go`, or `selection.go` logic — they stay shared/unchanged. `renderToolEntry`, `scrollbarColumn`, `plainLines`, `renderSelectionOnLine` remain where they are and are called from `chatView` or passed in.
- Out of scope: entry ownership, the `ChatDriver` event model, moving selection/scrollbar-drag into the component, any `/c` change. Those are steps 2–4.

### Reference: current symbols (do not redefine; these get moved or called)

```go
// internal/ui/model.go — Model fields involved
md         *render.Markdown   // model.go:88   (MOVES to chatView)
viewport   viewport.Model     // model.go:98   (MOVES to chatView, renamed field `vp`)
viewportPlainLines []string   // model.go:102  (MOVES to chatView, renamed `plainLines`)
turnStart time.Time; turnActivity string; turnTokOut int; turnModel string; turnCloud bool // :134-138 (STAY in Model; fed to chatView via turnStatus)
focusedToolIdx int            // model.go:188  (STAYS in Model; pushed to chatView via SetFocusedTool)

// Methods that MOVE to chatView (receiver *Model → *chatView; m.→c.):
func (m *Model) renderEntry(e *Entry, idx int) string             // model.go:1631
func (m *Model) renderAssistantMarkdown(e *Entry, textW int) string // model.go:1695
func (m *Model) renderMdBlock(b render.MdBlock, textW int) string // model.go:1718

// Package-level helpers that STAY (called by chatView, no move):
func isHeadingBlock(b render.MdBlock) bool        // model.go:1714
func trimBlankEdgeLines(s string) string          // model.go:1737
func codeRule(lang string, width int, styles theme.Styles) string // model.go:1752
func closeOpenFence(s string) string              // model.go:1771
func animateSpinnerGlyph() string                 // model.go (free fn)
func animateLimeSweep(s string) string            // model.go (free fn)
func indentBlock(pad, s string) string            // free fn
func plainLines(content string) []string          // selection.go:25
func scrollbarColumn(total, height, yOffset int) []rune // scrollbar.go
func renderToolEntry(...) string                  // scrollback_tool.go
func turnStatusLine(activity string, elapsed time.Duration, tokOut int, model string, isCloud bool) string // turnstatus.go:22

// Methods that STAY on Model and become thin delegators:
func (m *Model) refreshViewport()                 // model.go:1592 (25 callers — keep)
func (m Model) renderViewportWithScrollbar() string // model.go:2490 (1 caller @ :2284)
func (m Model) renderSelectionOnLine(line string, contentLine int) string // selection.go:132 (STAYS; passed into chatView.View)
```

---

### Task 1: Golden characterization net (pin current behavior)

Before moving anything, capture the main page's current render output for a fixture matrix as golden files. These pass against the CURRENT code and become the regression gate for Task 2.

**Files:**
- Create: `source/clients/cli/internal/ui/chat_view_golden_test.go`
- Create: `source/clients/cli/internal/ui/testdata/chatview/*.golden` (generated)

**Interfaces:**
- Consumes: current `Model` (`New`, `m.entries`, `m.refreshViewport()`, `m.renderViewportWithScrollbar()`), `theme.NewStyles(theme.Cracker())`.
- Produces: `renderFixture(t, name, entries, width, height, yOffset) string` test helper + committed goldens. Task 2 reuses this exact test unchanged.

- [ ] **Step 1: Write the golden test (with an `-update` flag)**

Create `chat_view_golden_test.go`:

```go
package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"charm.land/bubbles/v2/viewport"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

var updateGolden = flag.Bool("update", false, "regenerate chatview golden files")

// newRenderModel builds a Model sized for rendering only (no agent needed for
// refreshViewport/renderViewportWithScrollbar). Host reserves 2 cols (gap +
// scrollbar), mirroring relayout's contentW-2.
//
// TASK 2 REWIRE: after the extraction the viewport + md move into chatView.
// Change this body to `m.chat = newChatView(m.styles, m.palette, width-2, height)`
// and delete the viewport/md lines. Do NOT pass -update — the goldens must still
// match byte-for-byte; that match is the whole point of this file.
func newRenderModel(width, height int) Model {
	m := Model{styles: theme.NewStyles(theme.Cracker()), palette: theme.Cracker(), focusedToolIdx: -1}
	m.viewport = viewport.New(viewport.WithWidth(width-2), viewport.WithHeight(height))
	m.md = render.NewMarkdown(theme.CrackerMarkdownStyle())
	return m
}

// renderFixture renders entries through the MAIN-PAGE path and compares to the
// golden file, regenerating it under -update. yOffset scrolls before render.
func renderFixture(t *testing.T, name string, entries []*Entry, width, height, yOffset int) {
	t.Helper()
	m := newRenderModel(width, height)
	m.entries = entries
	m.refreshViewport()
	m.viewport.SetYOffset(yOffset)
	got := m.renderViewportWithScrollbar()

	path := filepath.Join("testdata", "chatview", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update once to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("render mismatch for %s (run -update to inspect diff)", name)
	}
}

func fixtureEntries() map[string][]*Entry {
	return map[string][]*Entry{
		"user_assistant_system": {
			{Role: RoleUser, Content: "how do I extract the transcript view"},
			{Role: RoleAssistant, Content: "You lift `renderEntry` into a `chatView`."},
			{Role: RoleSystem, Content: "done"},
		},
		"markdown_table_code": {
			{Role: RoleAssistant, Content: "Heading:\n\n# Title\n\nText with `code` and:\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```\n"},
		},
		"live_tail_open_fence": {
			{Role: RoleAssistant, Content: "Starting:\n\n```go\nfunc partial() {"},
		},
	}
}

func TestChatView_GoldenParity(t *testing.T) {
	for _, w := range []int{40, 120} {
		for name, entries := range fixtureEntries() {
			name, entries, w := name, entries, w
			t.Run(name+"_w"+itoa(w), func(t *testing.T) {
				renderFixture(t, name+"_w"+itoa(w), entries, w, 20, 0)
			})
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
```

Add imports `strconv` (for `itoa`). NOTE: this test references `newRenderMarkdown()` — if no such helper exists, replace `m.md = newRenderMarkdown()` with `m.md = render.NewMarkdown(theme.CrackerMarkdownStyle())` and import `render`. The fixtures deliberately exclude a streaming-empty placeholder entry (it is time-animated and non-deterministic); the placeholder is covered structurally in Task 3.

- [ ] **Step 2: Generate the goldens against current code**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestChatView_GoldenParity -update -count=1`
Expected: PASS (writes `testdata/chatview/*.golden`). Then run without `-update`:
Run: `go test ./internal/ui/ -run TestChatView_GoldenParity -count=1`
Expected: PASS (goldens match current code).

- [ ] **Step 3: Sanity-inspect one golden**

Run: `cd source/clients/cli && ls testdata/chatview/ && wc -l testdata/chatview/markdown_table_code_w120.golden`
Expected: six `.golden` files; the markdown one has multiple lines (table + code rules present).

- [ ] **Step 4: Commit the safety net**

```bash
git add source/clients/cli/internal/ui/chat_view_golden_test.go source/clients/cli/internal/ui/testdata/chatview/
git commit -m "test(cli): golden characterization net for main-page transcript render"
```

---

### Task 2: Extract `chatView`; `Model` delegates

Create `chatView`, move the viewport + markdown engine + render methods into it, and rewire `Model` to delegate. The Task 1 golden test must stay green **without** regeneration.

**Files:**
- Create: `source/clients/cli/internal/ui/chat_view.go`
- Modify: `source/clients/cli/internal/ui/model.go` (move methods out; add `chat *chatView`; rewire `New`, `relayout`, `refreshViewport`, `renderViewportWithScrollbar`, and all `m.viewport.*` / `m.viewportPlainLines` readers)

**Interfaces:**
- Consumes: the moved methods + the helpers listed in Global Constraints; `renderSelectionOnLine` (passed in).
- Produces:
  - `type turnStatus struct { activity string; start time.Time; tokOut int; model string; cloud bool }`
  - `func newChatView(s theme.Styles, p theme.Palette, w, h int) *chatView`
  - `func (c *chatView) SetSize(w, h int)`
  - `func (c *chatView) SetEntries(entries []*Entry)` — rebuilds content, preserves at-bottom follow, refreshes `plainLines`.
  - `func (c *chatView) SetFocusedTool(idx int)` / `func (c *chatView) SetTurnStatus(t turnStatus)`
  - `func (c *chatView) View(selOverlay func(line string, contentLine int) string) string`
  - scroll surface: `Width() int`, `Height() int`, `TotalLineCount() int`, `YOffset() int`, `SetYOffset(int)`, `AtBottom() bool`, `GotoBottom()`, `Update(tea.Msg) tea.Cmd`, `PlainLines() []string`.

- [ ] **Step 1: Create `chat_view.go` with the struct, constructor, and moved methods**

```go
package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// turnStatus carries the live-turn telemetry the host injects so chatView can
// render the animated pre-token placeholder without reaching into Model.
type turnStatus struct {
	activity string
	start    time.Time
	tokOut   int
	model    string
	cloud    bool
}

// chatView owns the transcript viewport + entry rendering. The host still owns
// the entry slice, the streaming state machine, and selection (step 1).
type chatView struct {
	width, height  int
	styles         theme.Styles
	palette        theme.Palette
	vp             viewport.Model
	md             *render.Markdown
	plainLines     []string
	focusedToolIdx int
	turn           turnStatus
}

func newChatView(s theme.Styles, p theme.Palette, w, h int) *chatView {
	vp := viewport.New(viewport.WithWidth(w), viewport.WithHeight(h))
	return &chatView{
		width: w, height: h, styles: s, palette: p, vp: vp,
		md:             render.NewMarkdown(theme.CrackerMarkdownStyle()),
		focusedToolIdx: -1,
	}
}

func (c *chatView) SetSize(w, h int) {
	c.width, c.height = w, h
	c.vp.SetWidth(w)
	c.vp.SetHeight(h)
}

func (c *chatView) SetFocusedTool(idx int)   { c.focusedToolIdx = idx }
func (c *chatView) SetTurnStatus(t turnStatus) { c.turn = t }

func (c *chatView) Width() int          { return c.vp.Width() }
func (c *chatView) Height() int         { return c.vp.Height() }
func (c *chatView) TotalLineCount() int { return c.vp.TotalLineCount() }
func (c *chatView) YOffset() int        { return c.vp.YOffset() }
func (c *chatView) SetYOffset(o int)    { c.vp.SetYOffset(o) }
func (c *chatView) AtBottom() bool      { return c.vp.AtBottom() }
func (c *chatView) GotoBottom()         { c.vp.GotoBottom() }
func (c *chatView) PlainLines() []string { return c.plainLines }

func (c *chatView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return cmd
}

// SetEntries rebuilds the viewport content from the host's entries. Mirrors the
// old Model.refreshViewport: blank-line spacing between turns (tool entries stay
// grouped), the plain-text mirror for selection, and auto-scroll only when
// already at bottom.
func (c *chatView) SetEntries(entries []*Entry) {
	wasAtBottom := c.vp.AtBottom()
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			if entries[i-1].Tool != nil && e.Tool != nil {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(c.renderEntry(e, i))
	}
	content := b.String()
	c.plainLines = plainLines(content)
	c.vp.SetContent(content)
	if wasAtBottom {
		c.vp.GotoBottom()
	}
}
```

Then MOVE these three methods from `model.go` into `chat_view.go`, changing the receiver `(m *Model)` → `(c *chatView)` and every `m.` field reference accordingly:
- `renderEntry` (`model.go:1631-1690`): `m.viewport.Width()` → `c.vp.Width()`; `m.styles` → `c.styles`; `m.focusedToolIdx` → `c.focusedToolIdx`; `m.renderAssistantMarkdown` → `c.renderAssistantMarkdown`; in the streaming-placeholder branch `m.turnActivity`→`c.turn.activity`, `time.Since(m.turnStart)`→`time.Since(c.turn.start)`, `m.turnTokOut`→`c.turn.tokOut`, `m.turnModel`→`c.turn.model`, `m.turnCloud`→`c.turn.cloud`. The free fns (`indentBlock`, `animateSpinnerGlyph`, `animateLimeSweep`, `renderToolEntry`, `turnStatusLine`) are called unchanged.
- `renderAssistantMarkdown` (`model.go:1695-1711`): `m.renderMdBlock`→`c.renderMdBlock`; `m.md`→`c.md`.
- `renderMdBlock` (`model.go:1718-1730`): `m.styles`→`c.styles`; `m.md`→`c.md`.

Leave `isHeadingBlock`, `trimBlankEdgeLines`, `codeRule`, `closeOpenFence` as package-level functions in `model.go` (unchanged — `chatView` calls them directly).

Finally add the `View` method (this is `renderViewportWithScrollbar`'s body with `m.viewport`→`c.vp`, `m.styles`→`c.styles`, and the selection call replaced by the injected `selOverlay`):

```go
// View renders the windowed transcript with the right-edge scrollbar column.
// selOverlay applies the host's selection highlight per content line (step 1);
// pass an identity func when there is no selection. In step 2 selection moves
// in and this param is removed.
func (c *chatView) View(selOverlay func(line string, contentLine int) string) string {
	body := c.vp.View()
	lines := strings.Split(body, "\n")
	height := c.vp.Height()
	col := scrollbarColumn(c.vp.TotalLineCount(), height, c.vp.YOffset())
	var b strings.Builder
	for i, line := range lines {
		contentLine := c.vp.YOffset() + i
		line = selOverlay(line, contentLine)
		line = ansi.Truncate(line, c.vp.Width(), "")
		b.WriteString(line)
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(c.styles.Border.Render("█"))
			case '░':
				b.WriteString(c.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
```

Add the `ansi` import (`github.com/charmbracelet/x/ansi`). Match the trailing-newline logic to the original `renderViewportWithScrollbar` exactly — read `model.go:2490-2525` and replicate its loop tail (the original may append `\n` differently; copy it verbatim, only swapping `m.`→`c.` and the selection call). The golden test will catch any divergence.

- [ ] **Step 2: Rewire `Model` — fields, `New`, delegators, and all viewport readers**

In `model.go`:

(a) Remove the `viewport viewport.Model`, `viewportPlainLines []string`, and `md *render.Markdown` fields from the `Model` struct (lines 88, 98, 102). Add `chat *chatView`.

(b) In `New` (around `model.go:246, 280, 287`): remove `vp := viewport.New(...)`, the `md:` literal, and `viewport: vp`. Add `chat: newChatView(styles, palette, 78, 10)` to the struct literal (use the same `styles`/`palette` values `New` already builds; 78≈80-2 placeholder, overridden by `relayout`).

(c) Replace `refreshViewport` body (`model.go:1592`) with the delegator:

```go
func (m *Model) refreshViewport() {
	m.chat.SetFocusedTool(m.focusedToolIdx)
	m.chat.SetTurnStatus(turnStatus{
		activity: m.turnActivity, start: m.turnStart, tokOut: m.turnTokOut,
		model: m.turnModel, cloud: m.turnCloud,
	})
	m.chat.SetEntries(m.entries)
}
```

(d) Replace `renderViewportWithScrollbar` body (`model.go:2490`) with:

```go
func (m Model) renderViewportWithScrollbar() string {
	return m.chat.View(m.renderSelectionOnLine)
}
```

(e) In `relayout` (`model.go:1464, 1487-1488`): replace `m.viewport.SetWidth(contentW - 2)` + `m.viewport.SetHeight(bodyH)` with `m.chat.SetSize(contentW-2, bodyH)`; replace `m.viewport.Width()` (line 1464 guard) with `m.chat.Width()`; replace `m.viewport.Height()` (line 1558) with `m.chat.Height()`.

(f) Rewire the remaining `m.viewport.*` readers to the `chatView` surface:
- `model.go:462` `m.viewport.Height()` → `m.chat.Height()`
- `model.go:530` `m.viewport, cmd = m.viewport.Update(msg)` → `cmd = m.chat.Update(msg)`
- `model.go:568` `m.viewport.Height()` → `m.chat.Height()`
- `model.go:581,618` `m.viewport.TotalLineCount()` → `m.chat.TotalLineCount()`
- `model.go:582,619` `m.viewport.SetYOffset(off)` → `m.chat.SetYOffset(off)`
- `model.go:617` `m.viewport.Height()` → `m.chat.Height()`
- `model.go:814` `m.viewport, cmd = m.viewport.Update(msg)` → `cmd = m.chat.Update(msg)`
- `model.go:1609` `m.viewportPlainLines = plainLines(content)` → DELETE (moved into `SetEntries`)
- any `m.viewportPlainLines` reader (selection) → `m.chat.PlainLines()`

Then grep to confirm none remain: `grep -n "m\.viewport\b\|m\.viewportPlainLines\|m\.md\b" internal/ui/model.go` must return nothing. If a `m.md` or `m.viewport` reader appears that is NOT in the list above, route it through `chatView` (add a forwarding method if needed) and note it in the report.

- [ ] **Step 3: Build, then run the golden gate + full suite**

Run: `cd source/clients/cli && go build ./...`
Expected: clean (no undefined `m.viewport`/`m.md`, no unused imports — remove `viewport`/`render` imports from `model.go` if now unused).

Run: `go test ./internal/ui/ -run TestChatView_GoldenParity -count=1`
Expected: PASS — **byte-identical**, goldens unchanged (do NOT pass `-update`). This proves zero behavior change.

Run: `go test ./... -count=1`
Expected: all packages PASS (existing selection/scroll/markdown tests still green).

- [ ] **Step 4: Commit**

```bash
git add source/clients/cli/internal/ui/chat_view.go source/clients/cli/internal/ui/model.go
git commit -m "refactor(cli): extract transcript rendering + viewport into chatView"
```

---

### Task 3: Direct `chatView` unit tests + placeholder seam

Add `chatView`-level unit tests for the scroll surface and the injected `turnStatus` placeholder (the one time-animated branch excluded from goldens), and a smoke check.

**Files:**
- Create: `source/clients/cli/internal/ui/chat_view_test.go`

**Interfaces:**
- Consumes: `newChatView`, `SetEntries`, `SetTurnStatus`, `SetFocusedTool`, `View`, the scroll surface.

- [ ] **Step 1: Write the unit tests**

```go
package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

func newTestChatView(w, h int) *chatView {
	return newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), w, h)
}

func TestChatView_ScrollSurfaceMatchesViewport(t *testing.T) {
	c := newTestChatView(40, 5)
	entries := make([]*Entry, 0, 30)
	for i := 0; i < 30; i++ {
		entries = append(entries, &Entry{Role: RoleSystem, Content: "line"})
	}
	c.SetEntries(entries)
	if c.TotalLineCount() < 30 {
		t.Fatalf("TotalLineCount = %d, want >= 30", c.TotalLineCount())
	}
	if !c.AtBottom() {
		t.Fatalf("fresh content should auto-follow to bottom")
	}
	c.SetYOffset(0)
	if c.YOffset() != 0 {
		t.Fatalf("SetYOffset(0) → YOffset %d", c.YOffset())
	}
	c.GotoBottom()
	if !c.AtBottom() {
		t.Fatalf("GotoBottom should land at bottom")
	}
}

func TestChatView_TurnStatusPlaceholder(t *testing.T) {
	c := newTestChatView(60, 10)
	c.SetTurnStatus(turnStatus{activity: "routing", start: time.Now().Add(-3 * time.Second), tokOut: 7, model: "opus", cloud: true})
	c.SetEntries([]*Entry{{Role: RoleAssistant, Streaming: true, Content: ""}})
	out := c.View(func(l string, _ int) string { return l })
	if !strings.Contains(out, "routing") {
		t.Errorf("placeholder should show the activity verb:\n%s", out)
	}
	if !strings.Contains(out, "opus") || !strings.Contains(out, "cloud") {
		t.Errorf("placeholder should show model + tier when set:\n%s", out)
	}
}

func TestChatView_ViewIdentityOverlayMatchesNoSelection(t *testing.T) {
	c := newTestChatView(50, 6)
	c.SetEntries([]*Entry{{Role: RoleUser, Content: "hello world"}})
	id := c.View(func(l string, _ int) string { return l })
	if strings.TrimSpace(id) == "" {
		t.Fatalf("View produced empty output")
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestChatView_ -count=1 -v`
Expected: PASS (scroll surface, placeholder, identity-overlay).

- [ ] **Step 3: Full build + suite + manual smoke**

Run: `go build ./... && go test ./... -count=1`
Expected: clean build, all packages PASS.

Manual (recommended): `go build -o bin/cercano-cli . && ./bin/cercano-cli` — chat renders, scrolls (wheel + drag), selection-copy works, a streamed turn shows the animated placeholder then prose, tool calls render — all visually identical to before.

- [ ] **Step 4: Commit**

```bash
git add source/clients/cli/internal/ui/chat_view_test.go
git commit -m "test(cli): chatView scroll surface + turn-status placeholder"
```

---

## Self-Review

**Spec coverage (against `design.md` Step 1):**
- Move `renderEntry`/`renderAssistantMarkdown`/`renderMdBlock` + viewport + scrollbar into `chatView` → Task 2 step 1–2.
- Host keeps entries/streaming/selection/scrollbar-drag → Task 2 keeps those in `Model`; only rendering + viewport move.
- `turnStatus` seam for the animated placeholder → Task 2 (struct + `SetTurnStatus`), Task 3 (test).
- Selection seam via callback → `View(selOverlay)`; host passes `renderSelectionOnLine` (Task 2 step 2d).
- Golden byte-identical gate → Task 1 (capture) + Task 2 step 3 (must stay green).
- `chatpane.go`/`scrollbar.go`/`scrollback_tool.go`/`selection.go` untouched → Global Constraints + Task 2 leaves helpers package-level.

**Placeholder scan:** No TBD/TODO. Two spots delegate to "copy the original verbatim" (the `renderEntry`/`renderViewportWithScrollbar` bodies) rather than re-pasting 60+ lines — this is a pure receiver/field rename of existing code, and the golden test is the exact correctness gate, so reproducing the bytes here would risk introducing drift the move itself must not have.

**Type consistency:** `chatView`, `turnStatus{activity,start,tokOut,model,cloud}`, `newChatView(s,p,w,h)`, `SetEntries`/`SetSize`/`SetFocusedTool`/`SetTurnStatus`/`View(selOverlay)`/`PlainLines` and the scroll surface are used identically across Tasks 2–3 and match the design's interface. `m.chat` is the field name throughout.

**Scope:** One component extraction; single plan. Steps 2–4 of the roadmap are explicitly out of scope.
