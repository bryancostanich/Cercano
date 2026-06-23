# Cercano TUI → Charm v2 Migration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the cercano-cli TUI from Charm v1 to v2 (bubbletea, bubbles, lipgloss), idiomatically, and add four v2 features: real terminal cursor at the input, enhanced keyboard, mouse-wheel scroll, declarative screen control.

**Architecture:** The TUI has one root `tea.Model` (`internal/cli/ui/model.go`) plus several plain sub-structs (config editor, history picker, banner, overlay row-list) that the root drives manually. Only the root is registered with the Program, so only its `View()` changes signature; sub-structs keep returning `string`. The dependency bump is atomic — the whole module switches import paths at once, then compiles green.

**Tech Stack:** Go 1.26, `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`, `charm.land/bubbles/v2/key`.

## Global Constraints

- Work happens in worktree `Cercano-tui-v2` (branch `tui-charm-v2`), repo subdir `source/server`. All `go` commands run from `source/server`.
- Module paths (verbatim): `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`. If `go get` cannot resolve `charm.land/...`, fall back to `github.com/charmbracelet/<lib>/v2` and note it in the commit message.
- The full v2 upgrade guides are indexed in the context-mode knowledge base under sources `bubbletea-v2-upgrade`, `bubbles-v2-upgrade`, `lipgloss-v2-upgrade`. When any symbol fails to compile, `ctx_search` those sources before guessing.
- Build/test commands (run from `source/server`):
  - Build: `go build ./...`
  - Test: `go test ./... -count=1`
  - CLI subset test: `go test ./internal/cli/... -count=1`
- Do NOT `git push`. Commit locally per task.
- Behavior must stay identical through Task 2; new behavior is added only in Tasks 3–6.

---

## Reference: key v1→v2 mappings (apply throughout)

| v1 | v2 |
|----|----|
| `github.com/charmbracelet/bubbletea` (`tea`) | `charm.land/bubbletea/v2` (`tea`) |
| `github.com/charmbracelet/bubbles/textinput` | `charm.land/bubbles/v2/textinput` |
| `github.com/charmbracelet/bubbles/viewport` | `charm.land/bubbles/v2/viewport` |
| `github.com/charmbracelet/lipgloss` | `charm.land/lipgloss/v2` |
| `case tea.KeyMsg:` | `case tea.KeyPressMsg:` |
| `msg.Type` (e.g. `tea.KeyEnter`) | `msg.Code` |
| `tea.KeyMsg{Type: tea.KeyDown}` | `tea.KeyPressMsg{Code: tea.KeyDown}` |
| `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}` | `tea.KeyPressMsg{Code: ch, Text: string(ch)}` |
| `vp := viewport.New(80, 10)` | `vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))` |
| `vp.Width = w` / `vp.Height = h` | `vp.SetWidth(w)` / `vp.SetHeight(h)` |
| `vp.Width` / `vp.Height` (read) | `vp.Width()` / `vp.Height()` |
| `ti.Width = w` | `ti.SetWidth(w)` |
| `tea.NewProgram(m, tea.WithAltScreen())` | `tea.NewProgram(m)` + `v.AltScreen = true` in `View()` |
| `func (m Model) View() string` (root only) | `func (m Model) View() tea.View` |

`msg.String()` still returns the same tokens (`"ctrl+c"`, `"tab"`, `"enter"`, `"pgup"`, `"shift+up"`, …), so every `msg.String()` comparison is unchanged. `ti.Prompt` (string), `ti.Placeholder`, `ti.CharLimit`, `ti.Focus()`, `ti.Value()`, `ti.SetValue()`, `ti.CursorEnd()`, `textinput.New()`, `viewport.SetContent()`, `vp.AtBottom()`, `vp.GotoBottom()`, `vp.Update()`, `vp.View()`, `lipgloss.NewStyle()`, `lipgloss.Color`, `lipgloss.Width`, `lipgloss.JoinHorizontal`, `lipgloss.RoundedBorder` carry over unchanged. If any of these last items does NOT compile, `ctx_search` the relevant guide source.

---

### Task 1: Dependency bump + mechanical port (compile-clean, behavior-identical)

This is one atomic task: the module does not build until every Charm import is on v2. Edit in the order below, then gate on build+test. Commit once.

**Files:**
- Modify: `source/server/go.mod`, `source/server/go.sum` (via go tooling)
- Modify: `internal/cli/theme/palette.go`, `internal/cli/theme/styles.go`
- Modify: `internal/cli/banner/banner.go`, `internal/cli/banner/anim.go`
- Modify: `internal/cli/render/table.go`
- Modify: `internal/cli/ui/scrollback_tool.go`
- Modify: `internal/cli/overlay/rowlist.go`
- Modify: `internal/cli/ui/config_editor.go`, `internal/cli/ui/history_picker.go`
- Modify: `internal/cli/ui/model.go`
- Modify: `cmd/cercano/main.go`
- Test: `internal/cli/overlay/rowlist_test.go`

**Interfaces:**
- Produces: `func (m Model) View() tea.View` (root model), unchanged exported names everywhere else. Sub-struct `View()` methods still return `string`.

- [ ] **Step 1: Bump dependencies**

Run from `source/server`:
```bash
go get charm.land/bubbletea/v2@latest
go get charm.land/bubbles/v2@latest
go get charm.land/lipgloss/v2@latest
go mod tidy
```
Expected: go.mod now lists the three `charm.land/.../v2` modules. If `go get` errors on resolution, retry with `github.com/charmbracelet/bubbletea/v2@latest` (and the matching bubbles/lipgloss) and use those paths everywhere below.

- [ ] **Step 2: Update lipgloss-only files (imports only)**

In each of `theme/palette.go`, `theme/styles.go`, `banner/banner.go`, `render/table.go`, `ui/scrollback_tool.go`, replace:
```go
"github.com/charmbracelet/lipgloss"
```
with:
```go
"charm.land/lipgloss/v2"
```
No other changes — these files use only `lipgloss.NewStyle/Color/Width/JoinHorizontal/RoundedBorder/Border`, all source-compatible. Package name stays `lipgloss`.

- [ ] **Step 3: Update `banner/anim.go`**

Replace the bubbletea import:
```go
tea "github.com/charmbracelet/bubbletea"
```
with:
```go
tea "charm.land/bubbletea/v2"
```
`anim.go` uses `tea.Cmd`, `tea.Msg`, `tea.Tick` — all unchanged. No further edits.

- [ ] **Step 4: Update `overlay/rowlist.go`**

Replace imports:
```go
"github.com/charmbracelet/bubbles/textinput"
tea "github.com/charmbracelet/bubbletea"
"github.com/charmbracelet/lipgloss"
```
with:
```go
"charm.land/bubbles/v2/textinput"
tea "charm.land/bubbletea/v2"
"charm.land/lipgloss/v2"
```
Change the `Update` signature from `tea.KeyMsg` to `tea.KeyPressMsg`:
```go
func (r RowList) Update(msg tea.KeyPressMsg, styles theme.Styles) (RowList, tea.Cmd, bool) {
```
The body uses `msg.String()` only — leave it. The inline edit input (`textinput.New()`, `ti.Prompt`, `ti.CharLimit`, `ti.Focus()`, `ti.SetValue`, `ti.CursorEnd()`, `r.input.Update(msg)`, `r.input.View()`) is unchanged. `textinput.Blink` (returned on edit start) — if it no longer exists in v2, replace `return r, textinput.Blink, false` with `return r, r.input.Cursor().BlinkCmd(), false`; if `textinput.Blink` still exists, keep it. Verify at build.

- [ ] **Step 5: Update `ui/config_editor.go` and `ui/history_picker.go`**

Replace `tea "github.com/charmbracelet/bubbletea"` with `tea "charm.land/bubbletea/v2"` in both. Change both `Update` method signatures from `tea.KeyMsg` to `tea.KeyPressMsg`:
```go
func (ed configEditor) Update(msg tea.KeyPressMsg) (configEditor, tea.Cmd, bool) {
```
```go
func (h historyPicker) Update(msg tea.KeyPressMsg) (historyPicker, tea.Cmd, bool) {
```
They forward `msg` to `ed.list.Update(msg, ...)` / `h.list.Update(msg, ...)`, which now also take `tea.KeyPressMsg` — types line up. No other edits.

- [ ] **Step 6: Update `ui/model.go` imports**

Replace the import block:
```go
"github.com/charmbracelet/bubbles/textinput"
"github.com/charmbracelet/bubbles/viewport"
tea "github.com/charmbracelet/bubbletea"
"github.com/charmbracelet/lipgloss"
```
with:
```go
"charm.land/bubbles/v2/textinput"
"charm.land/bubbles/v2/viewport"
tea "charm.land/bubbletea/v2"
"charm.land/lipgloss/v2"
```

- [ ] **Step 7: `ui/model.go` — viewport constructor + field→method**

In `New()`, change:
```go
vp := viewport.New(80, 10)
```
to:
```go
vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
```
In `relayout()` change the writes:
```go
m.viewport.Width = contentW
m.viewport.Height = bodyH
m.input.Width = contentW - 4
```
to:
```go
m.viewport.SetWidth(contentW)
m.viewport.SetHeight(bodyH)
m.input.SetWidth(contentW - 4)
```
Change every viewport-dimension **read** to the getter form. Occurrences: `relayout()` guard `if m.viewport.Width > 0` → `if m.viewport.Width() > 0`; `renderEntry()` `wrapW := m.viewport.Width` → `wrapW := m.viewport.Width()`. Grep to confirm none remain:
```bash
grep -n "viewport.Width\b\|viewport.Height\b\|\.input\.Width\b" internal/cli/ui/model.go
```
Expected after edits: only method-call forms (`Width()`, `Height()`, `SetWidth`, `SetHeight`) remain.

- [ ] **Step 8: `ui/model.go` — key message type + `msg.Type` switch**

Change the Update case `case tea.KeyMsg:` to `case tea.KeyPressMsg:`.
In the tool-nav block, replace the `switch msg.Type { case tea.KeyUp: ... }` with `msg.Code` matching (idiomatic `key.Matches` comes in Task 2; here just make it compile with identical behavior):
```go
switch msg.Code {
case tea.KeyUp:
    // ... unchanged body ...
case tea.KeyDown:
    // ... unchanged body ...
case tea.KeyEnter, tea.KeyTab:
    // ... unchanged body ...
case tea.KeyEsc:
    // ... unchanged body ...
}
```
Replace the two standalone `msg.Type == tea.KeyEsc` checks with `msg.Code == tea.KeyEsc`. All `msg.String()` comparisons (ctrl+c, tab, enter, pgup, …) stay as-is.

- [ ] **Step 9: `ui/model.go` — `View()` returns `tea.View`**

Change the signature:
```go
func (m Model) View() tea.View {
```
At the two early `return ""` / final `return out` points, wrap in `tea.NewView`:
- Early guard: `return tea.NewView("")`
- Final: replace `return out` with:
```go
return tea.NewView(out)
```
Keep `tea.WithAltScreen()` in main.go for now (declarative move is Task 3). Leave the height-padding loop and the `tea.ClearScreen` on resize untouched (also Task 3).

- [ ] **Step 10: `cmd/cercano/main.go` — import only**

Replace `tea "github.com/charmbracelet/bubbletea"` with `tea "charm.land/bubbletea/v2"`. Leave `tea.NewProgram(m, tea.WithAltScreen())` for now.

- [ ] **Step 11: Fix `overlay/rowlist_test.go` key construction**

Replace the v1 import and key literals. Import:
```go
tea "charm.land/bubbletea/v2"
```
Replace each literal:
- `tea.KeyMsg{Type: tea.KeyDown}` → `tea.KeyPressMsg{Code: tea.KeyDown}`
- `tea.KeyMsg{Type: tea.KeyEnter}` → `tea.KeyPressMsg{Code: tea.KeyEnter}`
- `tea.KeyMsg{Type: tea.KeyEsc}` → `tea.KeyPressMsg{Code: tea.KeyEsc}`
- `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}` → `tea.KeyPressMsg{Code: ch, Text: string(ch)}`

- [ ] **Step 12: Build**

Run: `go build ./...` (from `source/server`)
Expected: clean build, exit 0. If a symbol fails, `ctx_search` the matching guide source and fix; do not invent APIs.

- [ ] **Step 13: Test**

Run: `go test ./... -count=1`
Expected: all packages PASS, including `internal/cli/overlay`, `internal/cli/ui`, `internal/cli/render`, `internal/cli/banner`.

- [ ] **Step 14: Commit**

```bash
git add -A
git commit -m "refactor(tui): mechanical port to Charm v2 (compile-clean, behavior-identical)"
```

---

### Task 2: Idiomatic key bindings

Replace the `msg.Code` switch and scattered nav-key string compares with a `key.Binding` set matched via `key.Matches`. Behavior identical.

**Files:**
- Create: `internal/cli/ui/keys.go`
- Modify: `internal/cli/ui/model.go` (tool-nav block, scrollback-scroll case)
- Modify: `internal/cli/overlay/rowlist.go` (nav switch)

**Interfaces:**
- Produces: `type keyMap struct { ... key.Binding }`, package-level `var keys = newKeyMap()` in `ui`. Consumed only within `ui`.

- [ ] **Step 1: Create `internal/cli/ui/keys.go`**

```go
package ui

import "charm.land/bubbles/v2/key"

// keyMap holds the cercano-cli key bindings matched via key.Matches in Update.
type keyMap struct {
	NavUp     key.Binding
	NavDown   key.Binding
	ToggleTool key.Binding
	Back      key.Binding
	ScrollKeys key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		NavUp:      key.NewBinding(key.WithKeys("up")),
		NavDown:    key.NewBinding(key.WithKeys("down")),
		ToggleTool: key.NewBinding(key.WithKeys("enter", "tab")),
		Back:       key.NewBinding(key.WithKeys("esc")),
		ScrollKeys: key.NewBinding(key.WithKeys(
			"pgup", "pgdown", "home", "end",
			"ctrl+u", "ctrl+d", "ctrl+b", "ctrl+f", "shift+up", "shift+down")),
	}
}

var keys = newKeyMap()
```

- [ ] **Step 2: Use `key.Matches` in `model.go` tool-nav block**

Replace the `switch msg.Code { case tea.KeyUp: ... }` block with:
```go
switch {
case key.Matches(msg, keys.NavUp):
    // ... unchanged up body ...
    return m, nil
case key.Matches(msg, keys.NavDown):
    // ... unchanged down body ...
    return m, nil
case key.Matches(msg, keys.ToggleTool):
    // ... unchanged toggle body ...
    return m, nil
case key.Matches(msg, keys.Back):
    m.focusedToolIdx = -1
    m.refreshViewport()
    return m, nil
}
```
Add the import `"charm.land/bubbles/v2/key"` to model.go. Replace the standalone `msg.Code == tea.KeyEsc && m.input.Value() == ""` check with `key.Matches(msg, keys.Back) && m.input.Value() == ""`.

- [ ] **Step 3: Use the scroll binding for the viewport-forward case**

Replace the `case "pgup", "pgdown", ...:` literal in the `switch key {` block with a guard above it:
```go
if key.Matches(msg, keys.ScrollKeys) {
    var cmd tea.Cmd
    m.viewport, cmd = m.viewport.Update(msg)
    return m, cmd
}
```
Place this immediately before `switch key {` and delete the old `case "pgup", ...:` arm. Keep the `enter` arm in the switch.

- [ ] **Step 4: `overlay/rowlist.go` — key.Matches for nav**

Add import `"charm.land/bubbles/v2/key"`. Define a package-level binding set at top of file:
```go
var rowKeys = struct{ Up, Down, Home, End, Enter, Close key.Binding }{
	Up:    key.NewBinding(key.WithKeys("up", "k")),
	Down:  key.NewBinding(key.WithKeys("down", "j")),
	Home:  key.NewBinding(key.WithKeys("home", "g")),
	End:   key.NewBinding(key.WithKeys("end", "G")),
	Enter: key.NewBinding(key.WithKeys("enter")),
	Close: key.NewBinding(key.WithKeys("esc", "q")),
}
```
In the non-editing `switch key {`, replace the string cases with a `switch { case key.Matches(msg, rowKeys.Close): ...; case key.Matches(msg, rowKeys.Up): ...; }` equivalent, preserving each body. Keep the editing-mode block (esc/enter) string-based — it's clearer and gated on `r.editing`.

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./... -count=1`
Expected: clean build, all PASS. `rowlist_test.go` exercises `KeyDown`/`KeyEnter`/`KeyEsc` and a rune — confirms key.Matches dispatch still works.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(tui): idiomatic key.Binding/key.Matches for nav keys"
```

---

### Task 3: Declarative screen control

Move alt-screen into the `View()` struct; drop `tea.WithAltScreen()`. Then test whether the v2 renderer makes the manual resize hacks unnecessary; remove them only if the smoke test stays clean.

**Files:**
- Modify: `cmd/cercano/main.go`
- Modify: `internal/cli/ui/model.go` (View; WindowSizeMsg ClearScreen; height-pad loop)

- [ ] **Step 1: Drop the program option**

In `main.go` change:
```go
p := tea.NewProgram(m, tea.WithAltScreen())
```
to:
```go
p := tea.NewProgram(m)
```

- [ ] **Step 2: Set AltScreen in `View()`**

In `model.go View()`, build the view via the longer form so fields can be set:
```go
v := tea.NewView(out)
v.AltScreen = true
return v
```
Apply to the final return. For the early-guard return use `tea.NewView("")` with `AltScreen` set too:
```go
if m.width == 0 || m.height == 0 {
    v := tea.NewView("")
    v.AltScreen = true
    return v
}
```

- [ ] **Step 3: Build + manual smoke (alt-screen entry/exit + resize)**

Run: `go build -o bin/cercano ./cmd/cercano/` then launch `bin/cercano` against a running agent (or `make dev`). Verify: app enters alt-screen, renders normally, **resize the terminal smaller then larger** — confirm no stale rows. Quit with double ctrl+c.
Expected: identical to v1. If resize shows stale rows, the manual hacks are still needed — keep them and skip Step 4.

- [ ] **Step 4: Remove manual hacks only if Step 3 was clean**

If and only if resize was artifact-free with `AltScreen` declarative:
- In `WindowSizeMsg`, change `return m, tea.ClearScreen` to `return m, nil` (the `tea.ClearScreen` command no longer exists in v2 anyway — if Step 1–3 build failed on it, this removal is mandatory, not optional). If `tea.ClearScreen` does not exist, you MUST remove it here regardless and rely on the declarative renderer.
- Re-run the resize smoke. If a regression appears, restore the height-padding loop in `View()` (the `if rendered < m.height` block) as the fallback and keep it; leave a comment noting why.

Note: the `View()` height-padding loop and `tea.ClearScreen` are independent. `tea.ClearScreen` may not exist in v2 — confirm at Task 1 build; if Task 1 failed to compile on it, it was already removed there and this step only covers the padding loop.

- [ ] **Step 5: Build + test + commit**

```bash
go build ./... && go test ./... -count=1
git add -A
git commit -m "feat(tui): declarative alt-screen via tea.View; drop WithAltScreen"
```

---

### Task 4: Enhanced keyboard

Request keyboard enhancements so modifier combos (shift+up/down, ctrl+arrows) report reliably.

**Files:**
- Modify: `internal/cli/ui/model.go` (View)

- [ ] **Step 1: Set the View field**

In `View()` after `v.AltScreen = true`, add:
```go
v.KeyboardEnhancements = tea.KeyboardEnhancements{}
```
If `tea.KeyboardEnhancements` is a function/option form rather than a struct (confirm via `ctx_search bubbletea-v2-upgrade "KeyboardEnhancements"`), use the documented constructor. The zero/default value requests the standard enhancement set.

- [ ] **Step 2: Build + manual smoke**

Run: `go build -o bin/cercano ./cmd/cercano/` and launch. With a few tool entries in scrollback, press `shift+up`/`shift+down` and confirm scrollback scrolls (these route through `keys.ScrollKeys`). Confirm normal typing, enter, esc still work.
Expected: shift+arrow scroll works reliably.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat(tui): request v2 keyboard enhancements"
```

---

### Task 5: Mouse wheel scrolls history

Enable mouse cell-motion and forward wheel messages to the chat viewport.

**Files:**
- Modify: `internal/cli/ui/model.go` (View; Update)

- [ ] **Step 1: Enable mouse in View**

In `View()` add:
```go
v.MouseMode = tea.MouseModeCellMotion
```

- [ ] **Step 2: Forward wheel messages to the viewport in Update**

Add a case to the `Update` type switch (only when no overlay is active and the chat is visible):
```go
case tea.MouseWheelMsg:
    if m.editorActive || m.historyActive {
        return m, nil
    }
    var cmd tea.Cmd
    m.viewport, cmd = m.viewport.Update(msg)
    return m, cmd
```
If the v2 mouse message type is named differently (e.g. `tea.MouseMsg` interface with a wheel variant), `ctx_search bubbletea-v2-upgrade "mouse wheel message type"` and use the exact type. The viewport's own `Update` consumes wheel events to scroll.

- [ ] **Step 3: Build + manual smoke**

Run: `go build -o bin/cercano ./cmd/cercano/` and launch. Fill scrollback (ask the agent something long, or `/help`), then scroll the mouse wheel up/down over the chat area. Confirm the viewport pages. Confirm wheel does nothing destructive while `/config` is open.
Expected: wheel scrolls chat history.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat(tui): mouse wheel scrolls chat history"
```

---

### Task 6: Real terminal cursor at the input

Switch the main input off the simulated/virtual cursor and drive the real terminal cursor via `View.Cursor`, positioned at the input caret's absolute screen coordinates.

**Files:**
- Modify: `internal/cli/ui/model.go` (New; View)
- Create: `internal/cli/ui/cursor.go` (absolute-position helper + test target)
- Test: `internal/cli/ui/cursor_test.go`

**Interfaces:**
- Produces: `func inputCursorRow(parts []string, inputIdx int) int` — sum of line counts of `parts[:inputIdx]`, giving the 0-based screen row where the input line begins.

- [ ] **Step 1: Write the failing test**

`internal/cli/ui/cursor_test.go`:
```go
package ui

import "testing"

func TestInputCursorRow(t *testing.T) {
	parts := []string{
		"header",            // 1 line  → rows 0
		"────────",          // 1 line  → row 1
		"line a\nline b",    // 2 lines → rows 2-3
		"viewport\nrow\nrow",// 3 lines → rows 4-6
		"input here",        // input   → row 7
	}
	got := inputCursorRow(parts, 4)
	if got != 7 {
		t.Fatalf("inputCursorRow = %d, want 7", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ui/ -run TestInputCursorRow -v`
Expected: FAIL — `undefined: inputCursorRow`.

- [ ] **Step 3: Implement the helper**

`internal/cli/ui/cursor.go`:
```go
package ui

import "strings"

// inputCursorRow returns the 0-based screen row at which parts[inputIdx]
// begins, accounting for embedded newlines in earlier parts. Used to place
// the real terminal cursor at the input line.
func inputCursorRow(parts []string, inputIdx int) int {
	row := 0
	for i := 0; i < inputIdx && i < len(parts); i++ {
		row += strings.Count(parts[i], "\n") + 1
	}
	return row
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ui/ -run TestInputCursorRow -v`
Expected: PASS.

- [ ] **Step 5: Turn off the virtual cursor for the main input**

In `New()`, after `ti.Focus()`, add:
```go
ti.SetVirtualCursor(false)
```
This tells textinput to expose a real `*tea.Cursor` via `ti.Cursor()` instead of drawing a glyph into its `View()` string. If `SetVirtualCursor` is not the exact name, `ctx_search bubbles-v2-upgrade "VirtualCursor"` (guide shows `ta.VirtualCursor` bool and `SetVirtualCursor`).

- [ ] **Step 6: Place the real cursor in `View()`**

Track the input's index in `parts` while building the view, then set `v.Cursor`. Where the input is appended (`parts = append(parts, m.input.View())`), capture the index first:
```go
inputIdx := len(parts)
parts = append(parts, m.input.View())
```
After `out := strings.Join(parts, "\n")` and after setting AltScreen/Mouse/Keyboard fields, add — only when the chat input owns focus (no overlay, no pending confirm):
```go
if !m.editorActive && !m.historyActive && m.pendingConfirm == nil {
    if c := m.input.Cursor(); c != nil {
        c.Y += inputCursorRow(parts, inputIdx)
        v.Cursor = c
    }
} else {
    v.Cursor = nil // overlays manage their own (virtual) cursor
}
```
`m.input.Cursor()` returns a `*tea.Cursor` with `X` already at the caret column within the input line (prompt width + value offset) and `Y == 0` relative to the input; adding `inputCursorRow` lifts it to absolute screen Y. If the cursor `X` does not already include the prompt width, add `lipgloss.Width(m.input.Prompt)` — verify visually in Step 7.

- [ ] **Step 7: Build + manual smoke**

Run: `go build -o bin/cercano ./cmd/cercano/` and launch. Confirm: a real blinking block cursor sits at the end of typed input, moves with left/right arrows and as you type, and sits correctly after the `▶ ` prompt. Open `/config` — confirm the chat cursor disappears and the overlay's own edit cursor behaves. Close it — chat cursor returns. Toggle the splash off by submitting a line and confirm the cursor tracks the input as the layout shifts.
Expected: native cursor tracks the caret in all layouts.

- [ ] **Step 8: Test + commit**

```bash
go test ./... -count=1
git add -A
git commit -m "feat(tui): real terminal cursor at the input box"
```

---

### Task 7: Full smoke pass + cleanup

**Files:**
- Modify: any file with now-dead code surfaced during the migration (e.g. unused `countLines`, `QuitAfter` if the linter flags them — only remove if genuinely unused and unexported, or already unused before this work; do NOT remove exported API without checking callers).

- [ ] **Step 1: Full build + vet + test**

Run:
```bash
go build ./... && go vet ./... && go test ./... -count=1
```
Expected: clean across the board.

- [ ] **Step 2: Manual smoke checklist**

Launch `bin/cercano` against a running agent and verify each:
- Splash banner renders and animates; dismisses on first submit.
- Type a message, submit — streaming spinner + lime sweep animate, tokens fill in.
- Slash completion: type `/he` → suggestion strip; `tab` completes.
- `/config` opens, arrow-navigate, edit a field, esc closes; header model names refresh.
- `/history` opens, select a conversation, scrollback rehydrates.
- Tool confirm: trigger a W/X tool, see y/n/d prompt; `d` shows args; `y` runs; `n` cancels.
- ctrl+c on non-empty input clears it; ctrl+c on empty arms; second ctrl+c quits.
- Resize smaller/larger — no stale rows.
- Mouse wheel scrolls chat history.
- shift+up/down scrolls history.
- Real cursor blinks at the input and tracks the caret.

- [ ] **Step 3: Final commit (if any cleanup)**

```bash
git add -A
git commit -m "chore(tui): cleanup + full v2 smoke pass"
```

- [ ] **Step 4: Report**

Summarize: confirm `go test ./...` green, list the four features working, note whether the resize hacks were kept or removed, and surface any symbol that differed from this plan (with the v2 name actually used). Do NOT push — leave the branch `tui-charm-v2` for the user to review and merge.

---

## Self-Review notes

- **Spec coverage:** module paths (Task 1), KeyPressMsg (T1), View→tea.View (T1), lipgloss/bubbles field changes (T1), idiomatic key.Matches (T2), declarative screen (T3), enhanced keyboard (T4), mouse wheel (T5), real cursor (T6), testing + smoke (T7). All spec sections map to a task.
- **Uncertain symbols flagged inline** (verify-at-build, with the guide source to search): `textinput.Blink` vs `Cursor().BlinkCmd()` (T1 S4), `tea.ClearScreen` existence (T3 S4), `tea.KeyboardEnhancements` shape (T4 S1), v2 mouse wheel message type name (T5 S2), `SetVirtualCursor` name + cursor X/prompt offset (T6 S5–6). Each has a concrete fallback and a `ctx_search` pointer — not open-ended.
- **Type consistency:** `inputCursorRow(parts []string, inputIdx int) int` used identically in T6 S1/S3/S6; `keyMap`/`keys` defined T2 S1 and used T2 S2–3.
