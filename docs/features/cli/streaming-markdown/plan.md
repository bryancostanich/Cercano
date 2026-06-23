# Streaming Markdown Rendering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render the assistant's markdown prose nicely and progressively in the CLI viewport, using Glamour for prose and the existing responsive renderer for tables.

**Architecture:** A pure streaming block splitter divides the growing reply into completed blocks plus a live tail. `renderEntry` re-splits the buffer each frame: prose/code blocks go through a cached Glamour renderer, tables go through the existing `render.Table`, and the in-progress tail renders live (with any open code fence synthetically closed). Glamour bakes wrap width in at construction, so renderers are cached per width — resize is just a cache miss.

**Tech Stack:** Go, `charm.land/glamour/v2` v2.0.1, `charm.land/glamour/v2/ansi`, `charm.land/glamour/v2/styles`, `charm.land/lipgloss/v2`, Bubble Tea v2.

## Global Constraints

- Work happens in worktree `/Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render`, branch `md-streaming-render`. All commands run from `source/server/` unless noted.
- Charm v2 module paths only: `charm.land/glamour/v2`, `charm.land/glamour/v2/ansi`, `charm.land/glamour/v2/styles`, `charm.land/lipgloss/v2`. The module declares its path as `charm.land/glamour/v2` — `go get charm.land/glamour/v2@v2.0.1` (NOT the `github.com/charmbracelet` path).
- Glamour version pinned to `v2.0.1`.
- No new dependency beyond Glamour (and its transitive deps).
- Build: `go build ./... ` — Test: `go test ./... -count=1`.
- Commit messages: never include the word "Claude" anywhere (no Co-Authored-By trailer). Do not push.
- Scope is assistant prose only. Do not touch user/system entries or tool-call entry rendering.

---

### Task 1: Glamour render wrapper + dependency

**Files:**
- Modify: `source/server/go.mod`, `source/server/go.sum` (via `go get`)
- Create: `source/server/internal/cli/render/glamour.go`
- Test: `source/server/internal/cli/render/glamour_test.go`

**Interfaces:**
- Produces:
  - `render.NewMarkdown(style ansi.StyleConfig) *render.Markdown`
  - `(*render.Markdown).Render(md string, width int) string` — cached per (width, md); never errors (returns `md` on failure).
  - `(*render.Markdown).RenderLive(md string, width int) string` — uncached; for the changing tail.

- [ ] **Step 1: Add the dependency**

Run:
```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render/source/server
go get charm.land/glamour/v2@v2.0.1
```
Expected: `go.mod` gains `charm.land/glamour/v2 v2.0.1` (and `go.sum` updated). Exit 0.

- [ ] **Step 2: Write the failing test**

Create `source/server/internal/cli/render/glamour_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"charm.land/glamour/v2/styles"
)

func TestMarkdown_RendersBoldAndHeading(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	out := md.Render("# Title\n\nsome **bold** text\n", 80)
	if strings.Contains(out, "# Title") {
		t.Fatalf("heading markdown not transformed: %q", out)
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "bold") {
		t.Fatalf("expected rendered words present: %q", out)
	}
	// ANSI escape sequences indicate styling was applied.
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI styling in output: %q", out)
	}
}

func TestMarkdown_CacheReturnsConsistent(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	a := md.Render("**x**", 40)
	b := md.Render("**x**", 40)
	if a != b {
		t.Fatalf("cached render differs:\n%q\n%q", a, b)
	}
}

// Documents why we keep render.Table: Glamour cannot drop columns to fit a
// narrow width — it keeps all of them. render.Table drops/transposes.
func TestMarkdown_DoesNotDropTableColumns(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	table := "| AAAA | BBBB | CCCC | DDDD |\n| --- | --- | --- | --- |\n| 1 | 2 | 3 | 4 |\n"
	out := md.Render(table, 20)
	for _, h := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
		if !strings.Contains(out, h) {
			t.Fatalf("expected Glamour to keep column %q (it cannot drop): %q", h, out)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cli/render/ -run TestMarkdown -v`
Expected: FAIL — `undefined: NewMarkdown`.

- [ ] **Step 4: Write the implementation**

Create `source/server/internal/cli/render/glamour.go`:

```go
package render

import (
	"strings"
	"sync"

	glamour "charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
)

// Markdown renders markdown to themed ANSI. Glamour bakes the wrap width in at
// construction, so one TermRenderer is built and cached per width; resize is a
// cache miss. Render output is cached per (width, source) since committed blocks
// are stable across frames. The live tail uses RenderLive (uncached) because its
// source changes every frame.
type Markdown struct {
	style ansi.StyleConfig

	mu        sync.Mutex
	renderers map[int]*glamour.TermRenderer
	out       map[string]string
}

// NewMarkdown builds a renderer factory for the given style.
func NewMarkdown(style ansi.StyleConfig) *Markdown {
	return &Markdown{
		style:     style,
		renderers: map[int]*glamour.TermRenderer{},
		out:       map[string]string{},
	}
}

func (m *Markdown) renderer(width int) *glamour.TermRenderer {
	if r, ok := m.renderers[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(m.style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	m.renderers[width] = r
	return r
}

// render does the actual Glamour call. Returns md unchanged on any error so
// output is never lost. Trims the trailing newlines Glamour appends.
func (m *Markdown) render(md string, width int) string {
	if width < 1 {
		width = 1
	}
	r := m.renderer(width)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}

// Render renders md at the given wrap width, caching the result by (width, md).
func (m *Markdown) Render(md string, width int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := keyFor(width, md)
	if v, ok := m.out[key]; ok {
		return v
	}
	v := m.render(md, width)
	m.out[key] = v
	return v
}

// RenderLive renders md without caching — for the in-progress tail block whose
// source changes every frame.
func (m *Markdown) RenderLive(md string, width int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.render(md, width)
}

func keyFor(width int, md string) string {
	var b strings.Builder
	b.WriteString(itoa(width))
	b.WriteByte(0)
	b.WriteString(md)
	return b.String()
}
```

(`itoa` already exists in `markdown.go`, same package.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/render/ -run TestMarkdown -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render
git add source/server/go.mod source/server/go.sum source/server/internal/cli/render/glamour.go source/server/internal/cli/render/glamour_test.go
git commit -m "feat(render): glamour markdown wrapper with per-width renderer cache"
```

---

### Task 2: Cracker-themed Glamour style

**Files:**
- Create: `source/server/internal/cli/theme/markdown.go`
- Test: `source/server/internal/cli/theme/markdown_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces: `theme.CrackerMarkdownStyle() ansi.StyleConfig`

**Note on colors:** Hex values mirror `Cracker()` in `palette.go` (amber `#EA8212`, bright `#FFB84D`, lime `#BDF000`, cyan `#00C8E8`, muted `#888888`). Keep them in sync if the palette changes.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/cli/theme/markdown_test.go`:

```go
package theme

import "testing"

func TestCrackerMarkdownStyle_KeyColorsSet(t *testing.T) {
	sc := CrackerMarkdownStyle()
	if sc.Heading.Color == nil || *sc.Heading.Color != "#FFB84D" {
		t.Fatalf("heading color = %v, want #FFB84D", sc.Heading.Color)
	}
	if sc.Code.Color == nil || *sc.Code.Color != "#00C8E8" {
		t.Fatalf("inline code color = %v, want #00C8E8", sc.Code.Color)
	}
	if sc.Link.Underline == nil || !*sc.Link.Underline {
		t.Fatalf("link should be underlined")
	}
	if sc.Document.Margin == nil || *sc.Document.Margin != 0 {
		t.Fatalf("document margin = %v, want 0 (we add our own indent)", sc.Document.Margin)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/theme/ -run TestCrackerMarkdownStyle -v`
Expected: FAIL — `undefined: CrackerMarkdownStyle`.

- [ ] **Step 3: Write the implementation**

Create `source/server/internal/cli/theme/markdown.go`:

```go
package theme

import (
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// Cracker palette hex — keep in sync with Cracker() in palette.go.
const (
	mdAmber  = "#EA8212"
	mdBright = "#FFB84D"
	mdLime   = "#BDF000"
	mdCyan   = "#00C8E8"
	mdMuted  = "#888888"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func uintp(u uint) *uint    { return &u }

// CrackerMarkdownStyle returns a Glamour StyleConfig themed to the cracker
// palette. It starts from the bundled Dracula dark style (sane margins, code
// block layout, chroma theme) and recolors the leaf elements to amber/lime/cyan.
// Document margin is zeroed because renderEntry adds its own left indent.
func CrackerMarkdownStyle() ansi.StyleConfig {
	sc := styles.DraculaStyleConfig // struct copy; we replace leaf pointers, never mutate pointees

	sc.Document.Margin = uintp(0)
	sc.Document.Color = strp(mdAmber)

	sc.Heading.Color = strp(mdBright)
	sc.Heading.Bold = boolp(true)
	sc.H1.Color = strp(mdBright)
	sc.H1.Bold = boolp(true)
	sc.H2.Color = strp(mdBright)
	sc.H3.Color = strp(mdBright)

	sc.Strong.Color = strp(mdBright)
	sc.Strong.Bold = boolp(true)
	sc.Emph.Italic = boolp(true)

	sc.Code.Color = strp(mdCyan)

	sc.Item.Color = strp(mdLime)
	sc.Enumeration.Color = strp(mdLime)

	sc.Link.Color = strp(mdCyan)
	sc.Link.Underline = boolp(true)
	sc.LinkText.Color = strp(mdCyan)

	sc.HorizontalRule.Color = strp(mdMuted)

	return sc
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/theme/ -run TestCrackerMarkdownStyle -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render
git add source/server/internal/cli/theme/markdown.go source/server/internal/cli/theme/markdown_test.go
git commit -m "feat(theme): cracker-themed glamour markdown style"
```

---

### Task 3: Streaming block splitter

**Files:**
- Create: `source/server/internal/cli/render/mdstream.go`
- Test: `source/server/internal/cli/render/mdstream_test.go`

**Interfaces:**
- Consumes: `matchTable`, `markdownTable.toTable`, `Table` (existing in `markdown.go`/`table.go`).
- Produces:
  - `render.MdKind` with `render.MdProse`, `render.MdTable`
  - `render.MdBlock{ Kind MdKind; Raw string; Table *Table }`
  - `render.SplitBlocks(text string) (blocks []MdBlock, tail string)`

**Splitter rules:**
- A blank line (outside a code fence) ends the current prose block.
- A code fence (line whose trimmed text starts with ` ``` `) toggles fence state; blank lines inside a fence do NOT end the block.
- A pipe-table (detected by `matchTable`) is its own `MdTable` block, but only when it is *terminated* — another line follows it, or the text ended with a newline. Otherwise its lines fall through into the tail as raw prose.
- A single trailing newline at end of `text` is not a separator (it's the cursor position); only a genuine blank line separates.
- `tail` = the still-open trailing block (not yet terminated by a blank line).

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/cli/render/mdstream_test.go`:

```go
package render

import "testing"

func TestSplit_ParagraphsSplitOnBlankLine(t *testing.T) {
	blocks, tail := SplitBlocks("para one\n\npara two")
	if len(blocks) != 1 || blocks[0].Kind != MdProse || blocks[0].Raw != "para one" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if tail != "para two" {
		t.Fatalf("tail = %q, want %q", tail, "para two")
	}
}

func TestSplit_TrailingNewlineIsNotASeparator(t *testing.T) {
	blocks, tail := SplitBlocks("just one line\n")
	if len(blocks) != 0 {
		t.Fatalf("expected no committed blocks, got %#v", blocks)
	}
	if tail != "just one line" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_CodeFenceWithBlankLineStaysOneBlock(t *testing.T) {
	in := "```go\nfunc main() {\n\n}\n```\n\nafter"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 1 || blocks[0].Kind != MdProse {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Raw != "```go\nfunc main() {\n\n}\n```" {
		t.Fatalf("fence block raw = %q", blocks[0].Raw)
	}
	if tail != "after" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_OpenFenceIsTail(t *testing.T) {
	blocks, tail := SplitBlocks("intro\n\n```go\nfunc main() {")
	if len(blocks) != 1 || blocks[0].Raw != "intro" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if tail != "```go\nfunc main() {" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_TerminatedTableIsTableBlock(t *testing.T) {
	in := "| A | B |\n| --- | --- |\n| 1 | 2 |\n\nnext"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 1 || blocks[0].Kind != MdTable || blocks[0].Table == nil {
		t.Fatalf("blocks = %#v", blocks)
	}
	if len(blocks[0].Table.Cols) != 2 || len(blocks[0].Table.Rows) != 1 {
		t.Fatalf("table parsed wrong: %#v", blocks[0].Table)
	}
	if tail != "next" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_UnterminatedTableFallsToTail(t *testing.T) {
	// No trailing newline, nothing after — table is still streaming.
	in := "| A | B |\n| --- | --- |\n| 1 | 2 |"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 0 {
		t.Fatalf("expected table to stay in tail, got blocks %#v", blocks)
	}
	if tail != in {
		t.Fatalf("tail = %q", tail)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/render/ -run TestSplit -v`
Expected: FAIL — `undefined: SplitBlocks` / `undefined: MdProse`.

- [ ] **Step 3: Write the implementation**

Create `source/server/internal/cli/render/mdstream.go`:

```go
package render

import "strings"

// MdKind tags a streamed markdown block.
type MdKind int

const (
	MdProse MdKind = iota // prose, headings, lists, code fences — rendered via Glamour
	MdTable               // a pipe-table — rendered via the responsive Table renderer
)

// MdBlock is one block of a streamed assistant reply.
type MdBlock struct {
	Kind  MdKind
	Raw   string // markdown source (MdProse)
	Table *Table // set when Kind == MdTable
}

// SplitBlocks splits markdown into completed blocks plus a trailing in-progress
// tail. Completed blocks are stable across frames (cacheable); the tail changes
// as tokens arrive and is rendered live. See the rules in the plan/spec.
func SplitBlocks(text string) (blocks []MdBlock, tail string) {
	lines := strings.Split(text, "\n")
	hadTrailingNL := strings.HasSuffix(text, "\n")
	// A final newline produces a trailing "" element — that's the cursor
	// position, not a blank-line separator. Drop it.
	if hadTrailingNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var cur []string
	inFence := false

	flushProse := func() {
		if len(cur) > 0 {
			blocks = append(blocks, MdBlock{Kind: MdProse, Raw: strings.Join(cur, "\n")})
			cur = nil
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]

		if !inFence {
			if mt, consumed := matchTable(lines, i); consumed > 0 && tableTerminated(lines, i, consumed, hadTrailingNL) {
				flushProse()
				tbl := mt.toTable()
				blocks = append(blocks, MdBlock{
					Kind:  MdTable,
					Raw:   strings.Join(lines[i:i+consumed], "\n"),
					Table: &tbl,
				})
				i += consumed
				continue
			}
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			cur = append(cur, line)
			i++
			continue
		}
		if trimmed == "" && !inFence {
			flushProse()
			i++
			continue
		}
		cur = append(cur, line)
		i++
	}

	tail = strings.Join(cur, "\n")
	return blocks, tail
}

// tableTerminated reports whether a table starting at lines[i] spanning
// `consumed` lines is finished streaming: either a line follows it, or the
// buffer ended with a newline (so the last row is complete).
func tableTerminated(lines []string, i, consumed int, hadTrailingNL bool) bool {
	if i+consumed < len(lines) {
		return true
	}
	return hadTrailingNL
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/render/ -run TestSplit -v`
Expected: PASS (all six).

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render
git add source/server/internal/cli/render/mdstream.go source/server/internal/cli/render/mdstream_test.go
git commit -m "feat(render): streaming markdown block splitter"
```

---

### Task 4: Wire assistant rendering to streaming markdown

**Files:**
- Modify: `source/server/internal/cli/ui/model.go`
  - `Entry` struct (remove `Tables` field, ~lines 48-52)
  - `Model` struct (add `md` field, near `styles` ~line 65)
  - `New()` return literal (~line 192)
  - `applyStreamMsg` `TypeDone` (~lines 790-803)
  - `renderEntry` `RoleAssistant` case (~lines 1026-1055) + new helpers
- Modify: `source/server/internal/cli/render/markdown.go` (delete `InterceptMarkdownTables`)
- Modify: `source/server/internal/cli/render/markdown_test.go` (delete its `InterceptMarkdownTables` tests)
- Test: `source/server/internal/cli/ui/scrollback_markdown_test.go` (new)

**Interfaces:**
- Consumes: `render.NewMarkdown`, `(*render.Markdown).Render`, `(*render.Markdown).RenderLive`, `render.SplitBlocks`, `render.MdBlock`, `render.MdTable`, `theme.CrackerMarkdownStyle`.
- Produces: behavior change only; no new exported API.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/cli/ui/scrollback_markdown_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/server/internal/cli/render"
	"cercano/source/server/internal/cli/theme"
)

// Renders an assistant entry the way renderEntry does for committed blocks,
// proving prose is Glamour-formatted and tables go through render.Table.
func TestAssistantMarkdown_FormatsProseAndTable(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{
		Role:    RoleAssistant,
		Content: "# Header\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\ntrailing prose\n",
	}
	out := m.renderAssistantMarkdown(e, 60)

	if strings.Contains(out, "# Header") {
		t.Fatalf("heading not formatted: %q", out)
	}
	// Table routed through render.Table → box-drawing border present.
	if !strings.Contains(out, "│") {
		t.Fatalf("expected table border from render.Table: %q", out)
	}
	if !strings.Contains(out, "trailing prose") {
		t.Fatalf("trailing prose missing: %q", out)
	}
}

func TestAssistantMarkdown_OpenFenceTailRenders(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n```go\nx := 1"}
	out := m.renderAssistantMarkdown(e, 60)
	if !strings.Contains(out, "x := 1") {
		t.Fatalf("open-fence tail code missing: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ui/ -run TestAssistantMarkdown -v`
Expected: FAIL — `m.md undefined` / `m.renderAssistantMarkdown undefined`.

- [ ] **Step 3: Add the `md` field to the `Model` struct**

In `model.go`, in the `Model` struct after the `styles` field (~line 65), add:

```go
	// md renders assistant markdown prose. Holds per-width Glamour renderers
	// and a render cache for committed blocks.
	md *render.Markdown
```

- [ ] **Step 4: Initialize `md` in `New()`**

In the `return Model{...}` literal (~line 192), add after `styles: s,`:

```go
		md:                 render.NewMarkdown(theme.CrackerMarkdownStyle()),
```

(`render` and `theme` are already imported in `model.go`.)

- [ ] **Step 5: Remove the `Tables` field from `Entry`**

In the `Entry` struct, delete the `Tables` field and its comment block (the lines from `// Tables are markdown tables extracted...` through `Tables []render.Table`):

```go
	// Tables are markdown tables extracted from Content at stream-done.
	// Content carries `{{TABLE_N}}` sentinels; renderEntry substitutes them
	// with freshly-rendered Table strings at the CURRENT viewport width
	// every frame, so a terminal resize automatically re-fits each table.
	Tables []render.Table
```

- [ ] **Step 6: Simplify the `TypeDone` handler**

In `applyStreamMsg`, replace the `TypeDone` entry block (the `if e := m.lastAssistantEntry(); e != nil { ... }` that currently calls `InterceptMarkdownTables`) with:

```go
	case agentclient.TypeDone:
		if e := m.lastAssistantEntry(); e != nil {
			// If we never received any tokens, fall back to the full final response.
			if e.Content == "" {
				e.Content = sm.Final
			}
			e.Streaming = false
		}
```

(Leave everything below it — the `sm.Notice` handling, token accounting — unchanged.)

- [ ] **Step 7: Replace the `RoleAssistant` case in `renderEntry`**

Replace the entire `case RoleAssistant:` block (from `case RoleAssistant:` through its closing `return indentBlock(pad, wrapped)` just before `case RoleSystem:`) with:

```go
	case RoleAssistant:
		// Pre-stream placeholder: no content yet, show the animated status.
		if e.Streaming && e.Content == "" {
			status := e.Status
			if status == "" {
				status = "thinking…"
			}
			content := animateSpinnerGlyph() + " " + animateLimeSweep(status)
			return indentBlock(pad, content)
		}
		rendered := m.renderAssistantMarkdown(e, textW)
		if e.Streaming {
			rendered += m.styles.Accent.Render(" ⟳")
		}
		return indentBlock(pad, rendered)
```

- [ ] **Step 8: Add the markdown render helpers**

Immediately after the `renderEntry` function, add:

```go
// renderAssistantMarkdown splits the assistant buffer into completed blocks plus
// a live tail, rendering prose via Glamour and tables via the responsive Table
// renderer. Committed blocks are cached; the tail renders live (with any open
// code fence synthetically closed) so streaming code highlights as it grows.
func (m *Model) renderAssistantMarkdown(e *Entry, textW int) string {
	blocks, tail := render.SplitBlocks(e.Content)
	var parts []string
	for _, b := range blocks {
		parts = append(parts, m.renderMdBlock(b, textW))
	}
	if strings.TrimSpace(tail) != "" {
		parts = append(parts, m.md.RenderLive(closeOpenFence(tail), textW))
	}
	return strings.Join(parts, "\n")
}

func (m *Model) renderMdBlock(b render.MdBlock, textW int) string {
	if b.Kind == render.MdTable && b.Table != nil {
		return b.Table.Render(textW, m.styles)
	}
	return m.md.Render(b.Raw, textW)
}

// closeOpenFence appends a closing code fence when the tail has an odd number of
// fence lines, so Glamour renders an in-progress code block instead of leaking
// the rest as raw text.
func closeOpenFence(s string) string {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			n++
		}
	}
	if n%2 == 1 {
		return s + "\n```"
	}
	return s
}
```

- [ ] **Step 9: Delete the now-dead `InterceptMarkdownTables`**

In `source/server/internal/cli/render/markdown.go`, delete the entire `InterceptMarkdownTables` function (from its doc comment `// InterceptMarkdownTables scans the text...` through its closing `}`). Keep `markdownTable`, `matchTable`, `looksLikePipeRow`, `looksLikeSeparator`, `splitPipeRow`, `toTable`, and `itoa` — they are still used.

- [ ] **Step 10: Delete the `InterceptMarkdownTables` tests**

In `source/server/internal/cli/render/markdown_test.go`, delete the five tests that call `InterceptMarkdownTables`: `TestIntercept_NoTableLeavesTextAlone`, `TestIntercept_DetectsSimpleTable`, `TestIntercept_RejectsBareTextWithPipes`, `TestIntercept_RequiresSeparatorRow`, `TestIntercept_TwoTables`. If the file is left with no tests and unused imports, remove the dead imports too (table detection is now covered by `mdstream_test.go`). If the file becomes empty, delete it.

- [ ] **Step 11: Build and run the full test suite**

Run:
```bash
go build ./...
go test ./internal/cli/... -count=1
```
Expected: build clean; all tests pass, including `TestAssistantMarkdown_*`.

- [ ] **Step 12: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render
git add -A
git commit -m "feat(cli): progressive markdown rendering in the assistant viewport"
```

---

### Task 5: Manual smoke verification

**Files:** none (verification only).

- [ ] **Step 1: Full build + test**

Run:
```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano-md-render/source/server
go build -o bin/cercano ./cmd/cercano/
go test ./... -count=1
```
Expected: binary builds; full suite green.

- [ ] **Step 2: Headless smoke (text unaffected)**

Run:
```bash
bin/cercano run "reply with a markdown heading, a bold word, a bullet list, a fenced go code block, and a 3-column table"
```
Expected: exit 0; stdout carries the reply text (headless does not ANSI-format — this confirms no regression in the headless path).

- [ ] **Step 3: Interactive visual check**

STOP and hand to the user: launch `bin/cercano`, send the same prompt, and confirm in the live TUI that headings are amber/bold, bold/italic/inline-code render, the code block is syntax-highlighted, list bullets are lime, and the table renders through the responsive renderer (borders, narrow-terminal column dropping). Per the project's interactive-routing/review preference, review the rendered output together before considering the feature done.

---

## Self-Review

**Spec coverage:**
- Dependency & theme → Tasks 1, 2.
- Streaming block splitter → Task 3.
- Entry model change + applyStreamMsg + renderEntry → Task 4.
- Replaces `InterceptMarkdownTables` → Task 4 (steps 9-10).
- Glamour width handling (build per width, cache) → Task 1 (`Markdown.renderer`).
- Tables kept via responsive renderer → Task 3 (`MdTable`) + Task 4 (`renderMdBlock`).
- Resize refits → width-keyed renderer + output cache (Task 1); a new width is a cache miss, no explicit invalidation needed.
- Tail live render with synth-close → Task 4 (`closeOpenFence` + `RenderLive`).
- Glamour-error fallback to raw → Task 1 (`render` returns `md` on error).
- Testing (splitter, render golden-ish, resize/overflow) → Tasks 1, 3, 4. The overflow probe is reframed as "Glamour cannot drop columns" (stable assertion).

**Placeholder scan:** none — every code step has complete code; every run step has a command and expected result.

**Type consistency:** `Markdown` / `NewMarkdown` / `Render` / `RenderLive`; `MdBlock` / `MdKind` / `MdProse` / `MdTable`; `SplitBlocks` returns `([]MdBlock, string)`; `CrackerMarkdownStyle() ansi.StyleConfig`. The `md *render.Markdown` field name matches in struct, `New()`, and helpers. `Entry.Tables` removed and no later task references it.

**Note for executor:** Glamour output styling (exact colors, code-block theme) is expected to be refined interactively with the user after Task 5 — the style in Task 2 is a sound starting point, not a final palette.
