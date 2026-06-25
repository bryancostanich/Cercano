# History View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bordered `RowList` history picker with a content page that renders a markdown `# History` heading plus two-line, expandable conversation rows into the main chat region, using the main section's working, height-bounded scrollbar.

**Architecture:** The history view is a `contentPage` modeled directly on the existing `contextView` (`internal/ui/context_view.go`) — full-width content rendered through a scroll window with `scrollbarColumn`, implementing `contentPageScroller` so the model's wheel/drag wiring works. Rows are two lines (name + indented recap) with a `▸`/`▾` arrow; expanding opens an inline drawer with the full recap plus the last few turns, lazily fetched via `GetConversationTurns`. Selecting a row emits the existing `resumeRequestedMsg`, which the model already turns into `applyResume`.

**Tech Stack:** Go 1.21+, `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `github.com/charmbracelet/x/ansi`, the in-repo `render.Markdown` engine, `agentclient` (`ListConversations`, `GetConversationTurns`).

## Global Constraints

- Module: `cercano/source/clients/cli`. Build: `go build ./...`. Test: `go test ./... -count=1`. Run both from `source/clients/cli/`.
- Commit messages must not contain the word "Claude" anywhere; no Co-Authored-By trailers naming Claude.
- No new gRPC/proto. Expanded turns use the existing `agentclient.GetConversationTurns`.
- History is a `contentPage`, NOT a bordered `overlay.RowList` panel. It must never render a rounded border, and must clamp to `dashboardContentHeight(height)` so it never overflows the terminal.
- Follow `contextView` patterns for the scroller, async load message, and `handleClick` — do not invent new mechanisms.

### Reference types (already defined — do not redefine)

```go
// internal/ui/content_page.go
type contentPage interface {
	ID() contentPageID
	SetSize(width, height int)
	Update(tea.KeyPressMsg) (cmd tea.Cmd, closed bool)
	View() string
}
type contentPageScrollState struct{ Total, Height, Offset int }
type contentPageScroller interface {
	ScrollBy(delta int); ScrollTo(offset int); ScrollState() contentPageScrollState
}
const contentPageHistory contentPageID = "history" // already declared

// internal/ui/history_picker.go (to be retired in Task 6)
type resumeRequestedMsg struct{ ConversationID, Title string } // model handles this at model.go:1001

// source/server/pkg/agentclient
type ConversationInfo struct { // returned by ListConversations(ctx, projectDir, limit)
	ID, Title, Recap, Model string
	TurnCount               int
	LastTurnAt              time.Time
	// (other fields exist; only these are used here)
}
type ContextTurn struct { // returned by GetConversationTurns(ctx, convID)
	ID, Role, Kind, Preview, Body string
	Truncated                     bool
	EstTokens                     int
}

// internal/ui helpers already available:
//   dashboardContentHeight(height int) int   // usable content rows
//   dashboardPanelWidth(width int) int        // usable content width
//   scrollbarColumn(total, height, yOffset int) []rune  // '█' thumb / '░' track / ' '
//   padToWidth(s string, w int) string        // ANSI-aware right-pad
//   relativeTime(t time.Time) string          // "1h ago" (internal/ui/history_picker.go)
//   maxInt(a, b int) int; clampInt(v, lo, hi int) int
//   render.NewMarkdown(theme.CrackerMarkdownStyle()) *render.Markdown  // .Render(src, width) string
```

---

### Task 1: Shared scroll-window renderer (DRY extraction)

`contextView.renderScrollableContent` (`context_view.go:456`) contains the scroll-window + scrollbar paint loop the history view also needs. Extract it to a free function so both share one implementation.

**Files:**
- Create: `source/clients/cli/internal/ui/scrollable.go`
- Test: `source/clients/cli/internal/ui/scrollable_test.go`
- Modify: `source/clients/cli/internal/ui/context_view.go` (`renderScrollableContent`, ~456)

**Interfaces:**
- Produces: `func renderScrollable(lines []string, height, panelW, offset int, styles theme.Styles) string` — renders exactly `height` rows starting at `offset` (caller pre-clamps `offset`), each truncated/padded to `panelW`, with a styled scrollbar glyph column appended. Pure (no mutation).

- [ ] **Step 1: Write the failing test**

Create `scrollable_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

func TestRenderScrollable_WindowsAndPaintsBar(t *testing.T) {
	s := theme.NewStyles(theme.Cracker())
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	out := renderScrollable(lines, 5, 30, 0, s)
	rows := strings.Split(out, "\n")
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (height window)", len(rows))
	}
	for _, r := range rows {
		if w := lipgloss.Width(r); w != 32 { // panelW(30) + 1 gutter space + 1 bar glyph
			t.Fatalf("row width = %d, want 32 (panelW + space + bar)", w)
		}
	}
	if !strings.ContainsRune(out, '█') {
		t.Fatalf("expected a scrollbar thumb glyph")
	}
}

func TestRenderScrollable_ShorterThanHeightPadsBlank(t *testing.T) {
	s := theme.NewStyles(theme.Cracker())
	out := renderScrollable([]string{"a", "b"}, 4, 10, 0, s)
	if rows := strings.Split(out, "\n"); len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (padded to height)", len(rows))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestRenderScrollable -count=1`
Expected: FAIL — `undefined: renderScrollable`.

- [ ] **Step 3: Implement `renderScrollable` and delegate from contextView**

Create `scrollable.go`:

```go
package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// renderScrollable paints `height` rows of `lines` starting at `offset`, each
// truncated/padded to panelW, with a scrollbar glyph column appended after a
// one-space gutter. offset must already be clamped by the caller. Pure.
func renderScrollable(lines []string, height, panelW, offset int, styles theme.Styles) string {
	if height < 1 {
		height = 1
	}
	col := scrollbarColumn(len(lines), height, offset)
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if src := offset + i; src >= 0 && src < len(lines) {
			line = lines[src]
		}
		b.WriteString(padToWidth(ansi.Truncate(line, panelW, ""), panelW))
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(styles.Border.Render("█"))
			case '░':
				b.WriteString(styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
```

Then replace the body of `contextView.renderScrollableContent` (`context_view.go:456`) with a clamp + delegate:

```go
func (c *contextView) renderScrollableContent(full string, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(full, "\n")
	c.scrollOffset = clampInt(c.scrollOffset, 0, maxInt(0, len(lines)-height))
	return renderScrollable(lines, height, dashboardPanelWidth(c.width), c.scrollOffset, c.styles)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestRenderScrollable' -count=1 && go test ./internal/ui/ -count=1`
Expected: PASS — new tests green; all existing contextView tests still pass (delegation is behavior-preserving).

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/scrollable.go source/clients/cli/internal/ui/scrollable_test.go source/clients/cli/internal/ui/context_view.go
git commit -m "refactor(cli): extract renderScrollable shared by context + history views"
```

---

### Task 2: historyView scaffold — rows, content builder, scroller, View

Build the content page: load the conversation list, render `# History` + collapsed two-line rows, expose scrolling. Selection cursor + expand come in later tasks.

**Files:**
- Create: `source/clients/cli/internal/ui/history_view.go`
- Test: `source/clients/cli/internal/ui/history_view_test.go`

**Interfaces:**
- Consumes: `renderScrollable` (Task 1), `dashboardContentHeight`, `dashboardPanelWidth`, `relativeTime`, `render.NewMarkdown`, `agentclient.ConversationInfo`.
- Produces:
  - `type histRow struct { id, name, recap, meta string; expanded, turnsLoaded bool; turns []agentclient.ContextTurn }`
  - `type histLineMeta struct { row int; arrowCell bool }`
  - `func newHistoryView(ag *agentclient.Client, p theme.Palette, s theme.Styles, currentID string, w, h int) (*historyView, tea.Cmd)`
  - `func (h *historyView) rowsLines() ([]string, []histLineMeta)` — `# History` heading line(s) then two lines per row; the heading lines carry `histLineMeta{row: -1}`.
  - `historyView` implements `contentPage` (`ID`/`SetSize`/`Update`/`View`) and `contentPageScroller` (`ScrollBy`/`ScrollTo`/`ScrollState`). `Update` here only handles `esc`/`q` (close) + scroll keys; nav/resume land in Task 3.

- [ ] **Step 1: Write the failing tests**

Create `history_view_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// newTestHistoryView builds a historyView from hand-made rows, bypassing the
// agent (newHistoryView needs a live client; the pure render/nav logic does not).
func newTestHistoryView(rows []histRow, w, h int) *historyView {
	s := theme.NewStyles(theme.Cracker())
	return &historyView{
		palette: theme.Cracker(), styles: s,
		width: w, height: h, rows: rows, cursor: 0,
		md: newHistoryMarkdown(),
	}
}

func TestHistoryRowsLines_HeadingAndTwoLineRows(t *testing.T) {
	rows := []histRow{
		{id: "a", name: "read the cercano readme", recap: "Familiarized with the CLI", meta: "14 turns · 1h ago · opus-4-7"},
	}
	h := newTestHistoryView(rows, 100, 30)
	lines, meta := h.rowsLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "History") {
		t.Fatalf("expected a History heading, got:\n%s", joined)
	}
	// Find the row's two lines via meta (row index 0, non-heading).
	var rowLines []string
	for i, m := range meta {
		if m.row == 0 {
			rowLines = append(rowLines, lines[i])
		}
	}
	if len(rowLines) != 2 {
		t.Fatalf("collapsed row should be 2 lines, got %d", len(rowLines))
	}
	if !strings.Contains(rowLines[0], "read the cercano readme") || !strings.Contains(rowLines[0], "14 turns") {
		t.Errorf("line 1 should carry name + meta: %q", rowLines[0])
	}
	if !strings.Contains(rowLines[1], "Familiarized") {
		t.Errorf("line 2 should carry the recap: %q", rowLines[1])
	}
}

func TestHistoryRowsLines_LongContentDoesNotExceedWidth(t *testing.T) {
	rows := []histRow{
		{id: "a", name: strings.Repeat("long name ", 20), recap: strings.Repeat("long recap ", 20), meta: "2 turns · 1d ago · m"},
	}
	h := newTestHistoryView(rows, 80, 30)
	lines, _ := h.rowsLines()
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > dashboardPanelWidth(80) {
			t.Fatalf("line wider than panel (%d > %d): %q", w, dashboardPanelWidth(80), ln)
		}
	}
}

func TestHistoryScrollState_BoundsToContentHeight(t *testing.T) {
	rows := make([]histRow, 50)
	for i := range rows {
		rows[i] = histRow{id: "x", name: "n", recap: "r", meta: "m"}
	}
	h := newTestHistoryView(rows, 100, 24)
	st := h.ScrollState()
	if st.Height != dashboardContentHeight(24) {
		t.Errorf("Height = %d, want %d", st.Height, dashboardContentHeight(24))
	}
	if st.Total <= st.Height {
		t.Errorf("Total (%d) should exceed Height (%d) for 50 rows", st.Total, st.Height)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestHistoryRowsLines|TestHistoryScrollState' -count=1`
Expected: FAIL — `undefined: histRow`, `undefined: historyView`, `undefined: newHistoryMarkdown`.

- [ ] **Step 3: Implement the scaffold**

Create `history_view.go`:

```go
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// histRow is one conversation in the history list.
type histRow struct {
	id, name, recap, meta string
	expanded, turnsLoaded bool
	turns                 []agentclient.ContextTurn
}

// histLineMeta parallels each rendered line for hit-testing. row == -1 marks a
// heading/spacer line (not selectable); arrowCell marks the row's first line
// (where the ▸/▾ arrow sits and a click toggles expand).
type histLineMeta struct {
	row       int
	arrowCell bool
}

type historyView struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	currentID     string
	rows          []histRow
	cursor        int
	scrollOffset  int
	md            *render.Markdown
}

func newHistoryMarkdown() *render.Markdown { return render.NewMarkdown(theme.CrackerMarkdownStyle()) }

// newHistoryView loads the conversation list synchronously (matching the old
// picker + contextView) and returns the page. The turn drawer loads lazily.
func newHistoryView(ag *agentclient.Client, p theme.Palette, s theme.Styles, currentID string, w, h int) (*historyView, tea.Cmd) {
	hv := &historyView{
		palette: p, styles: s, agent: ag, currentID: currentID,
		width: w, height: h, cursor: 0, md: newHistoryMarkdown(),
	}
	hv.rows = loadHistoryRows(ag)
	return hv, nil
}

// loadHistoryRows snapshots conversations into histRows (newest first as the
// agent returns them).
func loadHistoryRows(ag *agentclient.Client) []histRow {
	if ag == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	convs, err := ag.ListConversations(ctx, "", 100)
	if err != nil {
		return nil
	}
	rows := make([]histRow, 0, len(convs))
	for _, c := range convs {
		name := c.Title
		if name == "" {
			name = "(untitled)"
		}
		meta := fmt.Sprintf("%d turn", c.TurnCount)
		if c.TurnCount != 1 {
			meta += "s"
		}
		meta += " · " + relativeTime(c.LastTurnAt)
		if c.Model != "" {
			meta += " · " + c.Model
		}
		rows = append(rows, histRow{id: c.ID, name: name, recap: c.Recap, meta: meta})
	}
	return rows
}

func (h *historyView) ID() contentPageID { return contentPageHistory }

func (h *historyView) SetSize(w, hgt int) {
	h.width = w
	h.height = hgt
	h.clampScroll()
}

// Update: nav/resume land in Task 3. Here, only close + scroll keys so the
// contentPage contract is valid.
func (h *historyView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "pgup", "ctrl+b":
		h.ScrollBy(-dashboardContentHeight(h.height))
	case "pgdown", "ctrl+f":
		h.ScrollBy(dashboardContentHeight(h.height))
	}
	return nil, false
}

// rowsLines renders the # History heading then two lines per row, returning
// parallel line + meta slices. Indent and truncation keep every line within the
// panel width.
func (h *historyView) rowsLines() ([]string, []histLineMeta) {
	var lines []string
	var meta []histLineMeta
	add := func(s string, m histLineMeta) { lines = append(lines, s); meta = append(meta, m) }

	panelW := dashboardPanelWidth(h.width)
	for _, hl := range strings.Split(h.md.Render("# History", panelW), "\n") {
		add(hl, histLineMeta{row: -1})
	}
	add("", histLineMeta{row: -1})

	if len(h.rows) == 0 {
		add(h.styles.Muted.Render("  (no saved conversations)"), histLineMeta{row: -1})
		return lines, meta
	}

	for i := range h.rows {
		h.appendRow(&lines, &meta, i, panelW)
	}
	return lines, meta
}

// appendRow renders one collapsed row: line 1 = arrow + name + right-aligned
// meta; line 2 = indented recap preview. (Expanded drawer: Task 5.)
func (h *historyView) appendRow(lines *[]string, meta *[]histLineMeta, i, panelW int) {
	add := func(s string, m histLineMeta) { *lines = append(*lines, s); *meta = append(*meta, m) }
	r := h.rows[i]

	arrow := "▸ "
	nameStyle := h.styles.Muted
	if i == h.cursor {
		arrow = h.styles.Accent.Render("▸ ")
		nameStyle = h.styles.Bright
	} else {
		arrow = h.styles.Dim.Render(arrow)
	}

	// Line 1: " <arrow><name padded>  <meta>" budgeted so meta sits at the right.
	metaCell := h.styles.Muted.Render(r.meta)
	metaW := lipgloss.Width(metaCell)
	const lead = 1 + 2 // leading space + arrow cell
	nameW := panelW - lead - metaW - 2 // 2-space gap before meta
	if nameW < 8 {
		nameW = 8
	}
	nameTxt := ansi.Truncate(r.name, nameW, "…")
	nameCell := nameStyle.Render(nameTxt) + strings.Repeat(" ", maxInt(0, nameW-lipgloss.Width(nameTxt)))
	line1 := " " + arrow + nameCell + "  " + metaCell
	add(line1, histLineMeta{row: i, arrowCell: true})

	// Line 2: indented recap preview.
	recap := r.recap
	if strings.TrimSpace(recap) == "" {
		recap = "(no recap)"
	}
	indent := "      "
	recapTxt := ansi.Truncate(recap, maxInt(8, panelW-lipgloss.Width(indent)), "…")
	add(indent+h.styles.Primary.Render(recapTxt), histLineMeta{row: i})
}

func (h *historyView) View() string {
	lines, _ := h.rowsLines()
	height := dashboardContentHeight(h.height)
	h.scrollOffset = clampInt(h.scrollOffset, 0, maxInt(0, len(lines)-height))
	return renderScrollable(lines, height, dashboardPanelWidth(h.width), h.scrollOffset, h.styles)
}

// --- scroller ---

func (h *historyView) ScrollBy(delta int)  { h.scrollOffset += delta; h.clampScroll() }
func (h *historyView) ScrollTo(offset int) { h.scrollOffset = offset; h.clampScroll() }
func (h *historyView) ScrollState() contentPageScrollState {
	total := h.lineCount()
	height := dashboardContentHeight(h.height)
	return contentPageScrollState{Total: total, Height: height, Offset: clampInt(h.scrollOffset, 0, maxInt(0, total-height))}
}
func (h *historyView) clampScroll() { h.scrollOffset = h.ScrollState().Offset }
func (h *historyView) lineCount() int { l, _ := h.rowsLines(); return len(l) }
```

Add the `lipgloss` import to the file's import block (`"charm.land/lipgloss/v2"`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestHistoryRowsLines|TestHistoryScrollState' -count=1 -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/history_view.go source/clients/cli/internal/ui/history_view_test.go
git commit -m "feat(cli): history view scaffold — heading + two-line rows + scroller"
```

---

### Task 3: Selection nav + resume/close

Add the selected-row cursor movement (keeping it on screen) and `enter` → resume the selected conversation, `esc`/`q` → close.

**Files:**
- Modify: `source/clients/cli/internal/ui/history_view.go` (`Update`, add `moveCursor`/`scrollToCursor`)
- Test: `source/clients/cli/internal/ui/history_view_test.go`

**Interfaces:**
- Consumes: `resumeRequestedMsg` (from `history_picker.go`, still present until Task 6).
- Produces: `Update` returns `(cmd, closed)` where `enter` yields a `tea.Cmd` emitting `resumeRequestedMsg{ConversationID, Title}` and `closed == true`; `up`/`down` move `h.cursor` (clamped) and re-clamp scroll so the selected row's first line is within the window.

- [ ] **Step 1: Write the failing tests**

Add to `history_view_test.go`:

```go
func TestHistoryUpdate_EnterResumesSelected(t *testing.T) {
	rows := []histRow{{id: "a", name: "first"}, {id: "b", name: "second"}}
	h := newTestHistoryView(rows, 100, 30)
	h.cursor = 1
	cmd, closed := h.Update(keyPress("enter"))
	if !closed {
		t.Fatalf("enter should close the page")
	}
	if cmd == nil {
		t.Fatalf("enter should return a resume command")
	}
	msg := cmd()
	rr, ok := msg.(resumeRequestedMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want resumeRequestedMsg", msg)
	}
	if rr.ConversationID != "b" {
		t.Errorf("resumed %q, want b", rr.ConversationID)
	}
}

func TestHistoryUpdate_DownMovesCursorClamped(t *testing.T) {
	rows := []histRow{{id: "a"}, {id: "b"}}
	h := newTestHistoryView(rows, 100, 30)
	h.Update(keyPress("down"))
	if h.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", h.cursor)
	}
	h.Update(keyPress("down")) // clamp at last
	if h.cursor != 1 {
		t.Fatalf("cursor = %d, want clamped at 1", h.cursor)
	}
}
```

Add this helper to `history_view_test.go` (or a shared test helper file if one exists — grep `func keyPress` first; only add if absent):

```go
func keyPress(s string) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Text: s, Code: keyCodeFor(s)}) }
```

NOTE for implementer: construct the `tea.KeyPressMsg` the way existing ui tests do — grep `KeyPressMsg{` / `tea.Key{` under `internal/ui/*_test.go` and copy that exact construction. If a helper already exists, use it and skip adding `keyPress`. The assertions above only depend on `msg.String()` returning `"enter"` / `"down"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestHistoryUpdate' -count=1`
Expected: FAIL — `enter`/`down` are not handled yet (`closed` is false, cursor unchanged).

- [ ] **Step 3: Implement nav + resume in Update**

In `history_view.go`, replace the `Update` method with:

```go
func (h *historyView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "up", "k":
		h.moveCursor(-1)
	case "down", "j":
		h.moveCursor(1)
	case "enter":
		if h.cursor < 0 || h.cursor >= len(h.rows) {
			return nil, false
		}
		r := h.rows[h.cursor]
		return func() tea.Msg { return resumeRequestedMsg{ConversationID: r.id, Title: r.name} }, true
	case "pgup", "ctrl+b":
		h.ScrollBy(-dashboardContentHeight(h.height))
	case "pgdown", "ctrl+f":
		h.ScrollBy(dashboardContentHeight(h.height))
	}
	return nil, false
}

// moveCursor shifts the selection by dir (clamped) and scrolls so the selected
// row's first line stays within the viewport window.
func (h *historyView) moveCursor(dir int) {
	if len(h.rows) == 0 {
		return
	}
	h.cursor = clampInt(h.cursor+dir, 0, len(h.rows)-1)
	h.scrollToCursor()
}

// scrollToCursor adjusts scrollOffset so the selected row's first line is within
// [scrollOffset, scrollOffset+height).
func (h *historyView) scrollToCursor() {
	_, meta := h.rowsLines()
	first := -1
	for i, m := range meta {
		if m.row == h.cursor {
			first = i
			break
		}
	}
	if first < 0 {
		return
	}
	height := dashboardContentHeight(h.height)
	if first < h.scrollOffset {
		h.scrollOffset = first
	} else if first >= h.scrollOffset+height {
		h.scrollOffset = first - height + 1
	}
	h.clampScroll()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestHistory' -count=1 && go test ./internal/ui/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/history_view.go source/clients/cli/internal/ui/history_view_test.go
git commit -m "feat(cli): history view selection nav + enter-to-resume"
```

---

### Task 4: Wire historyView into the model; retire the picker

Swap the two construction sites from `newHistoryPicker` to `newHistoryView`, and delete the bordered picker.

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (`-r` boot ~493; `ResultOpenHistoryPicker` ~1167)
- Delete: `source/clients/cli/internal/ui/history_picker.go` — BUT first move `resumeRequestedMsg` and `relativeTime` into `history_view.go` (both are still used).

**Interfaces:**
- Consumes: `newHistoryView` (Task 2). The model already handles `resumeRequestedMsg` (model.go:1001) and routes contentPage wheel/drag generically.

- [ ] **Step 1: Move shared symbols, then delete the picker**

`resumeRequestedMsg` (history_picker.go:19-22) and `relativeTime` (history_picker.go:123-137) are used by `history_view.go`. Move both verbatim into `history_view.go` (top, after imports), then delete `history_picker.go`:

```bash
git rm source/clients/cli/internal/ui/history_picker.go
```

Add to `history_view.go`:

```go
// resumeRequestedMsg is emitted when the user selects a conversation; the root
// model turns it into applyResume + an active-conversation switch.
type resumeRequestedMsg struct {
	ConversationID string
	Title          string
}

// relativeTime renders a coarse "5m ago" / "3h ago" / "2d ago" string.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 60*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
```

(Note: the old picker used a malformed `"2026-01-02"` layout; the correct Go reference layout is `"2006-01-02"`. Use the corrected form.)

- [ ] **Step 2: Swap the construction sites**

In `model.go` `-r` boot (~493), replace:

```go
			hp, _ := newHistoryPicker(m.agent, m.palette, m.styles, m.width, m.height, m.convID)
			m.content = hp
```

with:

```go
			hv, _ := newHistoryView(m.agent, m.palette, m.styles, m.convID, m.width, m.height)
			m.content = hv
```

In `model.go` `case slash.ResultOpenHistoryPicker:` (~1166), replace its two body lines with:

```go
	case slash.ResultOpenHistoryPicker:
		hv, _ := newHistoryView(m.agent, m.palette, m.styles, m.convID, m.width, m.height)
		m.content = hv
```

- [ ] **Step 3: Build and run the full suite**

Run: `go build ./... && go test ./... -count=1`
Expected: build clean (no remaining references to `newHistoryPicker`/`historyPicker`); all tests pass. If the build reports `newHistoryPicker` referenced elsewhere, grep `grep -rn "historyPicker" internal/` and update those call sites to `historyView`.

- [ ] **Step 4: Manual smoke (optional but recommended)**

Run: `go build -o bin/cercano-cli . && ./bin/cercano-cli -r`
Expected: a `History` heading and the conversation list render in the main section (no rounded border); `↑/↓` move the highlight; the right-edge scrollbar tracks; `enter` resumes; `esc` returns to chat.

- [ ] **Step 5: Commit**

```bash
git add -A source/clients/cli/internal/ui/
git commit -m "feat(cli): render history in the main viewport; retire bordered picker"
```

---

### Task 5: Expand/collapse drawer with lazy turn fetch

Add `→`/`←` to expand/collapse the selected row, an inline drawer showing the full recap + last 3 prose turns, and lazy loading via `GetConversationTurns`.

**Files:**
- Modify: `source/clients/cli/internal/ui/history_view.go` (`Update`, `appendRow`, add fetch + `applyTurns` + `historyTurnsLoadedMsg`)
- Modify: `source/clients/cli/internal/ui/model.go` (add a `historyTurnsLoadedMsg` case near `contextSnapshotMsg` ~881)
- Test: `source/clients/cli/internal/ui/history_view_test.go`

**Interfaces:**
- Produces:
  - `type historyTurnsLoadedMsg struct { id string; turns []agentclient.ContextTurn }`
  - `func (h *historyView) applyTurns(id string, turns []agentclient.ContextTurn)`
  - `func historyTailLines(turns []agentclient.ContextTurn, n, width int, styles theme.Styles) []string` — last `n` PROSE turns (skip `tool_use`/`tool_result` kinds), each `role · preview` clipped to width.
  - `Update`: `right`/`l` expands selected (returns a fetch `tea.Cmd` on first expand of an unloaded row), `left`/`h` collapses.

- [ ] **Step 1: Write the failing tests**

Add to `history_view_test.go`:

```go
func TestHistoryTailLines_LastThreeProseSkipsTools(t *testing.T) {
	s := theme.NewStyles(theme.Cracker())
	turns := []agentclient.ContextTurn{
		{Role: "user", Kind: "text", Preview: "u1"},
		{Role: "assistant", Kind: "tool_use", Preview: "Read(...)"},
		{Role: "user", Kind: "tool_result", Preview: "...output..."},
		{Role: "assistant", Kind: "text", Preview: "a1"},
		{Role: "user", Kind: "text", Preview: "u2"},
		{Role: "assistant", Kind: "text", Preview: "a2"},
	}
	got := historyTailLines(turns, 3, 80, s)
	if len(got) != 3 {
		t.Fatalf("got %d tail lines, want 3", len(got))
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "Read(") || strings.Contains(joined, "output") {
		t.Errorf("tool turns should be skipped:\n%s", joined)
	}
	if !strings.Contains(joined, "a2") || !strings.Contains(joined, "u2") || !strings.Contains(joined, "a1") {
		t.Errorf("expected last 3 prose previews a1,u2,a2:\n%s", joined)
	}
}

func TestHistoryExpand_ShowsDrawerAndLoading(t *testing.T) {
	rows := []histRow{{id: "a", name: "n", recap: "full recap text", meta: "m"}}
	h := newTestHistoryView(rows, 100, 30)
	h.rows[0].expanded = true // not yet loaded
	lines, _ := h.rowsLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "loading") {
		t.Errorf("unloaded expanded row should show loading…:\n%s", joined)
	}
}

func TestHistoryApplyTurns_FillsAndRenders(t *testing.T) {
	rows := []histRow{{id: "a", name: "n", recap: "rc", meta: "m", expanded: true}}
	h := newTestHistoryView(rows, 100, 30)
	h.applyTurns("a", []agentclient.ContextTurn{{Role: "assistant", Kind: "text", Preview: "hello-turn"}})
	if !h.rows[0].turnsLoaded {
		t.Fatalf("applyTurns should mark turnsLoaded")
	}
	lines, _ := h.rowsLines()
	if !strings.Contains(strings.Join(lines, "\n"), "hello-turn") {
		t.Errorf("expanded+loaded row should show the turn preview")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestHistoryTailLines|TestHistoryExpand|TestHistoryApplyTurns' -count=1`
Expected: FAIL — `undefined: historyTailLines`, no drawer/loading lines, no `applyTurns`.

- [ ] **Step 3: Implement expand + fetch + drawer**

In `history_view.go`, extend `Update` (add cases inside the existing switch, before `pgup`):

```go
	case "right", "l":
		return h.expandSelected(), false
	case "left", "h":
		if h.cursor >= 0 && h.cursor < len(h.rows) {
			h.rows[h.cursor].expanded = false
		}
```

Add the expand/fetch/apply/tail logic:

```go
// expandSelected marks the selected row expanded and, if its turns aren't loaded
// yet, returns a Cmd that fetches them.
func (h *historyView) expandSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.rows) {
		return nil
	}
	h.rows[h.cursor].expanded = true
	r := h.rows[h.cursor]
	if r.turnsLoaded || h.agent == nil {
		return nil
	}
	id := r.id
	ag := h.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		turns, err := ag.GetConversationTurns(ctx, id)
		if err != nil {
			turns = nil
		}
		return historyTurnsLoadedMsg{id: id, turns: turns}
	}
}

// historyTurnsLoadedMsg carries lazily-fetched turns back to the model, which
// routes them to applyTurns.
type historyTurnsLoadedMsg struct {
	id    string
	turns []agentclient.ContextTurn
}

func (h *historyView) applyTurns(id string, turns []agentclient.ContextTurn) {
	for i := range h.rows {
		if h.rows[i].id == id {
			h.rows[i].turns = turns
			h.rows[i].turnsLoaded = true
			return
		}
	}
}

// historyTailLines renders the last n PROSE turns (tool_use/tool_result skipped)
// as indented "role · preview" lines, each clipped to width.
func historyTailLines(turns []agentclient.ContextTurn, n, width int, styles theme.Styles) []string {
	var prose []agentclient.ContextTurn
	for _, t := range turns {
		if t.Kind == "tool_use" || t.Kind == "tool_result" {
			continue
		}
		prose = append(prose, t)
	}
	if len(prose) > n {
		prose = prose[len(prose)-n:]
	}
	indent := "        "
	w := maxInt(8, width-lipgloss.Width(indent))
	out := make([]string, 0, len(prose))
	for _, t := range prose {
		line := t.Role + " · " + strings.TrimSpace(t.Preview)
		out = append(out, indent+styles.Muted.Render(ansi.Truncate(line, w, "…")))
	}
	return out
}
```

Extend `appendRow` to render the drawer when `r.expanded`. Add this block at the END of `appendRow` (after the recap line `add(...)`):

```go
	if r.expanded {
		panelInner := panelW
		// Full recap wrapped, indented under the row.
		indent := "      "
		recapFull := strings.TrimSpace(r.recap)
		if recapFull == "" {
			recapFull = "(no recap)"
		}
		for _, l := range strings.Split(ansi.Wrap(recapFull, maxInt(8, panelInner-lipgloss.Width(indent)), ""), "\n") {
			add(indent+h.styles.Muted.Render(l), histLineMeta{row: i})
		}
		if !r.turnsLoaded {
			add(indent+h.styles.Dim.Render("loading…"), histLineMeta{row: i})
		} else if len(r.turns) > 0 {
			add(indent+h.styles.Dim.Render("recent:"), histLineMeta{row: i})
			for _, l := range historyTailLines(r.turns, 3, panelInner, h.styles) {
				add(l, histLineMeta{row: i})
			}
		}
	}
```

Also flip the arrow to `▾` when expanded. In `appendRow`, change the arrow setup:

```go
	glyph := "▸ "
	if r.expanded {
		glyph = "▾ "
	}
	nameStyle := h.styles.Muted
	var arrow string
	if i == h.cursor {
		arrow = h.styles.Accent.Render(glyph)
		nameStyle = h.styles.Bright
	} else {
		arrow = h.styles.Dim.Render(glyph)
	}
```

(Replace the earlier fixed `arrow := "▸ "` / nameStyle block from Task 2 with this.)

Add `historyTurnsLoadedMsg` routing in `model.go`, right after the `contextSnapshotMsg` case (~885):

```go
	case historyTurnsLoadedMsg:
		if hv, ok := m.content.(*historyView); ok {
			hv.applyTurns(msg.id, msg.turns)
		}
		return m, nil
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run 'TestHistory' -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/history_view.go source/clients/cli/internal/ui/history_view_test.go source/clients/cli/internal/ui/model.go
git commit -m "feat(cli): history row expand drawer with lazy turn fetch"
```

---

### Task 6: Clickable expand arrow

Route left-clicks on the history page to the row arrow, mirroring the contextView click path.

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (`MouseClickMsg`, contentPage branch ~548)
- Modify: `source/clients/cli/internal/ui/history_view.go` (add `handleClick`)
- Test: `source/clients/cli/internal/ui/history_view_test.go`

**Interfaces:**
- Produces: `func (h *historyView) handleClick(x, yLocal int) (cmd tea.Cmd, handled bool)` — `yLocal` is the click row relative to the page's top content row (`mouse.Y - m.contentTop()`); maps `scrollOffset+yLocal` → `histLineMeta`; if it's an `arrowCell`, selects that row and toggles expand (returning a fetch Cmd when expanding an unloaded row).

- [ ] **Step 1: Write the failing test**

Add to `history_view_test.go`:

```go
func TestHistoryHandleClick_TogglesArrowRow(t *testing.T) {
	rows := []histRow{{id: "a", name: "n0", recap: "r0", meta: "m"}, {id: "b", name: "n1", recap: "r1", meta: "m"}}
	h := newTestHistoryView(rows, 100, 40)
	h.agent = nil // no fetch; expand still toggles

	// Find the screen row (offset 0) of row 1's arrow line via meta.
	_, meta := h.rowsLines()
	arrowY := -1
	for i, mt := range meta {
		if mt.row == 1 && mt.arrowCell {
			arrowY = i
			break
		}
	}
	if arrowY < 0 {
		t.Fatal("could not find row 1 arrow line")
	}
	_, handled := h.handleClick(1, arrowY) // x=1 is within the arrow cell
	if !handled {
		t.Fatalf("click on arrow cell should be handled")
	}
	if !h.rows[1].expanded {
		t.Errorf("clicking the arrow should expand row 1")
	}
	if h.cursor != 1 {
		t.Errorf("clicking a row should select it; cursor=%d", h.cursor)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestHistoryHandleClick -count=1`
Expected: FAIL — `undefined: h.handleClick`.

- [ ] **Step 3: Implement handleClick + model routing**

Add to `history_view.go`:

```go
// handleClick toggles a row's expansion when the click lands on its arrow cell,
// selecting that row. yLocal is the click row relative to the page's top content
// row (mouse.Y - contentTop). Returns a fetch Cmd when expanding an unloaded row.
func (h *historyView) handleClick(x, yLocal int) (tea.Cmd, bool) {
	if yLocal < 0 {
		return nil, false
	}
	_, meta := h.rowsLines()
	idx := h.scrollOffset + yLocal
	if idx < 0 || idx >= len(meta) {
		return nil, false
	}
	m := meta[idx]
	if m.row < 0 || !m.arrowCell {
		return nil, false
	}
	// The arrow sits in the leading columns: " ▸ " → x 1-2.
	if x > 2 {
		return nil, false
	}
	h.cursor = m.row
	if h.rows[m.row].expanded {
		h.rows[m.row].expanded = false
		return nil, true
	}
	return h.expandSelected(), true
}
```

In `model.go` `MouseClickMsg`, the contentPage branch (~548), add a history case beside the contextView one:

```go
			if cv, ok := m.content.(*contextView); ok && mouse.Button == tea.MouseLeft {
				if cv.handleClick(mouse.X, mouse.Y-m.contentTop()) {
					return m, nil
				}
			}
			if hv, ok := m.content.(*historyView); ok && mouse.Button == tea.MouseLeft {
				if cmd, handled := hv.handleClick(mouse.X, mouse.Y-m.contentTop()); handled {
					return m, cmd
				}
			}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ui/ -run TestHistory -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/history_view.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/history_view_test.go
git commit -m "feat(cli): click history row arrow to expand"
```

---

## Self-Review

**Spec coverage:**
- "Render history in main section, no bordered block" → Task 2 (contentPage like contextView, `renderScrollable`, no border) + Task 4 (wiring replaces the panel).
- "Markdown H1 History, then the list" → Task 2 (`md.Render("# History", …)` heading in `rowsLines`).
- "Main viewport's working scrollbar" → Task 1 + Task 2 (`renderScrollable` + `scrollbarColumn`; `contentPageScroller` for wheel/drag, already wired in model).
- "Never past terminal bottom" → Task 2 (`View` clamps to `dashboardContentHeight`).
- "Two-line rows: name + indented preview" → Task 2 (`appendRow`).
- "Clickable expand arrow → more preview (full recap + last few turns)" → Task 5 (drawer + lazy fetch + `historyTailLines`) + Task 6 (click).
- "enter resumes, esc to chat" → Task 3 (`resumeRequestedMsg`, close).
- "`-r` and `/history` both" → Task 4 (both construction sites swapped).

**Placeholder scan:** No TBD/TODO. The one implementer judgment call (constructing `tea.KeyPressMsg` in tests) is explicitly delegated to "copy the existing ui test pattern" because key-event construction varies by bubbletea version — the test assertions only rely on `msg.String()`.

**Type consistency:** `histRow`, `histLineMeta{row, arrowCell}`, `historyView`, `newHistoryView(ag,p,s,currentID,w,h)`, `resumeRequestedMsg{ConversationID,Title}`, `historyTurnsLoadedMsg{id,turns}`, `historyTailLines(turns,n,width,styles)`, `handleClick(x,yLocal)→(tea.Cmd,bool)` are used identically across tasks. `renderScrollable(lines,height,panelW,offset,styles)` matches Task 1's definition and Task 2's call.

**Scope:** One content page + its model wiring; single plan. No decomposition needed.
