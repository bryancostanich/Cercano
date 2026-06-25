# `/c` Turn Previews + Expand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/c` turns show multiple lines (assistant 3, others 1) and a clickable/keyboard-toggled `▸`/`▾` arrow to expand overflowing turns to their full content.

**Architecture:** `ContextTurn` gains an un-flattened `body` (capped 4 KB) + `truncated`. `contextView` renders N collapsed lines per turn with an arrow when there's more, holds per-turn `expanded` state, and toggles via a click (new content-page click routing + a row→turn hit-test) or keyboard (`tab` focus + `enter`).

**Tech Stack:** Go, gRPC/protobuf, Bubble Tea v2, the existing `internal/ui` + `internal/server` code.

## Global Constraints

- Two modules. Server: `cd source/server && go build ./... && go test ./... -count=1`. CLI: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- **Proto regen** (verified; `$GOPATH/bin` has the plugins):
  ```bash
  export PATH="$PATH:$(go env GOPATH)/bin"
  cd source/proto && protoc \
    --go_out=../server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=../server/pkg/proto --go-grpc_opt=paths=source_relative \
    agent.proto
  ```
  (Adding fields to the `ContextTurn` *message* does NOT add methods to `AgentClient`, so the mcp mock is unaffected — no mock change needed.)
- Assistant turns collapse to **3** body lines; all others to **1**. `body` capped at **4096** bytes on a rune boundary; set `truncated` when cut. Wrapping uses `ansi.Wrap` (as in `scrollback_tool.go`).
- Commit messages MUST NOT contain "Claude". No Co-Authored-By trailer.

Reference shapes (already defined):

```go
// internal/server/context_turns.go
const contextTurnPreviewMax = 120
func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn // builds preview today
func ctPreview(string) string  // flatten ; func ctTruncate(s string, max int) string // rune-safe truncate
// proto ContextTurn: role, kind, preview, est_tokens, id
// internal/ui/context_view.go: contextView{ width,height,styles,..., snapshot, scrollOffset, showingProposal, proposal, expanded? }
//   renderTurn(i, agentclient.ContextTurn) string ; turnsLines() []string ; renderScrollableContent(full, height)
//   regionHeights() (turnsH,paneH) ; markedForDelete(id) ; padToWidth ; dashboardPanelWidth(width)
// internal/ui/model.go: handleContextViewKey(cv, msg) ; contentTop() int ; MouseClickMsg handler ; contentScrollbarAt
// agentclient.ContextTurn{ ID, Role, Kind, Preview string; EstTokens int }
```

---

### Task 1: Server — `ContextTurn.body` + `truncated`

**Files:**
- Modify: `source/proto/agent.proto`; regenerate `source/server/pkg/proto/*.pb.go`
- Modify: `source/server/internal/server/context_turns.go`
- Modify: `source/server/pkg/agentclient/client.go`
- Test: `source/server/internal/server/context_turns_test.go`

**Interfaces:**
- Produces: `ContextTurn.body string` (field 6), `ContextTurn.truncated bool` (field 7); `agentclient.ContextTurn` gains `Body string`, `Truncated bool`.

- [ ] **Step 1: Add proto fields + regen**

In `agent.proto`, add to `message ContextTurn`:

```proto
  string body      = 6; // un-flattened content (newlines preserved), capped at 4 KB
  bool   truncated = 7; // true if body was capped
```

Run the regen command. Then `cd source/server && go build ./pkg/proto/` (clean; `ContextTurn.Body`/`Truncated` exist).

- [ ] **Step 2: Write the failing test**

Add to `context_turns_test.go`:

```go
func TestContextTurnView_BodyMultilineAndCap(t *testing.T) {
	tok := contextmeter.Default()
	// multi-line text turn → body keeps newlines; preview is flattened.
	multi := conversation.Turn{Role: "assistant", Content: "line one\nline two\nline three"}
	ct := contextTurnView(multi, tok)
	if ct.Body != "line one\nline two\nline three" {
		t.Errorf("body should preserve newlines, got %q", ct.Body)
	}
	if ct.Truncated {
		t.Error("short body should not be truncated")
	}
	if contains(ct.Preview, "\n") {
		t.Errorf("preview should stay single-line, got %q", ct.Preview)
	}
	// over-cap body → truncated, valid UTF-8.
	big := conversation.Turn{Role: "assistant", Content: strings.Repeat("x", 5000)}
	bct := contextTurnView(big, tok)
	if !bct.Truncated || len(bct.Body) > 4096 {
		t.Errorf("body should be capped+flagged: len=%d truncated=%v", len(bct.Body), bct.Truncated)
	}
	if !utf8.ValidString(bct.Body) {
		t.Error("capped body must be valid UTF-8")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
```

(Add imports `strings`, `unicode/utf8` to the test if missing.)

- [ ] **Step 3: Run to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestContextTurnView_BodyMultiline -count=1`
Expected: FAIL — `ct.Body`/`Truncated` are empty/false (not yet built).

- [ ] **Step 4: Build `body` in `contextTurnView`**

In `context_turns.go`, add the cap helper and set the new fields. The body is the un-flattened content (NOT run through `ctPreview`):

```go
const contextTurnBodyMax = 4096

// capBody truncates s to <= max bytes on a rune boundary, reporting whether it cut.
func capBody(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max], true
}
```

In `contextTurnView`, build a `body` alongside the existing `preview` (reuse the same block parse). For text turns `body = t.Content`; for `tool_use` `body = b.ToolName + " " + string(b.ToolInput)`; for `tool_result` `body = b.Content`; text block fallback like preview. Then:

```go
	bodyStr, truncated := capBody(body, contextTurnBodyMax)
	return &proto.ContextTurn{
		Id:        t.ID,
		Role:      t.Role,
		Kind:      kind,
		Preview:   ctTruncate(ctPreview(preview), contextTurnPreviewMax),
		Body:      bodyStr,
		Truncated: truncated,
		EstTokens: int32(tok.Count(tokenSrc)),
	}
```

(Track `body` next to `preview` in the parse loop — same `switch`, but assign the raw, un-flattened content to `body`. Add `import "unicode/utf8"` if missing.)

- [ ] **Step 5: Add the agentclient fields**

In `client.go`, add `Body string` and `Truncated bool` to the `ContextTurn` struct and map them in `GetConversationTurns`:

```go
		Body:      t.GetBody(),
		Truncated: t.GetTruncated(),
```

- [ ] **Step 6: Run tests + build**

Run: `cd source/server && go test ./internal/server/ -run TestContextTurnView -count=1 -v && go build ./...`
Expected: PASS; clean build.

- [ ] **Step 7: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/internal/server/context_turns.go source/server/internal/server/context_turns_test.go source/server/pkg/agentclient/client.go
git commit -m "feat(server): ContextTurn carries un-flattened body + truncated flag"
```

---

### Task 2: CLI — multi-line render + arrow + expand state

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go`
- Test: `source/clients/cli/internal/ui/context_view_expand_test.go`

**Interfaces:**
- Consumes: `agentclient.ContextTurn.Body/Truncated` (Task 1); `ansi.Wrap`.
- Produces: `contextView.expanded map[string]bool`, `focusedTurn int`; `toggleExpand(id)`; `turnExpandable(t) bool`; a `turnsLines()` that returns lines + a parallel `[]turnLineMeta{turnID string; header, arrowCell bool}`.

- [ ] **Step 1: Write the failing test**

Create `context_view_expand_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func expandTestView() *contextView {
	cv := &contextView{
		width: 70, height: 30,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1", expanded: map[string]bool{},
	}
	cv.snapshot.Usage = &agentclient.ContextUsage{ModelMax: 1000}
	return cv
}

func TestContextView_AssistantShowsThreeLinesAndArrow(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text",
			Body: "L1\nL2\nL3\nL4\nL5\nL6", Preview: "L1 L2 L3 L4 L5 L6"},
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "L1") || !strings.Contains(out, "L3") {
		t.Errorf("assistant should show first lines:\n%s", out)
	}
	if strings.Contains(out, "L4") {
		t.Error("collapsed assistant should NOT show line 4")
	}
	if !strings.Contains(out, "▸") {
		t.Error("overflowing turn should show a ▸ arrow")
	}
}

func TestContextView_ExpandShowsAllAndCaret(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5\nL6"},
	}
	cv.toggleExpand("a")
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "L4") || !strings.Contains(out, "L6") {
		t.Errorf("expanded should show all body lines:\n%s", out)
	}
	if !strings.Contains(out, "▾") {
		t.Error("expanded turn should show a ▾ caret")
	}
}

func TestContextView_OneLineTurnNoArrow(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "u", Role: "user", Kind: "text", Body: "short", Preview: "short"},
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if strings.Contains(out, "▸") || strings.Contains(out, "▾") {
		t.Errorf("non-overflowing turn must have no arrow:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestContextView_Assistant|TestContextView_Expand|TestContextView_OneLine' -count=1`
Expected: FAIL — `expanded`/`toggleExpand`/`turnsLinesOnly` undefined.

- [ ] **Step 3: Implement the render + state**

In `context_view.go`:
- Add struct fields: `expanded map[string]bool` and `focusedTurn int`. Initialize `expanded` in `newContextView` (`expanded: map[string]bool{}`, `focusedTurn: -1`).
- Add:

```go
func (c *contextView) toggleExpand(id string) {
	if c.expanded == nil {
		c.expanded = map[string]bool{}
	}
	c.expanded[id] = !c.expanded[id]
}

// turnBodyLines returns body wrapped to the content width (preview fallback).
func (c *contextView) turnBodyLines(t agentclient.ContextTurn) []string {
	body := t.Body
	if strings.TrimSpace(body) == "" {
		body = t.Preview
	}
	w := dashboardPanelWidth(c.width) - 4 // leave room for the arrow + token gutter
	if w < 8 {
		w = 8
	}
	return strings.Split(ansi.Wrap(body, w, ""), "\n")
}

func (c *contextView) collapsedCount(t agentclient.ContextTurn) int {
	if t.Role == "assistant" {
		return 3
	}
	return 1
}

func (c *contextView) turnExpandable(t agentclient.ContextTurn) bool {
	return len(c.turnBodyLines(t)) > c.collapsedCount(t) || t.Truncated
}
```

- Add a meta type + a `turnsLines()` that returns lines AND meta (and a thin `turnsLinesOnly()` for tests/the View path):

```go
type turnLineMeta struct {
	turnID    string
	header    bool // first line of the turn (carries the arrow)
	arrowCell bool // header line of an expandable turn (clickable)
}

func (c *contextView) turnsLines() ([]string, []turnLineMeta) {
	var lines []string
	var meta []turnLineMeta
	add := func(s string, m turnLineMeta) { lines = append(lines, s); meta = append(meta, m) }
	add(c.renderHeader(), turnLineMeta{})
	add("", turnLineMeta{})
	switch {
	case c.convID == "":
		add(c.styles.Muted.Render("no conversation yet"), turnLineMeta{})
	case c.snapshot.TurnsErr != nil:
		add(c.styles.Error.Render("turns unavailable: "+c.snapshot.TurnsErr.Error()), turnLineMeta{})
	case len(c.snapshot.Turns) == 0:
		add(c.styles.Muted.Render("context is empty"), turnLineMeta{})
	default:
		for i, t := range c.snapshot.Turns {
			c.appendTurn(&lines, &meta, i, t)
		}
	}
	return lines, meta
}

func (c *contextView) turnsLinesOnly() []string { l, _ := c.turnsLines(); return l }
```

- Replace `renderTurn` with `appendTurn(lines *[]string, meta *[]turnLineMeta, i int, t)` that pushes the turn's lines + meta:
  - Compute `bodyLines := c.turnBodyLines(t)`; `expandable := c.turnExpandable(t)`; `open := c.expanded[t.ID]`.
  - `shown := bodyLines`; if `!open` → `shown = bodyLines[:min(c.collapsedCount(t), len(bodyLines))]`.
  - Arrow gutter: `"▸ "` / `"▾ "` when expandable (open→▾), else `"  "`. Focused turn (`i == c.focusedTurn`) renders the badge with a highlight (e.g. `styles.Bright`).
  - Header line: `<arrow><badge> ≈tokens  <shown[0]>` (badge per role/kind as today; marked-for-delete keeps its `✗`/dim treatment). Push with `meta{turnID: t.ID, header: true, arrowCell: expandable}`.
  - Remaining `shown[1:]` lines: hang-indent (e.g. 4 spaces) under the header; push with `meta{turnID: t.ID}`.
  - If `open && t.Truncated`: push a dim `"    …(truncated)"` line.

- Update `View()` and `ScrollState()` to use `turnsLinesOnly()` (or `turnsLines()` discarding meta) wherever they currently call the old `turnsLines()`/`renderTurn`. Keep the split-region layout intact.

(`min` helper: use the existing `clampInt`-style helpers or add a local `func minInt(a,b int) int`. Check whether `minInt` already exists in the package before adding.)

- [ ] **Step 4: Run tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestContextView' -count=1 -v && go build ./...`
Expected: PASS (the 3 new tests + the existing /c tests); clean build.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/context_view_expand_test.go
git commit -m "feat(cli): /c renders multi-line turns with an expand arrow"
```

---

### Task 3: Interaction — click hit-test + keyboard toggle

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go` (`handleClick`)
- Modify: `source/clients/cli/internal/ui/model.go` (route body clicks; `tab`/`enter` in `handleContextViewKey`)
- Test: `source/clients/cli/internal/ui/context_view_expand_test.go`

**Interfaces:**
- Consumes: `turnsLines()` meta, `toggleExpand`, `turnExpandable`, `m.contentTop()`.
- Produces: `contextView.handleClick(x, yLocal int) bool`; `tab`/`shift+tab` focus + `enter` toggle in `handleContextViewKey`.

- [ ] **Step 1: Write the failing test**

Add to `context_view_expand_test.go`:

```go
func TestContextView_ClickArrowToggles(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5\nL6"},
	}
	_, meta := cv.turnsLines()
	// find the arrow row (header of the expandable turn)
	row := -1
	for i, m := range meta {
		if m.arrowCell {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no arrow cell found")
	}
	if !cv.handleClick(0, row) { // x=0 (arrow column), yLocal=row (offset 0)
		t.Fatal("click on arrow should be handled")
	}
	if !cv.expanded["a"] {
		t.Error("click should have expanded turn a")
	}
	// click off the arrow column → no toggle
	cv.handleClick(40, row)
	if !cv.expanded["a"] {
		t.Error("off-arrow click should not collapse")
	}
}

func TestContextView_TabFocusEnterToggles(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	cv.expanded = map[string]bool{}
	cv.focusedTurn = -1
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5"},
	}
	// tab focuses the first expandable turn
	m, _ = m.handleContextViewKey(cv, keyPress("tab"))
	if cv.focusedTurn != 0 {
		t.Fatalf("tab should focus turn 0, got %d", cv.focusedTurn)
	}
	// enter with empty input toggles the focused turn (not submit)
	m.input.SetValue("")
	m, _ = m.handleContextViewKey(cv, keyEnter())
	if !cv.expanded["a"] {
		t.Error("enter on a focused turn (empty input) should expand it")
	}
}
```

(`keyPress("tab")`/`keyEnter()` — reconcile against the existing key helpers in `context_view_route_test.go`; build the `tea.KeyPressMsg` the same way.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestContextView_ClickArrow|TestContextView_TabFocus' -count=1`
Expected: FAIL — `handleClick`/`tab`/`enter`-toggle undefined.

- [ ] **Step 3: Implement `handleClick`**

In `context_view.go`:

```go
// handleClick toggles a turn's expansion when the click lands on its arrow cell.
// yLocal is the click row relative to the page's top content row.
func (c *contextView) handleClick(x, yLocal int) bool {
	turnsH, _ := c.regionHeights()
	if yLocal < 0 || yLocal >= turnsH {
		return false // not in the turns region
	}
	_, meta := c.turnsLines()
	idx := c.scrollOffset + yLocal
	if idx < 0 || idx >= len(meta) {
		return false
	}
	m := meta[idx]
	if m.arrowCell && x <= 1 { // arrow is the leading marker column
		c.toggleExpand(m.turnID)
		return true
	}
	return false
}
```

- [ ] **Step 4: Route body clicks in `model.go`**

In the `MouseClickMsg` content-page branch (after the `contentScrollbarAt` check, before the final `return m, nil`), route a non-scrollbar left click to the contextView:

```go
		if cv, ok := m.content.(*contextView); ok && mouse.Button == tea.MouseLeft {
			if cv.handleClick(mouse.X, mouse.Y-m.contentTop()) {
				return m, nil
			}
		}
```

- [ ] **Step 5: Keyboard focus + toggle in `handleContextViewKey`**

Add cases (BEFORE the default prompt-bar fallthrough). `tab`/`shift+tab` move focus among expandable turns; `enter` toggles the focused turn ONLY when the input is empty (else the existing submit runs). Add a `focusNextExpandable(dir int)` method on `contextView` (below) that advances `focusedTurn` to the next/previous index `i` where `turnExpandable(snapshot.Turns[i])`, wrapping; then in `handleContextViewKey`:

```go
	case "tab":
		cv.focusNextExpandable(+1)
		return m, nil
	case "shift+tab":
		cv.focusNextExpandable(-1)
		return m, nil
```

And change the existing `enter` case so an empty input with a focused turn toggles instead of submitting:

```go
	case "enter":
		if strings.TrimSpace(m.input.Value()) == "" {
			if cv.focusedTurn >= 0 && cv.focusedTurn < len(cv.snapshot.Turns) {
				cv.toggleExpand(cv.snapshot.Turns[cv.focusedTurn].ID)
				return m, nil
			}
			return m, nil
		}
		m.input.SetValue("")
		return m, cv.pane.Submit(text) // existing submit path (text already trimmed above)
```

Implement `focusNextExpandable`:

```go
func (c *contextView) focusNextExpandable(dir int) {
	n := len(c.snapshot.Turns)
	if n == 0 {
		return
	}
	for step := 1; step <= n; step++ {
		i := (c.focusedTurn + dir*step + n*step) % n
		if i < 0 {
			i += n
		}
		if c.turnExpandable(c.snapshot.Turns[i]) {
			c.focusedTurn = i
			return
		}
	}
}
```

(Reconcile the exact `enter` block against the current `handleContextViewKey` — keep the trimmed-text submit behavior, only add the empty-input toggle branch.)

- [ ] **Step 6: Run tests + build the CLI**

Run: `cd source/clients/cli && go test ./internal/ui/ -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS (new interaction tests + all prior); clean CLI build; full CLI suite green.

- [ ] **Step 7: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/context_view_expand_test.go
git commit -m "feat(cli): /c expand turns via arrow click or tab+enter"
```

---

## Self-Review

**Spec coverage:**
- §1 data (body + truncated, capped) → Task 1.
- §2 rendering (assistant 3 / others 1, arrow on overflow, expanded shows all + truncated marker) → Task 2.
- §3 expand state (per-turn map, preserved across reload since keyed by id) → Task 2.
- §4 interaction (click hit-test + tab/enter keyboard) → Task 3.
- §5 edge (empty body→preview, no-overflow→no arrow, off-arrow click ignored) → Tasks 2-3.
- §6 testing → Tasks 1-3.

**Type consistency:** `ContextTurn.Body/Truncated` (proto + agentclient), `expanded`/`focusedTurn`/`toggleExpand`/`turnExpandable`/`turnsLines()→([]string,[]turnLineMeta)`/`handleClick(x,yLocal)`/`focusNextExpandable` are used identically across tasks.

**Placeholder scan:** every code step shows full code + commands. The "reconcile key helpers / the enter block against current code" notes are deliberate reconciliations with live code (the implementer matches the real `keyPress`/`keyEnter` helpers and the existing `enter` submit block), not unresolved requirements.
