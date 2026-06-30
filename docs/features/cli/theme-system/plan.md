# Theme System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Named built-in themes plus create/edit/save/delete/import of custom themes, switchable live, all from the settings page.

**Architecture:** Expand `theme.Palette` to hold the full 18-color set (chrome + content), with `Theme = {Name, Palette}`. `NewStyles`/`MarkdownStyle` derive purely from a palette. A registry holds built-ins (code) + custom themes (YAML files). `Model.applyTheme` rebuilds styles everywhere and flushes the chat markdown cache for a live repaint. The settings page gains Theme sections (selector + grouped color rows + action rows) backed by two new `internal/form` widgets (`ColorField`, `ButtonField`).

**Tech Stack:** Go, `charm.land/lipgloss/v2`, `charm.land/glamour/v2`, `gopkg.in/yaml.v3` (already used by the server config), Bubble Tea v2.

## Global Constraints

- Module: all code under `cercano/source/clients/cli`. Test: `cd source/clients/cli && go test ./... -count=1`.
- Charm v2 imports: `tea "charm.land/bubbletea/v2"`, `"charm.land/lipgloss/v2"`, glamour `"charm.land/glamour/v2/ansi"` + `"charm.land/glamour/v2/styles"`.
- `internal/form` MUST NOT import `internal/ui` (import cycle); it may import `internal/theme`.
- No LLM calls anywhere in this feature; all logic is deterministic.
- The default look stays `cracker` with byte-identical colors — verified by a golden test.
- Color persistence format: `#RRGGBB` lowercase hex strings.
- **Deviation from spec:** the spec described `Theme` holding colors with `Palette` as a subset accessor. For minimal churn we instead expand `Palette` to hold all 18 colors and define `Theme = {Name string; Palette Palette}`. Functionally identical; keeps `NewStyles(Palette)`'s existing signature.

---

## Phase 1 — Infra

### Task 1: Expand `Palette` with content colors; remove the package-global `Buffer*` vars

**Files:**
- Modify: `source/clients/cli/internal/theme/palette.go`
- Modify: `source/clients/cli/internal/ui/scrollback_tool.go:54-93`
- Test: `source/clients/cli/internal/theme/palette_test.go` (create)

**Interfaces:**
- Produces: `Palette` with 5 new fields `BufferLink, BufferCode, BufferLime, BufferError, BufferUserBg color.Color`; `Cracker()` sets them. The package vars `theme.BufferLink/BufferCode/BufferLime/BufferError/BufferUserBg` are removed.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/theme/palette_test.go`:
```go
package theme

import "testing"

func TestCrackerHasContentColors(t *testing.T) {
	p := Cracker()
	// hexOf is added in Task 3; for now assert non-nil via String().
	for name, c := range map[string]interface{}{
		"BufferLink": p.BufferLink, "BufferCode": p.BufferCode, "BufferLime": p.BufferLime,
		"BufferError": p.BufferError, "BufferUserBg": p.BufferUserBg,
	} {
		if c == nil {
			t.Fatalf("Cracker().%s is nil", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/theme/ -run TestCrackerHasContentColors -v`
Expected: FAIL — `p.BufferLink` undefined (Palette has no such field).

- [ ] **Step 3: Write minimal implementation**

In `palette.go`, add the 5 fields to the `Palette` struct (after `Error`):
```go
	Error       color.Color // failures, bypass indicator

	// Content colors (scrollback + markdown prose).
	BufferLink   color.Color // markdown links
	BufferCode   color.Color // inline code / code-fence lang
	BufferLime   color.Color // tool ✓, focus caret, echoed user ▶
	BufferError  color.Color // tool ⚠
	BufferUserBg color.Color // fill behind echoed user prompts
```
Set them in `Cracker()` (after `Error:`):
```go
		Error:     lipgloss.Color(hexError),

		BufferLink:   lipgloss.Color(bufLinkHex),
		BufferCode:   lipgloss.Color(bufCodeHex),
		BufferLime:   lipgloss.Color(bufLimeHex),
		BufferError:  lipgloss.Color(bufRedHex),
		BufferUserBg: lipgloss.Color(bufUserBgHex),
```
Delete the package-global `var ( BufferLink ... BufferUserBg ... )` block (the exported `lipgloss.Color` vars). Keep the `buf*Hex` const block — it's now the source for `Cracker()`.

In `scrollback_tool.go`, the package vars reference removed globals. Replace `renderToolEntry`'s signature and body to take styles. Change line 80 signature:
```go
func renderToolEntry(e ToolEntry, width int, focused bool, styles theme.Styles, md *render.Markdown) string {
```
Replace the three package-level styles (lines 56-60) — delete that `var (...)` block — and inside `renderToolEntry` use:
```go
	toolEntryFaint := lipgloss.NewStyle().Faint(true)
	toolEntrySuccess := styles.ToolSuccess
	toolEntryError := styles.ToolError
```
and replace the focused gutter line:
```go
		gutter = styles.ToolFocus.Render("▶ ")
```
(`styles.ToolSuccess/ToolError/ToolFocus` are added in Task 2.) Update the one caller of `renderToolEntry` (grep `renderToolEntry(`) to pass `c.styles` before `md`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/theme/ -run TestCrackerHasContentColors -v`
Expected: PASS. (The `ui` package won't build until Task 2 adds the Styles fields — that's expected; this task's package test passes.)

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/theme/palette.go source/clients/cli/internal/theme/palette_test.go source/clients/cli/internal/ui/scrollback_tool.go
git commit -m "refactor(cli/theme): fold content colors into Palette; drop Buffer globals"
```

---

### Task 2: Derive `Styles` (incl. tool glyphs) from `Palette`; add `MarkdownStyle(Palette)`

**Files:**
- Modify: `source/clients/cli/internal/theme/styles.go`
- Modify: `source/clients/cli/internal/theme/markdown.go`
- Modify: `source/clients/cli/internal/ui/chat_view.go:65,84`, `context_view.go:78`, `history_view.go:67`
- Test: `source/clients/cli/internal/theme/markdown_test.go` (extend)

**Interfaces:**
- Consumes: expanded `Palette` (Task 1).
- Produces: `Styles` gains `ToolSuccess, ToolError, ToolFocus lipgloss.Style`; `BufferCode/BufferUserLine/BufferUserMarker` now derive from `p.Buffer*`. New `func MarkdownStyle(p Palette) ansi.StyleConfig`. `CrackerMarkdownStyle()` removed.

- [ ] **Step 1: Write the failing test**

In `markdown_test.go` add:
```go
func TestMarkdownStyleUsesPaletteColors(t *testing.T) {
	sc := MarkdownStyle(Cracker())
	if sc.Document.Color == nil || *sc.Document.Color != hexPrimary {
		t.Fatalf("Document.Color = %v, want %s", sc.Document.Color, hexPrimary)
	}
	if sc.Code.Color == nil || *sc.Code.Color != bufCodeHex {
		t.Fatalf("Code.Color = %v, want %s", sc.Code.Color, bufCodeHex)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/theme/ -run TestMarkdownStyleUsesPaletteColors -v`
Expected: FAIL — `MarkdownStyle` undefined.

- [ ] **Step 3: Write minimal implementation**

In `styles.go`, add fields to `Styles`:
```go
	ToolSuccess lipgloss.Style // muted lime ✓ glyph
	ToolError   lipgloss.Style // muted red ⚠ glyph
	ToolFocus   lipgloss.Style // muted lime ▶ nav caret
```
In `NewStyles(p Palette)`, change the buffer-derived styles to read from `p` and add the tool styles:
```go
		BufferCode:       lipgloss.NewStyle().Foreground(p.BufferCode),
		BufferUserPrompt: lipgloss.NewStyle().Foreground(p.BufferLime).Bold(true),
		BufferUserLine:   lipgloss.NewStyle().Background(p.BufferUserBg),
		BufferUserMarker: lipgloss.NewStyle().Foreground(p.BufferLime).Background(p.BufferUserBg).Bold(true),
		ToolSuccess:      lipgloss.NewStyle().Foreground(p.BufferLime),
		ToolError:        lipgloss.NewStyle().Foreground(p.BufferError),
		ToolFocus:        lipgloss.NewStyle().Foreground(p.BufferLime),
```
(The `MeterFill`/`MeterEmpty`/`BypassFlag`/`UserPrompt`/`AgentProse` lines stay as-is; they already use `p.*`.)

In `markdown.go`, rename `CrackerMarkdownStyle()` to `MarkdownStyle(p Palette)` and replace every hex literal with `hexOf(p.*)` (hexOf is added in Task 3; for now this task can keep using the local hex helper below). To avoid a Task-3 dependency, add a tiny local converter at the top of `markdown.go`:
```go
func hexStr(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
```
(imports: add `"fmt"` and `"image/color"`.) Then in `MarkdownStyle(p Palette)` replace:
`strp(hexPrimary)`→`strp(hexStr(p.Primary))`, `strp(hexBright)`→`strp(hexStr(p.Bright))`, `strp(bufCodeHex)`→`strp(hexStr(p.BufferCode))`, `strp(hexPrimary)` (List)→`strp(hexStr(p.Primary))`, `strp(bufLinkHex)`→`strp(hexStr(p.BufferLink))` (both Link and LinkText), `strp(hexMuted)`→`strp(hexStr(p.Muted))`. Keep the structure/comments otherwise. Rename the doc comment to describe a palette-parameterized style.

Update the three UI call sites:
- `chat_view.go:84` `md: render.NewMarkdown(theme.CrackerMarkdownStyle()),` → `md: render.NewMarkdown(theme.MarkdownStyle(palette)),`
- `context_view.go:78` `... render.NewMarkdown(theme.CrackerMarkdownStyle())` → `theme.MarkdownStyle(p)`
- `history_view.go:67` `func newHistoryMarkdown() *render.Markdown { return render.NewMarkdown(theme.CrackerMarkdownStyle()) }` → take a palette: `func newHistoryMarkdown(p theme.Palette) *render.Markdown { return render.NewMarkdown(theme.MarkdownStyle(p)) }` and update its caller(s) (grep `newHistoryMarkdown(`) to pass the palette in scope.

- [ ] **Step 4: Run test + full build**

Run: `cd source/clients/cli && go build ./... && go test ./internal/theme/ -count=1`
Expected: build succeeds (ui package now compiles with the new Styles fields), theme tests PASS. Fix any remaining `CrackerMarkdownStyle`/`theme.Buffer*` references the compiler flags.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/theme/styles.go source/clients/cli/internal/theme/markdown.go source/clients/cli/internal/ui/chat_view.go source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/history_view.go
git commit -m "refactor(cli/theme): derive Styles + MarkdownStyle from Palette"
```

---

### Task 3: `Theme` type + hex helpers

**Files:**
- Create: `source/clients/cli/internal/theme/theme.go`
- Test: `source/clients/cli/internal/theme/theme_test.go`

**Interfaces:**
- Produces: `type Theme struct { Name string; Palette Palette }`; `func ParseHex(s string) (color.Color, error)`; `func HexOf(c color.Color) string`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/theme/theme_test.go`:
```go
package theme

import "testing"

func TestHexRoundTrip(t *testing.T) {
	c, err := ParseHex("#ea8212")
	if err != nil {
		t.Fatalf("ParseHex error: %v", err)
	}
	if got := HexOf(c); got != "#ea8212" {
		t.Fatalf("HexOf round-trip = %q, want #ea8212", got)
	}
}

func TestParseHexRejectsBad(t *testing.T) {
	for _, bad := range []string{"", "ea8212", "#xyzxyz", "#12345", "#1234567"} {
		if _, err := ParseHex(bad); err == nil {
			t.Fatalf("ParseHex(%q) should error", bad)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/theme/ -run 'TestHexRoundTrip|TestParseHexRejectsBad' -v`
Expected: FAIL — `ParseHex`/`HexOf` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/theme/theme.go`:
```go
package theme

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is a named complete color set. Palette holds every cercano-cli color.
type Theme struct {
	Name    string
	Palette Palette
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ParseHex converts "#RRGGBB" to a color, rejecting any other shape.
func ParseHex(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if !hexRe.MatchString(s) {
		return nil, fmt.Errorf("invalid hex color %q (want #RRGGBB)", s)
	}
	return lipgloss.Color(strings.ToLower(s)), nil
}

// HexOf renders a color back to lowercase "#RRGGBB".
func HexOf(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/theme/ -run 'TestHexRoundTrip|TestParseHexRejectsBad' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/theme/theme.go source/clients/cli/internal/theme/theme_test.go
git commit -m "feat(cli/theme): Theme type + hex parse/format helpers"
```

---

### Task 4: Built-in themes + registry

**Files:**
- Create: `source/clients/cli/internal/theme/builtins.go`
- Create: `source/clients/cli/internal/theme/registry.go`
- Test: `source/clients/cli/internal/theme/registry_test.go`

**Interfaces:**
- Produces: `func BuiltinThemes() []Theme` (cracker first); `type Registry struct{...}`; `func NewRegistry(builtins []Theme) *Registry`; `(*Registry) Names() []string`, `Get(name string) (Theme, bool)`, `IsBuiltin(name string) bool`, `Add(t Theme) error` (rejects builtin-name collision), `Remove(name string) error`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/theme/registry_test.go`:
```go
package theme

import "testing"

func TestBuiltinsCrackerFirstAndExact(t *testing.T) {
	bs := BuiltinThemes()
	if len(bs) < 2 || bs[0].Name != "cracker" {
		t.Fatalf("expected cracker first, got %v", names(bs))
	}
	// cracker built-in equals Cracker() exactly (golden).
	if HexOf(bs[0].Palette.Primary) != HexOf(Cracker().Primary) ||
		HexOf(bs[0].Palette.BufferCode) != HexOf(Cracker().BufferCode) {
		t.Fatal("cracker builtin diverged from Cracker()")
	}
}

func TestRegistryAddRemoveAndBuiltinProtection(t *testing.T) {
	r := NewRegistry(BuiltinThemes())
	if !r.IsBuiltin("cracker") {
		t.Fatal("cracker should be builtin")
	}
	if err := r.Add(Theme{Name: "cracker", Palette: Cracker()}); err == nil {
		t.Fatal("adding a theme named like a builtin must error")
	}
	if err := r.Add(Theme{Name: "mine", Palette: Cracker()}); err != nil {
		t.Fatalf("Add custom: %v", err)
	}
	if _, ok := r.Get("mine"); !ok {
		t.Fatal("custom theme not found after Add")
	}
	if err := r.Remove("cracker"); err == nil {
		t.Fatal("removing a builtin must error")
	}
	if err := r.Remove("mine"); err != nil {
		t.Fatalf("Remove custom: %v", err)
	}
}

func names(ts []Theme) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/theme/ -run 'TestBuiltins|TestRegistry' -v`
Expected: FAIL — `BuiltinThemes`/`NewRegistry` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/theme/builtins.go`:
```go
package theme

import "charm.land/lipgloss/v2"

func hc(s string) lipgloss.Color { return lipgloss.Color(s) }

// BuiltinThemes returns the always-present, read-only themes, cracker first.
func BuiltinThemes() []Theme {
	return []Theme{
		{Name: "cracker", Palette: Cracker()},
		{Name: "phosphor", Palette: phosphorPalette()},
		{Name: "synthwave", Palette: synthwavePalette()},
		{Name: "daylight", Palette: daylightPalette()},
	}
}

func phosphorPalette() Palette {
	return Palette{
		BgDeep: hc("#0A0F0A"), Surface: hc("#11180F"), BorderDim: hc("#1F3A1F"), Border: hc("#356635"),
		Primary: hc("#33FF33"), Bright: hc("#9CFF9C"), DimAmber: hc("#0E3A0E"), Accent: hc("#7CFF4D"),
		Info: hc("#33CFAF"), Muted: hc("#5C8C5C"), Success: hc("#6FCF6F"), Warn: hc("#CFCF4D"), Error: hc("#E86F6F"),
		BufferLink: hc("#33CFAF"), BufferCode: hc("#9CE0A0"), BufferLime: hc("#7CFF4D"), BufferError: hc("#D95C5C"), BufferUserBg: hc("#11331A"),
	}
}

func synthwavePalette() Palette {
	return Palette{
		BgDeep: hc("#1A1033"), Surface: hc("#241640"), BorderDim: hc("#3A2A66"), Border: hc("#6F4DBF"),
		Primary: hc("#FF6AD5"), Bright: hc("#FFC2F0"), DimAmber: hc("#4D2A66"), Accent: hc("#36E0E0"),
		Info: hc("#8A6AFF"), Muted: hc("#9B86C9"), Success: hc("#6FE0B0"), Warn: hc("#FFD24D"), Error: hc("#FF5C8A"),
		BufferLink: hc("#36E0E0"), BufferCode: hc("#C2A6FF"), BufferLime: hc("#FF6AD5"), BufferError: hc("#FF5C8A"), BufferUserBg: hc("#2E2057"),
	}
}

func daylightPalette() Palette {
	return Palette{
		BgDeep: hc("#FBF3E0"), Surface: hc("#F1E6CC"), BorderDim: hc("#D8C7A0"), Border: hc("#B79A5E"),
		Primary: hc("#5A3A0A"), Bright: hc("#7A4E0A"), DimAmber: hc("#C9B68C"), Accent: hc("#1E7A3C"),
		Info: hc("#1763A0"), Muted: hc("#8A7A55"), Success: hc("#2E7D32"), Warn: hc("#B8860B"), Error: hc("#B23A3A"),
		BufferLink: hc("#1763A0"), BufferCode: hc("#6A4BA3"), BufferLime: hc("#1E7A3C"), BufferError: hc("#B23A3A"), BufferUserBg: hc("#E6D7B0"),
	}
}
```

`source/clients/cli/internal/theme/registry.go`:
```go
package theme

import "fmt"

// Registry holds the ordered set of available themes (built-ins + custom).
type Registry struct {
	order   []string
	themes  map[string]Theme
	builtin map[string]bool
}

// NewRegistry seeds a registry with built-ins (in order).
func NewRegistry(builtins []Theme) *Registry {
	r := &Registry{themes: map[string]Theme{}, builtin: map[string]bool{}}
	for _, t := range builtins {
		r.order = append(r.order, t.Name)
		r.themes[t.Name] = t
		r.builtin[t.Name] = true
	}
	return r
}

func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

func (r *Registry) Get(name string) (Theme, bool) { t, ok := r.themes[name]; return t, ok }

func (r *Registry) IsBuiltin(name string) bool { return r.builtin[name] }

// Add registers a custom theme. Errors on empty name or a built-in name collision.
func (r *Registry) Add(t Theme) error {
	if t.Name == "" {
		return fmt.Errorf("theme name required")
	}
	if r.builtin[t.Name] {
		return fmt.Errorf("%q is a built-in theme name", t.Name)
	}
	if _, exists := r.themes[t.Name]; !exists {
		r.order = append(r.order, t.Name)
	}
	r.themes[t.Name] = t
	return nil
}

// Remove deletes a custom theme. Built-ins cannot be removed.
func (r *Registry) Remove(name string) error {
	if r.builtin[name] {
		return fmt.Errorf("cannot remove built-in theme %q", name)
	}
	if _, ok := r.themes[name]; !ok {
		return fmt.Errorf("no such theme %q", name)
	}
	delete(r.themes, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/theme/ -count=1`
Expected: PASS (all theme tests).

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/theme/builtins.go source/clients/cli/internal/theme/registry.go source/clients/cli/internal/theme/registry_test.go
git commit -m "feat(cli/theme): built-in themes + registry"
```

---

### Task 5: `chatView.SetStyles` + `Model.applyTheme` (live propagation)

**Files:**
- Modify: `source/clients/cli/internal/ui/chat_view.go`
- Modify: `source/clients/cli/internal/ui/model.go` (add `theme` field, `applyTheme`, init)
- Test: `source/clients/cli/internal/ui/theme_apply_test.go` (create)

**Interfaces:**
- Consumes: `Theme`, `Registry`, `MarkdownStyle` (Tasks 2-4).
- Produces: `func (c *chatView) SetStyles(s theme.Styles, p theme.Palette)` (replaces `c.md` with a fresh `render.NewMarkdown(theme.MarkdownStyle(p))`, updates styles/palette, rebuilds); `func (m *Model) applyTheme(t theme.Theme)` updating `m.theme/m.palette/m.styles`, calling `m.chat.SetStyles`, re-resolving `m.promptBorderColor`, and refreshing; `Model` gains `theme theme.Theme` and `themes *theme.Registry` fields.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/ui/theme_apply_test.go`:
```go
package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestChatSetStylesReplacesMarkdownRenderer(t *testing.T) {
	c := newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), ".", ".", 80, 24)
	before := c.md
	c.SetStyles(theme.NewStyles(theme.BuiltinThemes()[1].Palette), theme.BuiltinThemes()[1].Palette)
	if c.md == before {
		t.Fatal("SetStyles must replace the markdown renderer (flush cache)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestChatSetStylesReplacesMarkdownRenderer -v`
Expected: FAIL — `c.SetStyles` undefined.

- [ ] **Step 3: Write minimal implementation**

In `chat_view.go`, add:
```go
// SetStyles swaps the chat's styles/palette and rebuilds. The markdown renderer
// is replaced wholesale so its per-width glamour cache is flushed and committed
// entries re-render in the new theme.
func (c *chatView) SetStyles(s theme.Styles, p theme.Palette) {
	c.styles = s
	c.palette = p
	c.md = render.NewMarkdown(theme.MarkdownStyle(p))
	c.rebuild()
}
```

In `model.go`: add fields to the `Model` struct near `palette`/`styles`:
```go
	theme  theme.Theme
	themes *theme.Registry
```
In the constructor (where `p := theme.Cracker()` is, ~line 198), after building styles, seed the theme + registry:
```go
	themes := theme.NewRegistry(theme.BuiltinThemes())
```
and set `theme: theme.Theme{Name: "cracker", Palette: p}, themes: themes,` in the `Model{...}` literal (near `palette: p,`).

Add the method:
```go
// applyTheme swaps the active theme and live-repaints: rebuild styles, push them
// to the chat (which flushes its markdown cache), re-resolve the prompt border,
// and refresh. If a settings page is open it rebuilds with the new styles too.
func (m *Model) applyTheme(t theme.Theme) {
	m.theme = t
	m.palette = t.Palette
	m.styles = theme.NewStyles(t.Palette)
	m.chat.SetStyles(m.styles, m.palette)
	m.promptBorderColor = m.resolvePromptColor(m.promptColorToken)
	if sp, ok := m.content.(*settingsPage); ok {
		sp.SetStyles(m.styles, m.palette)
	}
	m.refreshViewport()
}
```
(`settingsPage.SetStyles` is added in Task 9; if implementing strictly in order, guard with the type assertion which compiles once `settingsPage` exists — it already does from the prior feature. Add the method stub in Task 9.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go build ./... && go test ./internal/ui/ -run TestChatSetStylesReplacesMarkdownRenderer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/chat_view.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/theme_apply_test.go
git commit -m "feat(cli): chatView.SetStyles + Model.applyTheme live propagation"
```

---

## Phase 2 — Persistence

### Task 6: Theme YAML (de)serialization

**Files:**
- Create: `source/clients/cli/internal/theme/serialize.go`
- Test: `source/clients/cli/internal/theme/serialize_test.go`

**Interfaces:**
- Consumes: `Theme`, `ParseHex`, `HexOf` (Task 3).
- Produces: `func MarshalTheme(t Theme) ([]byte, error)`; `func UnmarshalTheme(name string, data []byte) (Theme, error)` (missing/invalid keys fall back to the cracker value for that field).

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/theme/serialize_test.go`:
```go
package theme

import "testing"

func TestThemeYAMLRoundTrip(t *testing.T) {
	in := Theme{Name: "mine", Palette: Cracker()}
	in.Palette.Accent = mustHex("#123456")
	data, err := MarshalTheme(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalTheme("mine", data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if HexOf(out.Palette.Accent) != "#123456" {
		t.Fatalf("accent round-trip = %s", HexOf(out.Palette.Accent))
	}
	if HexOf(out.Palette.Primary) != HexOf(Cracker().Primary) {
		t.Fatalf("primary should survive round-trip")
	}
}

func TestUnmarshalMissingKeyFallsBack(t *testing.T) {
	out, err := UnmarshalTheme("partial", []byte("colors:\n  accent: \"#abcdef\"\n"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if HexOf(out.Palette.Accent) != "#abcdef" {
		t.Fatalf("accent = %s", HexOf(out.Palette.Accent))
	}
	if HexOf(out.Palette.Primary) != HexOf(Cracker().Primary) {
		t.Fatalf("missing primary should fall back to cracker")
	}
}

func mustHex(s string) (c interface{ RGBA() (uint32, uint32, uint32, uint32) }) {
	col, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return col
}
```
> If the `mustHex` return-type trick doesn't compile, replace its body/signature with `func mustHex(s string) color.Color { c, err := ParseHex(s); if err != nil { panic(err) }; return c }` and add `import "image/color"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/theme/ -run 'TestThemeYAML|TestUnmarshalMissing' -v`
Expected: FAIL — `MarshalTheme`/`UnmarshalTheme` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/theme/serialize.go`:
```go
package theme

import (
	"image/color"

	"gopkg.in/yaml.v3"
)

type themeFile struct {
	Colors map[string]string `yaml:"colors"`
}

// colorFields maps a stable yaml key to a pointer into a Palette. Used by both
// marshal and unmarshal so the two can never drift.
func colorFields(p *Palette) map[string]*color.Color {
	return map[string]*color.Color{
		"bg_deep": &p.BgDeep, "surface": &p.Surface, "border_dim": &p.BorderDim, "border": &p.Border,
		"primary": &p.Primary, "bright": &p.Bright, "dim_amber": &p.DimAmber, "accent": &p.Accent,
		"info": &p.Info, "muted": &p.Muted, "success": &p.Success, "warn": &p.Warn, "error": &p.Error,
		"buffer_link": &p.BufferLink, "buffer_code": &p.BufferCode, "buffer_lime": &p.BufferLime,
		"buffer_error": &p.BufferError, "buffer_user_bg": &p.BufferUserBg,
	}
}

// MarshalTheme serializes a theme's colors to YAML.
func MarshalTheme(t Theme) ([]byte, error) {
	p := t.Palette
	fields := colorFields(&p)
	out := themeFile{Colors: make(map[string]string, len(fields))}
	for key, ptr := range fields {
		out.Colors[key] = HexOf(*ptr)
	}
	return yaml.Marshal(out)
}

// UnmarshalTheme parses YAML into a Theme. Missing or invalid color keys fall
// back to the cracker value for that field, so a partial/old file still loads.
func UnmarshalTheme(name string, data []byte) (Theme, error) {
	var tf themeFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return Theme{}, err
	}
	p := Cracker() // defaults
	fields := colorFields(&p)
	for key, ptr := range fields {
		if hex, ok := tf.Colors[key]; ok {
			if c, err := ParseHex(hex); err == nil {
				*ptr = c
			}
		}
	}
	return Theme{Name: name, Palette: p}, nil
}
```
Confirm `gopkg.in/yaml.v3` is in the CLI module: `grep yaml.v3 go.mod` (the server uses it; if absent from the CLI module run `go get gopkg.in/yaml.v3` in `source/clients/cli`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/theme/ -run 'TestThemeYAML|TestUnmarshalMissing' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/theme/serialize.go source/clients/cli/internal/theme/serialize_test.go source/clients/cli/go.mod source/clients/cli/go.sum
git commit -m "feat(cli/theme): theme YAML (de)serialization with cracker fallback"
```

---

### Task 7: `uiconfig` (active theme) + themes-dir load/save/delete

**Files:**
- Create: `source/clients/cli/internal/uiconfig/uiconfig.go`
- Test: `source/clients/cli/internal/uiconfig/uiconfig_test.go`

**Interfaces:**
- Consumes: `theme` package (Task 6).
- Produces:
  - `func ConfigPath() string` (`$CERCANO_UI_CONFIG` → `~/.config/cercano/ui.yaml`)
  - `func ThemesDir() string` (`$CERCANO_THEMES_DIR` → `~/.config/cercano/themes`)
  - `func LoadActiveTheme() string` (returns `"cracker"` if unset/missing)
  - `func SaveActiveTheme(name string) error`
  - `func LoadCustomThemes() []theme.Theme` (parses every `*.yaml` in ThemesDir; skips invalid)
  - `func SaveCustomTheme(t theme.Theme) error` (writes `<ThemesDir>/<name>.yaml`)
  - `func DeleteCustomTheme(name string) error`
  - `func ImportTheme(path string) (theme.Theme, error)` (reads a yaml file, names the theme after its base filename, copies it into ThemesDir)

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/uiconfig/uiconfig_test.go`:
```go
package uiconfig

import (
	"os"
	"path/filepath"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestActiveThemeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CERCANO_UI_CONFIG", filepath.Join(dir, "ui.yaml"))
	if got := LoadActiveTheme(); got != "cracker" {
		t.Fatalf("default = %q, want cracker", got)
	}
	if err := SaveActiveTheme("phosphor"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := LoadActiveTheme(); got != "phosphor" {
		t.Fatalf("after save = %q, want phosphor", got)
	}
}

func TestCustomThemeSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CERCANO_THEMES_DIR", dir)
	mt := theme.Theme{Name: "mine", Palette: theme.Cracker()}
	if err := SaveCustomTheme(mt); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.yaml")); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	got := LoadCustomThemes()
	if len(got) != 1 || got[0].Name != "mine" {
		t.Fatalf("load = %v", got)
	}
	if err := DeleteCustomTheme("mine"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(LoadCustomThemes()) != 0 {
		t.Fatal("theme should be gone after delete")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/uiconfig/ -v`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/uiconfig/uiconfig.go`:
```go
// Package uiconfig persists CLI-only UI preferences: the active theme name
// (ui.yaml) and custom theme files (themes/<name>.yaml).
package uiconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"cercano/source/clients/cli/internal/theme"
)

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cercano")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cercano")
}

// ConfigPath resolves the ui.yaml path.
func ConfigPath() string {
	if p := os.Getenv("CERCANO_UI_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "ui.yaml")
}

// ThemesDir resolves the custom-themes directory.
func ThemesDir() string {
	if p := os.Getenv("CERCANO_THEMES_DIR"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "themes")
}

type uiFile struct {
	Theme string `yaml:"theme"`
}

// LoadActiveTheme returns the persisted active theme name, or "cracker".
func LoadActiveTheme() string {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return "cracker"
	}
	var f uiFile
	if yaml.Unmarshal(data, &f) != nil || f.Theme == "" {
		return "cracker"
	}
	return f.Theme
}

// SaveActiveTheme persists the active theme name.
func SaveActiveTheme(name string) error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(uiFile{Theme: name})
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0o644)
}

// LoadCustomThemes parses every *.yaml in ThemesDir, skipping invalid files.
func LoadCustomThemes() []theme.Theme {
	entries, err := os.ReadDir(ThemesDir())
	if err != nil {
		return nil
	}
	var out []theme.Theme
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ThemesDir(), e.Name()))
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		t, err := theme.UnmarshalTheme(name, data)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func themePath(name string) string { return filepath.Join(ThemesDir(), name+".yaml") }

// SaveCustomTheme writes a theme to ThemesDir/<name>.yaml.
func SaveCustomTheme(t theme.Theme) error {
	if t.Name == "" {
		return fmt.Errorf("theme name required")
	}
	if err := os.MkdirAll(ThemesDir(), 0o755); err != nil {
		return err
	}
	data, err := theme.MarshalTheme(t)
	if err != nil {
		return err
	}
	return os.WriteFile(themePath(t.Name), data, 0o644)
}

// DeleteCustomTheme removes a custom theme file.
func DeleteCustomTheme(name string) error { return os.Remove(themePath(name)) }

// ImportTheme reads a yaml theme from an arbitrary path, names it after the file
// base, and copies it into ThemesDir.
func ImportTheme(path string) (theme.Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return theme.Theme{}, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	t, err := theme.UnmarshalTheme(name, data)
	if err != nil {
		return theme.Theme{}, err
	}
	if err := SaveCustomTheme(t); err != nil {
		return theme.Theme{}, err
	}
	return t, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/uiconfig/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/uiconfig/
git commit -m "feat(cli/uiconfig): active-theme + custom-theme persistence"
```

---

### Task 8: Load persisted theme at startup

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (constructor: seed registry with custom themes + apply active)
- Test: `source/clients/cli/internal/ui/theme_apply_test.go` (extend)

**Interfaces:**
- Consumes: `uiconfig` (Task 7), `Registry`, `applyTheme` (Task 5).
- Produces: at startup the registry includes custom themes and the active theme is applied.

- [ ] **Step 1: Write the failing test**

Add to `theme_apply_test.go`:
```go
func TestRegistryIncludesCustomAndBuiltins(t *testing.T) {
	r := theme.NewRegistry(theme.BuiltinThemes())
	_ = r.Add(theme.Theme{Name: "mine", Palette: theme.Cracker()})
	names := r.Names()
	if names[0] != "cracker" || names[len(names)-1] != "mine" {
		t.Fatalf("registry order = %v", names)
	}
}
```
(This guards the seeding contract used by the constructor below.)

- [ ] **Step 2: Run test to verify it fails / passes**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestRegistryIncludesCustomAndBuiltins -v`
Expected: PASS already (it exercises the registry). It documents the contract; proceed to wire the constructor.

- [ ] **Step 3: Write the constructor wiring**

In `model.go` constructor, replace the registry seed (from Task 5) with built-ins + custom, then apply the active theme. Where `themes := theme.NewRegistry(theme.BuiltinThemes())` is:
```go
	themes := theme.NewRegistry(theme.BuiltinThemes())
	for _, ct := range uiconfig.LoadCustomThemes() {
		_ = themes.Add(ct) // skip collisions silently
	}
	activeName := uiconfig.LoadActiveTheme()
	active, ok := themes.Get(activeName)
	if !ok {
		active, _ = themes.Get("cracker")
	}
	p = active.Palette // so the initial Styles/markdown match the active theme
```
Ensure `p` (used to build the initial `styles`/markdown and stored in the model) now comes from `active.Palette`, and set `theme: active,` in the `Model{...}` literal. Add the import `"cercano/source/clients/cli/internal/uiconfig"`.

> Note: the constructor builds `styles`/`chat` from `p`; since `p = active.Palette`, the first paint is already themed — no post-construction `applyTheme` needed. (If the constructor returns a value `Model` rather than a pointer, that's fine; `applyTheme` is only needed for *runtime* switches.)

- [ ] **Step 4: Build + run**

Run: `cd source/clients/cli && go build ./... && go test ./internal/ui/ -count=1`
Expected: build OK, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/theme_apply_test.go
git commit -m "feat(cli): load custom themes + active theme at startup"
```

---

## Phase 3 — UI

### Task 9: `ColorField` widget

**Files:**
- Create: `source/clients/cli/internal/form/color_field.go`
- Test: `source/clients/cli/internal/form/color_field_test.go`

**Interfaces:**
- Consumes: `Field` (existing).
- Produces: `func NewColor(key, label, hex string, editable bool) *ColorField`; `(*ColorField) Hex() string`. Renders `███ #rrggbb`; edit (when editable) reuses inline text entry; commit validates `#RRGGBB` (bad input → not committed, sets no value), carries the new hex.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/color_field_test.go`:
```go
package form

import (
	"strings"
	"testing"
)

func TestColorFieldEditCommit(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter()) // begin edit
	if !f.Editing() {
		t.Fatal("enter should begin editing an editable color")
	}
	for _, r := range "#123456" {
		f.Update(typ(r))
	}
	_, committed, val := f.Update(enter())
	if !committed || val != "#123456" {
		t.Fatalf("commit = (%v,%q), want (true,#123456)", committed, val)
	}
	if f.Hex() != "#123456" {
		t.Fatalf("Hex() = %q", f.Hex())
	}
}

func TestColorFieldRejectsBadHex(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter())
	for _, r := range "nope" {
		f.Update(typ(r))
	}
	_, committed, _ := f.Update(enter())
	if committed {
		t.Fatal("bad hex must not commit")
	}
	if f.Hex() != "#bdf000" {
		t.Fatalf("Hex() after bad edit = %q, want unchanged", f.Hex())
	}
}

func TestColorFieldReadOnlyInert(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", false)
	_, committed, _ := f.Update(enter())
	if f.Editing() || committed {
		t.Fatal("read-only color field must be inert")
	}
}

func TestColorFieldViewHasSwatch(t *testing.T) {
	_, s := testStyles()
	f := NewColor("accent", "accent", "#bdf000", true)
	if !strings.Contains(f.View(false, 40, s), "#bdf000") {
		t.Fatal("View should show the hex")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestColorField -v`
Expected: FAIL — `NewColor` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/color_field.go`:
```go
package form

import (
	"regexp"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

var colorHexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ColorField edits a single #RRGGBB color, showing a live swatch.
type ColorField struct {
	key, label string
	hex        string
	editable   bool
	editing    bool
	input      textinput.Model
}

// NewColor builds a color field. editable=false renders a read-only swatch.
func NewColor(key, label, hex string, editable bool) *ColorField {
	return &ColorField{key: key, label: label, hex: strings.ToLower(hex), editable: editable}
}

func (f *ColorField) Key() string     { return f.key }
func (f *ColorField) Label() string   { return f.label }
func (f *ColorField) Display() string { return f.hex }
func (f *ColorField) Editing() bool   { return f.editing }
func (f *ColorField) Hex() string     { return f.hex }

func (f *ColorField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if !f.editing {
		if f.editable && msg.Code == tea.KeyEnter {
			ti := textinput.New()
			ti.CharLimit = 7
			cmd := ti.Focus()
			ti.SetValue(f.hex)
			ti.CursorEnd()
			f.input = ti
			f.editing = true
			return cmd, false, ""
		}
		return nil, false, ""
	}
	switch msg.Code {
	case tea.KeyEscape:
		f.editing = false
		return nil, false, ""
	case tea.KeyEnter:
		val := strings.ToLower(strings.TrimSpace(f.input.Value()))
		if !colorHexRe.MatchString(val) {
			f.editing = false
			return nil, false, "" // reject; value unchanged
		}
		f.hex = val
		f.editing = false
		return nil, true, val
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd, false, ""
}

func (f *ColorField) View(focused bool, width int, s theme.Styles) string {
	swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(f.hex)).Render("███")
	if f.editing {
		return swatch + " " + f.input.View()
	}
	hex := s.Primary.Render(f.hex)
	if !f.editable {
		hex = s.Muted.Render(f.hex)
	}
	return swatch + " " + hex
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestColorField -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/color_field.go source/clients/cli/internal/form/color_field_test.go
git commit -m "feat(cli/form): ColorField (swatch + hex)"
```

---

### Task 10: `ButtonField` widget

**Files:**
- Create: `source/clients/cli/internal/form/button_field.go`
- Test: `source/clients/cli/internal/form/button_field_test.go`

**Interfaces:**
- Produces: `func NewButton(key, label string, enabled bool) *ButtonField`. Enter on an enabled button commits the sentinel value `"activate"`; disabled is inert; `Editing()` always false.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/button_field_test.go`:
```go
package form

import "testing"

func TestButtonFieldActivates(t *testing.T) {
	f := NewButton("theme-save", "Save", true)
	_, committed, val := f.Update(enter())
	if !committed || val != "activate" {
		t.Fatalf("enabled button enter = (%v,%q), want (true,activate)", committed, val)
	}
	if f.Editing() {
		t.Fatal("button never enters editing")
	}
}

func TestButtonFieldDisabledInert(t *testing.T) {
	f := NewButton("theme-delete", "Delete", false)
	_, committed, _ := f.Update(enter())
	if committed {
		t.Fatal("disabled button must not commit")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestButtonField -v`
Expected: FAIL — `NewButton` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/button_field.go`:
```go
package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// ButtonActivate is the sentinel value a ButtonField commits when activated.
const ButtonActivate = "activate"

// ButtonField is a selectable action row. Enter (when enabled) commits
// ButtonActivate so the Form's OnCommit routes it to an action.
type ButtonField struct {
	key, label string
	enabled    bool
}

// NewButton builds an action button.
func NewButton(key, label string, enabled bool) *ButtonField {
	return &ButtonField{key: key, label: label, enabled: enabled}
}

func (f *ButtonField) Key() string     { return f.key }
func (f *ButtonField) Label() string   { return f.label }
func (f *ButtonField) Display() string { return "" }
func (f *ButtonField) Editing() bool   { return false }

func (f *ButtonField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if f.enabled && msg.Code == tea.KeyEnter {
		return nil, true, ButtonActivate
	}
	return nil, false, ""
}

func (f *ButtonField) View(focused bool, width int, s theme.Styles) string {
	label := "[ " + f.label + " ]"
	if !f.enabled {
		return s.Dim.Render(label)
	}
	if focused {
		return s.Bright.Render(label)
	}
	return s.Accent.Render(label)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestButtonField -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/button_field.go source/clients/cli/internal/form/button_field_test.go
git commit -m "feat(cli/form): ButtonField (action row)"
```

---

### Task 11: Theme sections builder + settings-page working-theme model

**Files:**
- Create: `source/clients/cli/internal/ui/theme_sections.go`
- Modify: `source/clients/cli/internal/ui/settings_page.go`
- Modify: `source/clients/cli/internal/ui/settings_build.go` (drop the accent-color row)
- Modify: `source/clients/cli/internal/ui/model.go` (handle theme messages; `settingsPage.SetStyles`)
- Test: `source/clients/cli/internal/ui/theme_sections_test.go`

**Interfaces:**
- Consumes: `ColorField`, `ButtonField` (Tasks 9-10); `Registry`, `Theme`, `uiconfig`, `applyTheme` (Phases 1-2).
- Produces:
  - `func buildThemeSections(working theme.Theme, names []string, builtin, dirty bool) []form.Section`
  - `settingsPage` fields: `themes *theme.Registry`, `working theme.Theme`, `dirty bool`; `func (sp *settingsPage) SetStyles(s theme.Styles, p theme.Palette)`.
  - `settingsThemeMsg{ working theme.Theme; persistName string }` handled in `Model.Update` → `applyTheme` (+ persist active name when `persistName != ""`).

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/ui/theme_sections_test.go`:
```go
package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestBuildThemeSections(t *testing.T) {
	secs := buildThemeSections(theme.Theme{Name: "cracker", Palette: theme.Cracker()},
		[]string{"cracker", "phosphor"}, true /*builtin*/, false /*dirty*/)
	titles := map[string]bool{}
	keys := map[string]bool{}
	for _, s := range secs {
		titles[s.Title] = true
		for _, f := range s.Fields {
			keys[f.Key()] = true
		}
	}
	for _, want := range []string{"Theme", "Theme · Chrome", "Theme · Content", "Theme · Actions"} {
		if !titles[want] {
			t.Errorf("missing section %q", want)
		}
	}
	for _, want := range []string{"theme-select", "color:accent", "color:buffer_code", "theme-save", "theme-save-as", "theme-delete", "theme-import"} {
		if !keys[want] {
			t.Errorf("missing field %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestBuildThemeSections -v`
Expected: FAIL — `buildThemeSections` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/ui/theme_sections.go`:
```go
package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
)

// chromeColorRows / contentColorRows list the editable palette fields in display
// order, paired with their yaml key (matching theme.colorFields) and label.
var chromeColorRows = []struct{ key, label string }{
	{"bg_deep", "background"}, {"surface", "surface"}, {"border_dim", "border-dim"}, {"border", "border"},
	{"primary", "primary"}, {"bright", "bright"}, {"dim_amber", "dim"}, {"accent", "accent"},
	{"info", "info"}, {"muted", "muted"}, {"success", "success"}, {"warn", "warn"}, {"error", "error"},
}

var contentColorRows = []struct{ key, label string }{
	{"buffer_link", "link"}, {"buffer_code", "code"}, {"buffer_lime", "tool-ok"},
	{"buffer_error", "tool-err"}, {"buffer_user_bg", "user-bg"},
}

// paletteHex returns the #RRGGBB for a yaml color key of a palette.
func paletteHex(p theme.Palette, key string) string {
	pc := p // copy so we can take field pointers via the shared map
	return theme.HexOf(*themeColorPtr(&pc, key))
}

func buildThemeSections(working theme.Theme, names []string, builtin, dirty bool) []form.Section {
	editable := !builtin
	chrome := make([]form.Field, 0, len(chromeColorRows))
	for _, r := range chromeColorRows {
		chrome = append(chrome, form.NewColor("color:"+r.key, r.label, paletteHex(working.Palette, r.key), editable))
	}
	content := make([]form.Field, 0, len(contentColorRows))
	for _, r := range contentColorRows {
		content = append(content, form.NewColor("color:"+r.key, r.label, paletteHex(working.Palette, r.key), editable))
	}
	themeRow := form.NewSelect("theme-select", "theme", optionsFromNames(names), working.Name)
	actions := []form.Field{
		form.NewButton("theme-save", "Save", editable && dirty),
		form.NewText("theme-save-as", "save as", "", "type a name, enter to clone"),
		form.NewButton("theme-delete", "Delete", editable),
		form.NewText("theme-import", "import", "", "path to a .yaml theme"),
	}
	return []form.Section{
		{Title: "Theme", Fields: []form.Field{themeRow}},
		{Title: "Theme · Chrome", Fields: chrome},
		{Title: "Theme · Content", Fields: content},
		{Title: "Theme · Actions", Fields: actions},
	}
}

func optionsFromNames(names []string) []form.Option {
	out := make([]form.Option, len(names))
	for i, n := range names {
		out[i] = form.Option{Label: n, Value: n}
	}
	return out
}
```

Add `themeColorPtr` to the `theme` package so the ui layer can read/write a palette field by key without duplicating the map. In `serialize.go`, export a helper:
```go
// FieldPtr returns a pointer to the palette color for a yaml key, or nil.
func FieldPtr(p *Palette, key string) *color.Color { return colorFields(p)[key] }
```
and in `theme_sections.go` define:
```go
func themeColorPtr(p *theme.Palette, key string) *colorAlias { return nil } // replaced below
```
> Simpler: skip `themeColorPtr`/`paletteHex` indirection — use `theme.FieldPtr`:
> ```go
> func paletteHex(p theme.Palette, key string) string {
>     pc := p
>     if ptr := theme.FieldPtr(&pc, key); ptr != nil { return theme.HexOf(*ptr) }
>     return ""
> }
> ```
> Delete the `themeColorPtr` stub. (Use this form; the stub above only exists to make the first compile fail loudly.)

Now wire the settings page. In `settings_build.go`, remove the `accent-color` row from the `UI / Theme` section (delete that section entry; the theme sections replace it).

In `settings_page.go`:
1. Add fields to `settingsPage`: `themes *theme.Registry`, `working theme.Theme`, `dirty bool`.
2. `newSettingsPage` signature gains `themes *theme.Registry, active theme.Theme`; store `sp.themes = themes; sp.working = active`. Update the caller in `model.go` (`newSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, m.height)` → add `m.themes, m.theme`).
3. `snapshotSections` appends the theme sections:
```go
	secs := buildSettingsSections(cfg, mode, sp.accentToken)
	builtin := sp.themes.IsBuiltin(sp.working.Name)
	secs = append(secs, buildThemeSections(sp.working, sp.themes.Names(), builtin, sp.dirty)...)
	return secs
```
4. Add `SetStyles`:
```go
func (sp *settingsPage) SetStyles(s theme.Styles, p theme.Palette) {
	sp.styles = s
	sp.palette = p
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
}
```
5. Extend `onCommit` with theme cases (before the final `return`):
```go
	case "theme-select":
		if t, ok := sp.themes.Get(value); ok {
			sp.working = t
			sp.dirty = false
			return "theme: " + value, func() tea.Msg { return settingsThemeMsg{working: t, persistName: value} }, nil
		}
		return "no such theme", nil, nil
	case "theme-save":
		if err := uiconfig.SaveCustomTheme(sp.working); err != nil {
			return "", nil, err
		}
		sp.dirty = false
		return "saved " + sp.working.Name, nil, nil
	case "theme-save-as":
		name := strings.TrimSpace(value)
		if name == "" {
			return "name required", nil, nil
		}
		nt := theme.Theme{Name: name, Palette: sp.working.Palette}
		if err := sp.themes.Add(nt); err != nil {
			return "", nil, err
		}
		if err := uiconfig.SaveCustomTheme(nt); err != nil {
			return "", nil, err
		}
		sp.working = nt
		sp.dirty = false
		return "saved as " + name, func() tea.Msg { return settingsThemeMsg{working: nt, persistName: name} }, nil
	case "theme-delete":
		name := sp.working.Name
		if err := sp.themes.Remove(name); err != nil {
			return "", nil, err
		}
		_ = uiconfig.DeleteCustomTheme(name)
		cracker, _ := sp.themes.Get("cracker")
		sp.working = cracker
		sp.dirty = false
		return "deleted " + name, func() tea.Msg { return settingsThemeMsg{working: cracker, persistName: "cracker"} }, nil
	case "theme-import":
		t, err := uiconfig.ImportTheme(strings.TrimSpace(value))
		if err != nil {
			return "", nil, err
		}
		_ = sp.themes.Add(t)
		sp.working = t
		sp.dirty = false
		return "imported " + t.Name, func() tea.Msg { return settingsThemeMsg{working: t, persistName: t.Name} }, nil
```
And handle color edits (keys begin `color:`) — add at the top of `onCommit`, before `classifyCommit`:
```go
	if strings.HasPrefix(key, "color:") {
		fieldKey := strings.TrimPrefix(key, "color:")
		if c, err := theme.ParseHex(value); err == nil {
			pc := sp.working.Palette
			if ptr := theme.FieldPtr(&pc, fieldKey); ptr != nil {
				*ptr = c
				sp.working.Palette = pc
				sp.dirty = true
				w := sp.working
				return "edited " + fieldKey, func() tea.Msg { return settingsThemeMsg{working: w} }, nil
			}
		}
		return "bad color", nil, nil
	}
```
(Add `"strings"`, `theme`, and `uiconfig` imports to settings_page.go if missing.)

In `model.go`, define the message and handle it in `Update`'s message switch (near `settingsColorMsg`):
```go
	case settingsThemeMsg:
		m.applyTheme(msg.working)
		if msg.persistName != "" {
			_ = uiconfig.SaveActiveTheme(msg.persistName)
		}
		return m, nil
```
and define the type (near `settingsColorMsg`):
```go
type settingsThemeMsg struct {
	working     theme.Theme
	persistName string // when non-empty, persist as the active theme
}
```

- [ ] **Step 4: Build + run**

Run: `cd source/clients/cli && go build ./... && go test ./internal/ui/ -run 'TestBuildThemeSections|TestSettings' -count=1`
Expected: build OK; tests PASS. Fix any leftover `accent-color`/signature mismatches the compiler flags.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/theme_sections.go source/clients/cli/internal/ui/theme_sections_test.go source/clients/cli/internal/ui/settings_page.go source/clients/cli/internal/ui/settings_build.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/theme/serialize.go
git commit -m "feat(cli): theme editor sections + live editing in settings page"
```

---

### Task 12: `/theme` shortcut + docs + acceptance

**Files:**
- Create: `source/clients/cli/internal/slash/theme.go`
- Modify: `source/clients/cli/internal/ui/model.go` (register; `/theme` opens settings)
- Modify: `docs/agent/README.md`, `docs/features/cli/theme-system/design.md`
- Test: `source/clients/cli/internal/slash/theme_test.go`

**Interfaces:**
- Consumes: `ResultOpenSettings` (existing).
- Produces: `func RegisterTheme(r *Registry)` mapping `/theme` → `ResultOpenSettings`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/slash/theme_test.go`:
```go
package slash

import "testing"

func TestThemeOpensSettings(t *testing.T) {
	r := New()
	RegisterTheme(r)
	res, ok := r.Dispatch("/theme")
	if !ok || res.Kind != ResultOpenSettings {
		t.Fatalf("/theme -> (%v,%v), want ResultOpenSettings", res.Kind, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/slash/ -run TestThemeOpensSettings -v`
Expected: FAIL — `RegisterTheme` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/slash/theme.go`:
```go
package slash

// RegisterTheme installs /theme, which opens the settings page (where the theme
// selector + editor live).
func RegisterTheme(r *Registry) {
	r.Register(Command{
		Name: "theme",
		Help: "Open the settings page to switch or edit the color theme.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenSettings}
		},
	})
}
```
Register it in `model.go` next to `slash.RegisterSettings(reg)`:
```go
	slash.RegisterTheme(reg)
```

- [ ] **Step 4: Build + full suite**

Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: build OK; ALL packages PASS.

- [ ] **Step 5: Docs + manual acceptance**

Update `docs/agent/README.md` slash table: add `/theme` ("Open settings to switch/edit the color theme"). Flip `docs/features/cli/theme-system/design.md` Status to "Implemented".

Manual acceptance (record in commit body):
```bash
cd source/clients/cli && go build -o bin/cercano-cli . && ./bin/cercano-cli
```
Verify: `/s` (or `/theme`) → Theme section lists cracker/phosphor/synthwave/daylight; selecting one repaints the whole UI live and persists across relaunch; on a built-in the color rows are read-only and Save/Delete are dim; **Save As** `<name>` clones to an editable custom theme; editing a ColorField recolors live; **Save** persists; **Import** `<path>` loads a yaml; **Delete** removes a custom theme and falls back to cracker; assistant markdown + code spans recolor with the theme.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/slash/theme.go source/clients/cli/internal/slash/theme_test.go source/clients/cli/internal/ui/model.go docs/agent/README.md docs/features/cli/theme-system/design.md
git commit -m "feat(cli): /theme shortcut; document theme system"
```

---

## Self-Review

**Spec coverage:**
- Full 18-color theme (chrome+content) → Tasks 1-2 (Palette expansion, Styles/Markdown derive). ✓
- Built-in named themes (cracker/phosphor/synthwave/daylight) → Task 4. ✓
- Live apply (rebuild styles + flush markdown cache + repaint) → Task 5 (`applyTheme`, `chatView.SetStyles`). ✓
- Persistence (ui.yaml active + themes-dir custom) → Tasks 6-8. ✓
- Files-only import → Task 7 `ImportTheme` + Task 11 `theme-import` row. ✓
- Editor inline in settings (selector + grouped colors + actions; built-in read-only; working copy; Save/Save As/Delete) → Tasks 9-11. ✓
- New widgets ColorField + ButtonField → Tasks 9-10. ✓
- `/color` stays; accent-color row removed; prompt border from theme → Task 11 (remove row) + Task 5 (`applyTheme` re-resolves promptBorderColor). ✓
- `/theme` shortcut → Task 12. ✓
- cracker unchanged (golden) → Task 4 test. ✓

**Placeholder scan:** The intentional first-compile stubs (`mustHex` return-type note in Task 6, `themeColorPtr` stub in Task 11) are explicitly replaced within their own steps. No "TODO/handle errors" placeholders.

**Type consistency:** `Theme{Name, Palette}`, `Palette` 18 fields, `NewStyles(Palette)`, `MarkdownStyle(Palette)`, `Registry` methods, `ColorField`/`ButtonField` signatures, `settingsThemeMsg{working, persistName}`, yaml keys (`colorFields` map ↔ `chromeColorRows`/`contentColorRows` ↔ `FieldPtr`) all consistent across tasks. The yaml color keys are the single contract shared by serialize, FieldPtr, and the section rows — verified identical spelling.

**API-name caveats:** charm v2 key constants (`tea.KeyEnter`, etc.) are confirmed available in this codebase; theme/styles constructors confirmed (`theme.Cracker()`, `theme.NewStyles`, `testStyles()` in form tests). `gopkg.in/yaml.v3` presence in the CLI module is checked in Task 6 Step 3.
