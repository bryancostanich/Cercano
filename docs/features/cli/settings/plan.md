# Settings Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat `/config` editor with a richer, sectioned settings page (opened by `/s`, `/settings`, `/config`) built on a new extensible form-field widget package.

**Architecture:** A new agent-free `internal/form` package provides a `Field` interface, concrete widgets (text, masked, select, toggle, read-only), and a `Form` that groups fields into titled sections with nav + commit routing. A new `settingsPage` content page (`internal/ui/settings_page.go`) builds the form from `GetConfig` + `GetPermissionMode` + the root model's accent color, routes commits to the right sink (config RPC / permission RPC / local color msg), and implements the existing `contentPage` + `contentPageScroller` interfaces. Slash routing and the root model are rewired; the old `config_editor.go` is removed.

**Tech Stack:** Go 1.21+, Bubble Tea v2 (`charm.land/bubbletea/v2`), Bubbles v2 (`charm.land/bubbles/v2/{textinput,key}`), Lipgloss v2 (`charm.land/lipgloss/v2`), `cercano/source/server/pkg/agentclient`.

## Global Constraints

- Module: all new CLI code lives under `cercano/source/clients/cli` (its own Go module). Run tests with `cd source/clients/cli && go test ./... -count=1`.
- Charm libraries are v2: import `tea "charm.land/bubbletea/v2"`, `"charm.land/bubbles/v2/textinput"`, `"charm.land/bubbles/v2/key"`, `"charm.land/lipgloss/v2"`.
- `internal/form` MUST NOT import `internal/ui` (would create an import cycle — `ui` imports `form`). It may import `internal/theme` only.
- Algorithmic only: no LLM calls anywhere in this feature.
- The page never mutates root `Model` state directly; cross-page effects are emitted as `tea.Cmd` → messages the root model handles.
- Reuse existing RPCs unchanged: `agentclient.Client.GetConfig(ctx) (*Config, error)`, `UpdateConfig(ctx, ConfigUpdate) (string, error)`, `GetPermissionMode(ctx) (string, error)`, `SetPermissionMode(ctx, mode) error`.
- Keep the existing `/config key value`, `/config show`, `/cloud`, `/color`, `/strict`, `/permissive`, `/bypass`, `/locus` commands working (the page is an additional unified surface; `/color #RRGGBB` remains the hex escape hatch for accent color).

---

### Task 1: `form` package — `Field` interface + `ReadOnlyField`

**Files:**
- Create: `source/clients/cli/internal/form/field.go`
- Create: `source/clients/cli/internal/form/readonly_field.go`
- Test: `source/clients/cli/internal/form/readonly_field_test.go`

**Interfaces:**
- Produces: the `Field` interface and `*ReadOnlyField` (constructor `NewReadOnly(key, label, value, hint string) *ReadOnlyField`). All later widgets implement `Field`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/readonly_field_test.go`:
```go
package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestReadOnlyFieldBasics(t *testing.T) {
	f := NewReadOnly("port", "port", "50052", "(read-only)")
	if f.Key() != "port" {
		t.Fatalf("Key() = %q, want port", f.Key())
	}
	if f.Label() != "port" {
		t.Fatalf("Label() = %q, want port", f.Label())
	}
	if f.Display() != "50052" {
		t.Fatalf("Display() = %q, want 50052", f.Display())
	}
	if f.Editing() {
		t.Fatal("ReadOnly field must never report Editing")
	}
	cmd, committed, val := f.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil || committed || val != "" {
		t.Fatalf("ReadOnly Update must be inert, got cmd=%v committed=%v val=%q", cmd, committed, val)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestReadOnlyFieldBasics -v`
Expected: FAIL — package/`NewReadOnly` undefined (build error).

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/field.go`:
```go
// Package form provides composable settings-form widgets for the cercano-cli
// TUI. A Field is one navigable (and optionally editable) control; a Form
// groups Fields into titled sections with nav and commit routing. The package
// is agent-free and depends only on theme + charm libraries — it MUST NOT
// import internal/ui (that would create an import cycle).
package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Field is one settings control.
type Field interface {
	Key() string     // opaque id forwarded to the commit hook
	Label() string   // left-column label
	Display() string // value shown when the field is not being edited
	Editing() bool   // true while the widget owns keystrokes (inline edit / open picker)

	// Update routes a key event. committed=true means the user accepted a new
	// value (carried in value); the Form then calls its commit hook. A field
	// that is not Editing() interprets enter/space as "activate" (begin edit /
	// open picker / toggle).
	Update(msg tea.KeyPressMsg) (cmd tea.Cmd, committed bool, value string)

	// View renders the field's value cell (label is rendered by the Form).
	View(focused bool, width int, s theme.Styles) string
}
```

`source/clients/cli/internal/form/readonly_field.go`:
```go
package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// ReadOnlyField displays a value that cannot be edited.
type ReadOnlyField struct {
	key, label, value, hint string
}

// NewReadOnly builds a read-only field.
func NewReadOnly(key, label, value, hint string) *ReadOnlyField {
	return &ReadOnlyField{key: key, label: label, value: value, hint: hint}
}

func (f *ReadOnlyField) Key() string                            { return f.key }
func (f *ReadOnlyField) Label() string                          { return f.label }
func (f *ReadOnlyField) Display() string                        { return f.value }
func (f *ReadOnlyField) Editing() bool                          { return false }
func (f *ReadOnlyField) Update(tea.KeyPressMsg) (tea.Cmd, bool, string) {
	return nil, false, ""
}

func (f *ReadOnlyField) View(focused bool, width int, s theme.Styles) string {
	val := f.value
	if val == "" {
		val = s.Dim.Render("(unset)")
	} else {
		val = s.Muted.Render(val)
	}
	if f.hint != "" {
		val += s.BorderDim.Render("  " + f.hint)
	}
	return val
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestReadOnlyFieldBasics -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/field.go source/clients/cli/internal/form/readonly_field.go source/clients/cli/internal/form/readonly_field_test.go
git commit -m "feat(cli/form): Field interface + ReadOnlyField"
```

---

### Task 2: `TextField` (+ masked variant)

**Files:**
- Create: `source/clients/cli/internal/form/text_field.go`
- Test: `source/clients/cli/internal/form/text_field_test.go`

**Interfaces:**
- Consumes: `Field` (Task 1).
- Produces: `NewText(key, label, value, hint string) *TextField` and `NewMasked(key, label string, set bool) *TextField`. Masked fields start blank on edit and Display `(set)`/`(unset)`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/text_field_test.go`:
```go
package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func enter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func esc() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func typ(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestTextFieldEditCommit(t *testing.T) {
	f := NewText("ollama-url", "ollama-url", "http://old", "")
	// Enter activates edit mode; not yet committed.
	_, committed, _ := f.Update(enter())
	if !f.Editing() || committed {
		t.Fatalf("first enter should begin editing, not commit (editing=%v committed=%v)", f.Editing(), committed)
	}
	for _, r := range "x" {
		f.Update(typ(r))
	}
	// Second enter commits the edited value.
	_, committed, val := f.Update(enter())
	if !committed {
		t.Fatal("second enter should commit")
	}
	if f.Editing() {
		t.Fatal("commit should exit editing")
	}
	if val == "" {
		t.Fatalf("committed value should be non-empty, got %q", val)
	}
}

func TestTextFieldEscCancels(t *testing.T) {
	f := NewText("k", "k", "orig", "")
	f.Update(enter())
	_, committed, _ := f.Update(esc())
	if committed || f.Editing() {
		t.Fatalf("esc should cancel without commit (committed=%v editing=%v)", committed, f.Editing())
	}
	if f.Display() != "orig" {
		t.Fatalf("Display after cancel = %q, want orig", f.Display())
	}
}

func TestMaskedFieldDisplayAndBlankOnEdit(t *testing.T) {
	f := NewMasked("cloud-api-key", "cloud-api-key", true)
	if f.Display() != "(set)" {
		t.Fatalf("masked Display = %q, want (set)", f.Display())
	}
	f.Update(enter()) // begin edit — input must start blank
	if got := f.currentInput(); got != "" {
		t.Fatalf("masked edit should start blank, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run 'TestTextField|TestMasked' -v`
Expected: FAIL — `NewText`/`NewMasked`/`currentInput` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/text_field.go`:
```go
package form

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// TextField is an inline free-text editor. With masked=true it behaves as a
// secret: Display shows (set)/(unset) and edit starts blank.
type TextField struct {
	key, label, value, hint string
	masked                  bool
	set                     bool // for masked: whether a value is currently set
	editing                 bool
	input                   textinput.Model
}

// NewText builds a free-text field.
func NewText(key, label, value, hint string) *TextField {
	return &TextField{key: key, label: label, value: value, hint: hint}
}

// NewMasked builds a secret field. `set` reports whether a value already exists.
func NewMasked(key, label string, set bool) *TextField {
	return &TextField{key: key, label: label, masked: true, set: set}
}

func (f *TextField) Key() string     { return f.key }
func (f *TextField) Label() string   { return f.label }
func (f *TextField) Editing() bool   { return f.editing }

func (f *TextField) Display() string {
	if f.masked {
		if f.set {
			return "(set)"
		}
		return ""
	}
	return f.value
}

// currentInput exposes the in-progress edit buffer for tests.
func (f *TextField) currentInput() string { return f.input.Value() }

func (f *TextField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if !f.editing {
		switch msg.Code {
		case tea.KeyEnter:
			ti := textinput.New()
			ti.CharLimit = 0
			cmd := ti.Focus()
			if !f.masked {
				ti.SetValue(f.value)
				ti.CursorEnd()
			}
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
		val := f.input.Value()
		f.editing = false
		if f.masked {
			f.set = val != ""
		} else {
			f.value = val
		}
		return nil, true, val
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd, false, ""
}

func (f *TextField) View(focused bool, width int, s theme.Styles) string {
	if f.editing {
		return f.input.View()
	}
	d := f.Display()
	var cell string
	if d == "" {
		cell = s.Dim.Render("(unset)")
	} else {
		cell = s.Primary.Render(d)
	}
	if f.hint != "" {
		cell += s.BorderDim.Render("  " + f.hint)
	}
	return cell
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run 'TestTextField|TestMasked' -v`
Expected: PASS.

> Note: if `tea.KeyPressMsg`/`tea.KeyEnter`/`tea.KeyEscape` field/constant names differ in the installed v2 API, mirror exactly how `internal/overlay/rowlist.go` and `internal/ui/*` read keys (they use `msg.String()` and `key.Matches`). If so, switch the `msg.Code` comparisons to `msg.String() == "enter"` / `"esc"` and update the test helpers to `tea.KeyPressMsg` built the same way the existing `*_test.go` files build them.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/text_field.go source/clients/cli/internal/form/text_field_test.go
git commit -m "feat(cli/form): TextField + masked variant"
```

---

### Task 3: `SelectField` (inline picker)

**Files:**
- Create: `source/clients/cli/internal/form/select_field.go`
- Test: `source/clients/cli/internal/form/select_field_test.go`

**Interfaces:**
- Consumes: `Field` (Task 1).
- Produces: `Option{Label, Value string}`, `NewSelect(key, label string, options []Option, currentValue string) *SelectField`. Display shows the current option's Label; committed value is the chosen option's `Value`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/select_field_test.go`:
```go
package form

import "testing"

func down() (m struct{}) { return } // placeholder to keep imports tidy

func TestSelectFieldPick(t *testing.T) {
	opts := []Option{{Label: "strict", Value: "strict"}, {Label: "permissive", Value: "permissive"}, {Label: "bypass", Value: "bypass"}}
	f := NewSelect("permission-mode", "permission-mode", opts, "permissive")
	if f.Display() != "permissive" {
		t.Fatalf("Display = %q, want permissive", f.Display())
	}
	// enter opens the picker (cursor on current = index 1).
	_, committed, _ := f.Update(enter())
	if !f.Editing() || committed {
		t.Fatalf("enter should open picker without commit (editing=%v committed=%v)", f.Editing(), committed)
	}
	// move down to bypass and commit.
	f.Update(arrowDown())
	_, committed, val := f.Update(enter())
	if !committed || val != "bypass" {
		t.Fatalf("commit val = %q committed=%v, want bypass/true", val, committed)
	}
	if f.Editing() {
		t.Fatal("commit should close the picker")
	}
	if f.Display() != "bypass" {
		t.Fatalf("Display after commit = %q, want bypass", f.Display())
	}
}

func TestSelectFieldEscCancels(t *testing.T) {
	opts := []Option{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}}
	f := NewSelect("k", "k", opts, "a")
	f.Update(enter())
	f.Update(arrowDown())
	_, committed, _ := f.Update(esc())
	if committed || f.Editing() {
		t.Fatalf("esc should cancel (committed=%v editing=%v)", committed, f.Editing())
	}
	if f.Display() != "a" {
		t.Fatalf("Display after cancel = %q, want a", f.Display())
	}
}
```

Add this key helper to `text_field_test.go` (same package) so both files share it:
```go
func arrowDown() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }
```
And delete the placeholder `down()` stub above once `arrowDown` exists. (It is only present to make the first compile attempt fail loudly on missing symbols; remove it in Step 3.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestSelectField -v`
Expected: FAIL — `NewSelect`/`Option`/`arrowDown` undefined.

- [ ] **Step 3: Write minimal implementation**

Remove the `down()` placeholder from `select_field_test.go`. Then create `source/clients/cli/internal/form/select_field.go`:
```go
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Option is one selectable value. Label is shown; Value is committed.
type Option struct {
	Label string
	Value string
}

// SelectField is an enum chooser with an inline picker.
type SelectField struct {
	key, label string
	options    []Option
	current    int // index of the committed value (-1 if none matched)
	open       bool
	cursor     int // highlighted option while open
}

// NewSelect builds a select field. currentValue selects the initial option.
func NewSelect(key, label string, options []Option, currentValue string) *SelectField {
	cur := -1
	for i, o := range options {
		if o.Value == currentValue {
			cur = i
			break
		}
	}
	return &SelectField{key: key, label: label, options: options, current: cur}
}

func (f *SelectField) Key() string   { return f.key }
func (f *SelectField) Label() string { return f.label }
func (f *SelectField) Editing() bool { return f.open }

func (f *SelectField) Display() string {
	if f.current < 0 || f.current >= len(f.options) {
		return ""
	}
	return f.options[f.current].Label
}

func (f *SelectField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if !f.open {
		if msg.Code == tea.KeyEnter {
			f.open = true
			f.cursor = f.current
			if f.cursor < 0 {
				f.cursor = 0
			}
		}
		return nil, false, ""
	}
	switch msg.Code {
	case tea.KeyEscape:
		f.open = false
		return nil, false, ""
	case tea.KeyUp:
		if f.cursor > 0 {
			f.cursor--
		}
	case tea.KeyDown:
		if f.cursor < len(f.options)-1 {
			f.cursor++
		}
	case tea.KeyEnter:
		f.current = f.cursor
		f.open = false
		return nil, true, f.options[f.current].Value
	}
	return nil, false, ""
}

func (f *SelectField) View(focused bool, width int, s theme.Styles) string {
	if !f.open {
		d := f.Display()
		if d == "" {
			return s.Dim.Render("(unset)")
		}
		return s.Primary.Render(d)
	}
	var b strings.Builder
	for i, o := range f.options {
		if i == f.cursor {
			b.WriteString(s.Accent.Render("‹" + o.Label + "›"))
		} else {
			b.WriteString(s.Muted.Render(o.Label))
		}
		if i < len(f.options)-1 {
			b.WriteString(s.BorderDim.Render(" · "))
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestSelectField -v`
Expected: PASS.

> Same API caveat as Task 2: if `tea.KeyUp`/`KeyDown`/`KeyEnter`/`KeyEscape` constants differ, switch to `msg.String()` comparisons (`"up"`,`"down"`,`"enter"`,`"esc"`) as `rowlist.go` does.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/select_field.go source/clients/cli/internal/form/select_field_test.go source/clients/cli/internal/form/text_field_test.go
git commit -m "feat(cli/form): SelectField with inline picker"
```

---

### Task 4: `ToggleField`

**Files:**
- Create: `source/clients/cli/internal/form/toggle_field.go`
- Test: `source/clients/cli/internal/form/toggle_field_test.go`

**Interfaces:**
- Consumes: `Field` (Task 1).
- Produces: `NewToggle(key, label string, on bool) *ToggleField`. Commits `"true"`/`"false"`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/toggle_field_test.go`:
```go
package form

import "testing"

func TestToggleFieldFlips(t *testing.T) {
	f := NewToggle("flag", "flag", false)
	if f.Display() != "off" {
		t.Fatalf("Display = %q, want off", f.Display())
	}
	_, committed, val := f.Update(enter())
	if !committed || val != "true" {
		t.Fatalf("enter should flip+commit to true, got committed=%v val=%q", committed, val)
	}
	if f.Display() != "on" {
		t.Fatalf("Display after flip = %q, want on", f.Display())
	}
	if f.Editing() {
		t.Fatal("toggle never stays in editing mode")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestToggleField -v`
Expected: FAIL — `NewToggle` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/toggle_field.go`:
```go
package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// ToggleField is a boolean control flipped with enter/space.
type ToggleField struct {
	key, label string
	on         bool
}

// NewToggle builds a boolean field.
func NewToggle(key, label string, on bool) *ToggleField {
	return &ToggleField{key: key, label: label, on: on}
}

func (f *ToggleField) Key() string   { return f.key }
func (f *ToggleField) Label() string { return f.label }
func (f *ToggleField) Editing() bool { return false }

func (f *ToggleField) Display() string {
	if f.on {
		return "on"
	}
	return "off"
}

func (f *ToggleField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if msg.Code == tea.KeyEnter || msg.String() == " " {
		f.on = !f.on
		if f.on {
			return nil, true, "true"
		}
		return nil, true, "false"
	}
	return nil, false, ""
}

func (f *ToggleField) View(focused bool, width int, s theme.Styles) string {
	if f.on {
		return s.Accent.Render("on")
	}
	return s.Muted.Render("off")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestToggleField -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/toggle_field.go source/clients/cli/internal/form/toggle_field_test.go
git commit -m "feat(cli/form): ToggleField"
```

---

### Task 5: `Form` — sections, navigation, commit routing, render

**Files:**
- Create: `source/clients/cli/internal/form/form.go`
- Test: `source/clients/cli/internal/form/form_test.go`

**Interfaces:**
- Consumes: `Field` and all widgets (Tasks 1-4).
- Produces:
  - `Section{Title string; Fields []Field}`
  - `Form` with exported fields `Sections []Section`, `OnCommit func(key, value string) (status string, cmd tea.Cmd, err error)`, `OnReload func() []Section`.
  - `New(sections []Section) *Form`
  - `(*Form) Update(msg tea.KeyPressMsg) (cmd tea.Cmd, closed bool)`
  - `(*Form) View(width int, palette theme.Palette, styles theme.Styles) string`
  - `(*Form) Lines(width int, palette theme.Palette, styles theme.Styles) []string` (View split on "\n", for the page's scroller)
  - `(*Form) Cursor() int` (test seam)

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/form/form_test.go`:
```go
package form

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func testStyles() (theme.Palette, theme.Styles) {
	p := theme.NewCrackerPalette()
	return p, theme.NewStyles(p)
}

func TestFormNavSkipsHeadersAndClamps(t *testing.T) {
	sections := []Section{
		{Title: "A", Fields: []Field{NewText("a1", "a1", "v", ""), NewReadOnly("a2", "a2", "v", "")}},
		{Title: "B", Fields: []Field{NewText("b1", "b1", "v", "")}},
	}
	f := New(sections)
	if f.Cursor() != 0 {
		t.Fatalf("initial cursor = %d, want 0", f.Cursor())
	}
	f.Update(arrowDown())
	f.Update(arrowDown())
	if f.Cursor() != 2 {
		t.Fatalf("cursor after 2 downs = %d, want 2 (3 flat fields)", f.Cursor())
	}
	f.Update(arrowDown()) // clamp at last
	if f.Cursor() != 2 {
		t.Fatalf("cursor should clamp at 2, got %d", f.Cursor())
	}
}

func TestFormCommitRoutesToHook(t *testing.T) {
	var gotKey, gotVal string
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a1", "a1", "old", "")}}})
	f.OnCommit = func(key, value string) (string, tea.Cmd, error) {
		gotKey, gotVal = key, value
		return "saved", nil, nil
	}
	f.Update(enter()) // begin edit
	for _, r := range "new" {
		f.Update(typ(r))
	}
	f.Update(enter()) // commit -> OnCommit
	if gotKey != "a1" || gotVal != "new" {
		t.Fatalf("OnCommit got key=%q val=%q, want a1/new", gotKey, gotVal)
	}
}

func TestFormEscClosesWhenNotEditing(t *testing.T) {
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a1", "a1", "v", "")}}})
	_, closed := f.Update(esc())
	if !closed {
		t.Fatal("esc with no field editing should close the form")
	}
}

func TestFormViewRendersSectionTitles(t *testing.T) {
	p, s := testStyles()
	f := New([]Section{{Title: "Cloud", Fields: []Field{NewText("cloud-model", "cloud-model", "x", "")}}})
	out := f.View(80, p, s)
	if !strings.Contains(out, "Cloud") || !strings.Contains(out, "cloud-model") {
		t.Fatalf("View missing section title or field label:\n%s", out)
	}
}
```

> Before running, confirm the exact palette/styles constructor names by grepping `internal/theme` (`grep -n "func New" source/clients/cli/internal/theme/*.go`). Replace `NewCrackerPalette`/`NewStyles` with the real names if they differ (other `internal/ui/*_test.go` files already build a palette+styles — copy their construction).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/form/ -run TestForm -v`
Expected: FAIL — `New`/`Section`/`View`/`Cursor` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/form/form.go`:
```go
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Section is a titled group of fields.
type Section struct {
	Title  string
	Fields []Field
}

// Form arranges sections of fields with nav + commit routing.
type Form struct {
	Sections []Section
	// OnCommit fires when a field commits a new value. Optional.
	OnCommit func(key, value string) (status string, cmd tea.Cmd, err error)
	// OnReload returns a fresh section snapshot after a successful commit. Optional.
	OnReload func() []Section

	cursor int // index into the flattened field list
	status string
}

// New builds a form positioned on the first focusable field.
func New(sections []Section) *Form { return &Form{Sections: sections} }

// Cursor returns the flattened-field index of the focused field.
func (f *Form) Cursor() int { return f.cursor }

func (f *Form) flat() []Field {
	var out []Field
	for _, sec := range f.Sections {
		out = append(out, sec.Fields...)
	}
	return out
}

// Update routes a key event. Returns (cmd, closed). closed=true means the host
// should dismiss the page.
func (f *Form) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	fields := f.flat()
	if len(fields) == 0 {
		if msg.String() == "esc" || msg.String() == "q" {
			return nil, true
		}
		return nil, false
	}
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	cur := fields[f.cursor]

	if cur.Editing() {
		cmd, committed, val := cur.Update(msg)
		if committed {
			return f.commit(cur.Key(), val, cmd), false
		}
		return cmd, false
	}

	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "up", "k":
		if f.cursor > 0 {
			f.cursor--
		}
	case "down", "j":
		if f.cursor < len(fields)-1 {
			f.cursor++
		}
	case "home", "g":
		f.cursor = 0
	case "end", "G":
		f.cursor = len(fields) - 1
	default:
		// Forward activation keys (enter/space) to the field.
		cmd, committed, val := cur.Update(msg)
		if committed {
			return f.commit(cur.Key(), val, cmd), false
		}
		return cmd, false
	}
	return nil, false
}

func (f *Form) commit(key, val string, fieldCmd tea.Cmd) tea.Cmd {
	if f.OnCommit == nil {
		f.status = "no commit handler"
		return fieldCmd
	}
	status, cmd, err := f.OnCommit(key, val)
	if err != nil {
		f.status = "save failed: " + err.Error()
		return fieldCmd
	}
	f.status = status
	if f.OnReload != nil {
		f.Sections = f.OnReload()
	}
	return tea.Batch(fieldCmd, cmd)
}

// Lines renders the form as individual lines (for scroller hosts).
func (f *Form) Lines(width int, palette theme.Palette, styles theme.Styles) []string {
	return strings.Split(f.View(width, palette, styles), "\n")
}

// View renders all sections as titled panels plus a footer.
func (f *Form) View(width int, palette theme.Palette, styles theme.Styles) string {
	panelW := width - 6
	if panelW < 40 {
		panelW = 40
	}
	labelW := 0
	for _, sec := range f.Sections {
		for _, fld := range sec.Fields {
			if lw := lipgloss.Width(fld.Label()); lw > labelW {
				labelW = lw
			}
		}
	}

	var out strings.Builder
	idx := 0
	for _, sec := range f.Sections {
		var body strings.Builder
		title := styles.Accent.Render("─ " + sec.Title + " ")
		body.WriteString(title + styles.BorderDim.Render(strings.Repeat("─", panelW-lipgloss.Width(title))) + "\n\n")
		for _, fld := range sec.Fields {
			focused := idx == f.cursor
			marker := "   "
			if focused {
				marker = styles.Accent.Render(" ▶ ")
			}
			label := styles.Muted.Render(fld.Label())
			if focused && !fld.Editing() {
				label = styles.Bright.Render(fld.Label())
			}
			pad := strings.Repeat(" ", labelW-lipgloss.Width(fld.Label())+2)
			body.WriteString(marker + label + pad + fld.View(focused, panelW, styles) + "\n")
			idx++
		}
		out.WriteString(lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Padding(0, 1).
			Width(panelW).
			Render(body.String()))
		out.WriteString("\n")
	}

	footer := styles.Muted.Render("   ↑↓ navigate  enter edit/select  esc close")
	if f.status != "" {
		footer += styles.BorderDim.Render("   ·   ") + styles.Accent.Render(f.status)
	}
	out.WriteString(footer)
	return out.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/form/ -v`
Expected: PASS (all form tests).

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/form/form.go source/clients/cli/internal/form/form_test.go
git commit -m "feat(cli/form): Form with sections, nav, commit routing, render"
```

---

### Task 6: Settings field builder + commit classifier (pure, agent-free)

**Files:**
- Create: `source/clients/cli/internal/ui/settings_build.go`
- Test: `source/clients/cli/internal/ui/settings_build_test.go`

**Interfaces:**
- Consumes: `form` package (Tasks 1-5), `agentclient.Config`.
- Produces:
  - `buildSettingsSections(cfg *agentclient.Config, mode, accentToken string) []form.Section`
  - `commitKind` (`commitConfig`, `commitPermission`, `commitColor`, `commitNoop`) and `commitAction{kind commitKind; update agentclient.ConfigUpdate; value string}`
  - `classifyCommit(key, value string) commitAction`
  - `accentColorOptions() []form.Option` (palette tokens accepted by `Model.resolvePromptColor`)

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/ui/settings_build_test.go`:
```go
package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestBuildSettingsSectionsCoversKeys(t *testing.T) {
	cfg := &agentclient.Config{
		LocalRuntime: "ollama", LocalModel: "qwen3-coder", OllamaURL: "http://x",
		EmbeddingModel: "nomic", CloudProvider: "anthropic", CloudModel: "claude",
		CloudBaseURL: "http://m", CloudAPIKeySet: true, CloudState: "ok",
		Port: "50052", LocusMode: "cloud_primary",
	}
	secs := buildSettingsSections(cfg, "permissive", "palette:accent")
	titles := map[string]bool{}
	keys := map[string]bool{}
	for _, s := range secs {
		titles[s.Title] = true
		for _, f := range s.Fields {
			keys[f.Key()] = true
		}
	}
	for _, want := range []string{"Local Model", "Cloud", "Routing", "Permissions", "UI / Theme", "Server"} {
		if !titles[want] {
			t.Errorf("missing section %q", want)
		}
	}
	for _, want := range []string{
		"local-runtime", "local-model", "ollama-url", "embedding-model",
		"cloud-provider", "cloud-model", "cloud-base-url", "cloud-api-key", "cloud-state",
		"locus-mode", "permission-mode", "accent-color", "port",
	} {
		if !keys[want] {
			t.Errorf("missing field %q", want)
		}
	}
}

func TestClassifyCommit(t *testing.T) {
	if a := classifyCommit("local-model", "qwen"); a.kind != commitConfig || a.update.LocalModel != "qwen" {
		t.Fatalf("local-model -> %+v", a)
	}
	if a := classifyCommit("cloud-api-key", "sk-1"); a.kind != commitConfig || a.update.CloudAPIKey != "sk-1" {
		t.Fatalf("cloud-api-key -> %+v", a)
	}
	if a := classifyCommit("locus-mode", "local_only"); a.kind != commitConfig || a.update.LocusMode != "local_only" {
		t.Fatalf("locus-mode -> %+v", a)
	}
	if a := classifyCommit("permission-mode", "bypass"); a.kind != commitPermission || a.value != "bypass" {
		t.Fatalf("permission-mode -> %+v", a)
	}
	if a := classifyCommit("accent-color", "palette:info"); a.kind != commitColor || a.value != "palette:info" {
		t.Fatalf("accent-color -> %+v", a)
	}
	if a := classifyCommit("unknown", "x"); a.kind != commitNoop {
		t.Fatalf("unknown -> %+v", a)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestBuildSettings|TestClassifyCommit' -v`
Expected: FAIL — `buildSettingsSections`/`classifyCommit` undefined.

- [ ] **Step 3: Write minimal implementation**

`source/clients/cli/internal/ui/settings_build.go`:
```go
package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
)

// accentColorOptions lists the palette tokens accepted by Model.resolvePromptColor.
// Value tokens use the "palette:<key>" shape; the hex escape hatch stays on /color.
func accentColorOptions() []form.Option {
	return []form.Option{
		{Label: "accent (lime)", Value: "palette:accent"},
		{Label: "primary (amber)", Value: "palette:primary"},
		{Label: "info (cyan)", Value: "palette:info"},
		{Label: "bright", Value: "palette:bright"},
		{Label: "muted", Value: "palette:muted"},
		{Label: "border", Value: "palette:border"},
	}
}

func buildSettingsSections(cfg *agentclient.Config, mode, accentToken string) []form.Section {
	apiSet := cfg.CloudAPIKeySet
	return []form.Section{
		{Title: "Local Model", Fields: []form.Field{
			form.NewSelect("local-runtime", "local-runtime", []form.Option{
				{Label: "ollama", Value: "ollama"}, {Label: "llama_server", Value: "llama_server"},
			}, cfg.LocalRuntime),
			form.NewText("local-model", "local-model", cfg.LocalModel, ""),
			form.NewText("ollama-url", "ollama-url", cfg.OllamaURL, ""),
			form.NewReadOnly("embedding-model", "embedding-model", cfg.EmbeddingModel, "(read-only)"),
		}},
		{Title: "Cloud", Fields: []form.Field{
			form.NewSelect("cloud-provider", "cloud-provider", []form.Option{
				{Label: "anthropic", Value: "anthropic"}, {Label: "google", Value: "google"},
			}, cfg.CloudProvider),
			form.NewText("cloud-model", "cloud-model", cfg.CloudModel, ""),
			form.NewText("cloud-base-url", "cloud-base-url", cfg.CloudBaseURL, ""),
			form.NewMasked("cloud-api-key", "cloud-api-key", apiSet),
			form.NewReadOnly("cloud-state", "cloud-state", cfg.CloudState, "(read-only)"),
		}},
		{Title: "Routing", Fields: []form.Field{
			form.NewSelect("locus-mode", "locus-mode", []form.Option{
				{Label: "cloud_only", Value: "cloud_only"},
				{Label: "cloud_primary", Value: "cloud_primary"},
				{Label: "local_primary", Value: "local_primary"},
				{Label: "local_only", Value: "local_only"},
			}, cfg.LocusMode),
		}},
		{Title: "Permissions", Fields: []form.Field{
			form.NewSelect("permission-mode", "permission-mode", []form.Option{
				{Label: "strict", Value: "strict"},
				{Label: "permissive", Value: "permissive"},
				{Label: "bypass", Value: "bypass"},
			}, mode),
		}},
		{Title: "UI / Theme", Fields: []form.Field{
			form.NewSelect("accent-color", "accent-color", accentColorOptions(), accentToken),
		}},
		{Title: "Server", Fields: []form.Field{
			form.NewReadOnly("port", "port", cfg.Port, "(read-only)"),
		}},
	}
}

type commitKind int

const (
	commitNoop commitKind = iota
	commitConfig
	commitPermission
	commitColor
)

type commitAction struct {
	kind   commitKind
	update agentclient.ConfigUpdate
	value  string
}

// classifyCommit maps a committed (key,value) to the sink that should apply it.
func classifyCommit(key, value string) commitAction {
	var u agentclient.ConfigUpdate
	switch key {
	case "local-runtime":
		u.LocalRuntime = value
	case "local-model":
		u.LocalModel = value
	case "ollama-url":
		u.OllamaURL = value
	case "cloud-provider":
		u.CloudProvider = value
	case "cloud-model":
		u.CloudModel = value
	case "cloud-base-url":
		u.CloudBaseURL = value
	case "cloud-api-key":
		u.CloudAPIKey = value
	case "locus-mode":
		u.LocusMode = value
	case "permission-mode":
		return commitAction{kind: commitPermission, value: value}
	case "accent-color":
		return commitAction{kind: commitColor, value: value}
	default:
		return commitAction{kind: commitNoop}
	}
	return commitAction{kind: commitConfig, update: u}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestBuildSettings|TestClassifyCommit' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/settings_build.go source/clients/cli/internal/ui/settings_build_test.go
git commit -m "feat(cli): settings section builder + commit classifier"
```

---

### Task 7: `settingsPage` content page (RPC fetch, commit sinks, scroller)

**Files:**
- Create: `source/clients/cli/internal/ui/settings_page.go`
- Test: `source/clients/cli/internal/ui/settings_page_test.go`
- Modify: `source/clients/cli/internal/ui/content_page.go:7-12` (rename `contentPageConfig` → `contentPageSettings`)

**Interfaces:**
- Consumes: `buildSettingsSections`, `classifyCommit` (Task 6); `form.Form` (Task 5); `agentclient.Client`.
- Produces:
  - `settingsColorMsg{token string}` — emitted on accent-color commit; handled by the root model (Task 8).
  - `newSettingsPage(ag *agentclient.Client, p theme.Palette, s theme.Styles, accentToken string, w, h int) (*settingsPage, tea.Cmd)`
  - `*settingsPage` implements `contentPage` (`ID`/`SetSize`/`Update`/`View`) and `contentPageScroller` (`ScrollBy`/`ScrollTo`/`ScrollState`).
  - `settingsPage.ID()` returns `contentPageSettings`.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/ui/settings_page_test.go`:
```go
package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
)

func TestSettingsColorCommitEmitsMsg(t *testing.T) {
	p := theme.NewCrackerPalette()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 40}
	// Build a form whose commit hook is the page's real router.
	sp.form = form.New([]form.Section{{Title: "UI / Theme", Fields: []form.Field{
		form.NewSelect("accent-color", "accent-color", accentColorOptions(), "palette:accent"),
	}}})
	sp.form.OnCommit = sp.onCommit

	status, cmd, err := sp.onCommit("accent-color", "palette:info")
	if err != nil {
		t.Fatalf("onCommit err: %v", err)
	}
	if cmd == nil {
		t.Fatal("color commit should return a cmd")
	}
	msg := cmd()
	cm, ok := msg.(settingsColorMsg)
	if !ok || cm.token != "palette:info" {
		t.Fatalf("expected settingsColorMsg{palette:info}, got %#v", msg)
	}
	_ = status
}

func TestSettingsPageImplementsContentPage(t *testing.T) {
	var _ contentPage = (*settingsPage)(nil)
	var _ contentPageScroller = (*settingsPage)(nil)
}

func TestSettingsPageViewRenders(t *testing.T) {
	p := theme.NewCrackerPalette()
	s := theme.NewStyles(p)
	sp := &settingsPage{palette: p, styles: s, width: 100, height: 40}
	sp.form = form.New(buildSettingsSections(&testConfig(), "permissive", "palette:accent"))
	out := sp.View()
	if !strings.Contains(out, "Local Model") || !strings.Contains(out, "permission-mode") {
		t.Fatalf("settings View missing content:\n%s", out)
	}
}

// minimal config used by the render test (avoids hitting an agent)
func testConfig() agentclientConfigAlias { return agentclientConfigAlias{} }

// keep the unused tea import meaningful for future steps
var _ = tea.Batch
```

> Replace `agentclientConfigAlias` with `agentclient.Config` and add the `agentclient` import — the alias is only a placeholder to make Step 2 fail on undefined symbols. Set a couple of fields (e.g. `LocalModel`, `Port`) so the render shows values.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestSettings -v`
Expected: FAIL — `settingsPage`/`settingsColorMsg`/`onCommit` undefined.

- [ ] **Step 3: Write minimal implementation**

First rename in `source/clients/cli/internal/ui/content_page.go`:
```go
	contentPageSettings contentPageID = "settings"
	contentPageContext  contentPageID = "context"
	contentPageHistory  contentPageID = "history"
	contentPageModels   contentPageID = "models"
```
(remove the old `contentPageConfig` line; grep for other uses: `grep -rn "contentPageConfig" source/clients/cli` and update them in Task 8.)

Then `source/clients/cli/internal/ui/settings_page.go`:
```go
package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// settingsColorMsg is emitted when the accent-color field commits. The root
// model resolves the token and updates promptBorderColor (CLI-local, mirrors
// ResultSetPromptColor).
type settingsColorMsg struct{ token string }

// settingsPage is the sectioned settings content page (opened by /s, /settings,
// /config). It replaces the old flat configEditor.
type settingsPage struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	accentToken   string
	form          *form.Form
	offset        int
}

func newSettingsPage(ag *agentclient.Client, p theme.Palette, s theme.Styles, accentToken string, w, h int) (*settingsPage, tea.Cmd) {
	sp := &settingsPage{agent: ag, palette: p, styles: s, accentToken: accentToken, width: w, height: h}
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
	return sp, nil
}

func (sp *settingsPage) snapshotSections() []form.Section {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg, err := sp.agent.GetConfig(ctx)
	if err != nil {
		return []form.Section{{Title: "Settings", Fields: []form.Field{
			form.NewReadOnly("error", "error", err.Error(), ""),
		}}}
	}
	mode, err := sp.agent.GetPermissionMode(ctx)
	if err != nil {
		mode = ""
	}
	return buildSettingsSections(cfg, mode, sp.accentToken)
}

func (sp *settingsPage) ID() contentPageID { return contentPageSettings }

func (sp *settingsPage) SetSize(w, h int) { sp.width, sp.height = w, h }

func (sp *settingsPage) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, closed := sp.form.Update(msg)
	sp.clampScroll()
	return cmd, closed
}

func (sp *settingsPage) View() string {
	lines := sp.form.Lines(sp.width, sp.palette, sp.styles)
	sp.clampScroll()
	return renderScrollable(lines, sp.height, sp.width-2, sp.offset, sp.styles)
}

// onCommit routes a committed field to its sink.
func (sp *settingsPage) onCommit(key, value string) (string, tea.Cmd, error) {
	action := classifyCommit(key, value)
	switch action.kind {
	case commitConfig:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := sp.agent.UpdateConfig(ctx, action.update)
		return status, nil, err
	case commitPermission:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sp.agent.SetPermissionMode(ctx, action.value); err != nil {
			return "", nil, err
		}
		mode := action.value
		return "permission mode: " + mode, func() tea.Msg {
			return permissionModeChangedMsg{mode: mode}
		}, nil
	case commitColor:
		sp.accentToken = action.value
		token := action.value
		return "accent color set", func() tea.Msg {
			return settingsColorMsg{token: token}
		}, nil
	}
	return "", nil, nil
}

// --- contentPageScroller ---

func (sp *settingsPage) ScrollBy(delta int) { sp.offset += delta; sp.clampScroll() }
func (sp *settingsPage) ScrollTo(offset int) { sp.offset = offset; sp.clampScroll() }
func (sp *settingsPage) ScrollState() contentPageScrollState {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	return contentPageScrollState{Total: total, Height: sp.height, Offset: sp.offset}
}

func (sp *settingsPage) clampScroll() {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	max := total - sp.height
	if max < 0 {
		max = 0
	}
	if sp.offset > max {
		sp.offset = max
	}
	if sp.offset < 0 {
		sp.offset = 0
	}
}
```

Adjust the test (`settings_page_test.go`): replace `agentclientConfigAlias{}` with a real `agentclient.Config{LocalModel: "qwen", Port: "50052"}` and add the `"cercano/source/server/pkg/agentclient"` import; drop the `var _ = tea.Batch` line if `tea` is otherwise used.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestSettings -v`
Expected: PASS.

> `permissionModeChangedMsg{mode: ...}` is defined in `internal/ui/event_subscription.go` (already handled in `model.go`). Confirm the field name is `mode` (lowercase) before relying on it.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/settings_page.go source/clients/cli/internal/ui/settings_page_test.go source/clients/cli/internal/ui/content_page.go
git commit -m "feat(cli): settingsPage content page with commit sinks + scroller"
```

---

### Task 8: Wire-in — slash routing, root model, remove old editor

**Files:**
- Modify: `source/clients/cli/internal/slash/registry.go:23-36` (rename result kind)
- Modify: `source/clients/cli/internal/slash/contextview.go` (add settings registration) OR create `source/clients/cli/internal/slash/settings.go`
- Modify: `source/clients/cli/internal/slash/config.go:25` (open settings instead of config editor)
- Modify: `source/clients/cli/internal/ui/model.go` (registration, routing, color msg handler, accent token state)
- Delete: `source/clients/cli/internal/ui/config_editor.go`
- Test: `source/clients/cli/internal/slash/settings_test.go`

**Interfaces:**
- Consumes: `newSettingsPage`, `settingsColorMsg` (Task 7); `Model.resolvePromptColor` (existing).
- Produces: `ResultOpenSettings` result kind; `/s`, `/settings`, `/config` all dispatch it.

- [ ] **Step 1: Write the failing test**

`source/clients/cli/internal/slash/settings_test.go`:
```go
package slash

import "testing"

func TestSettingsCommandsOpenSettings(t *testing.T) {
	r := New()
	RegisterSettings(r)
	for _, name := range []string{"s", "settings"} {
		res, ok := r.Dispatch("/" + name)
		if !ok {
			t.Fatalf("/%s did not dispatch", name)
		}
		if res.Kind != ResultOpenSettings {
			t.Fatalf("/%s -> kind %v, want ResultOpenSettings", name, res.Kind)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/slash/ -run TestSettingsCommands -v`
Expected: FAIL — `RegisterSettings`/`ResultOpenSettings` undefined.

- [ ] **Step 3: Write minimal implementation**

In `registry.go`, rename the result kind constant `ResultOpenConfigEditor` → `ResultOpenSettings` (line ~28). Grep and fix every reference: `grep -rn "ResultOpenConfigEditor" source/clients/cli`.

Create `source/clients/cli/internal/slash/settings.go`:
```go
package slash

// RegisterSettings installs /s and /settings, the sectioned settings page that
// replaces the old flat /config editor. /config (registered in config.go) opens
// the same page when called with no args.
func RegisterSettings(r *Registry) {
	r.Register(Command{
		Name:    "s",
		Aliases: []string{"settings"},
		Help:    "Open the settings page.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenSettings}
		},
	})
}
```

In `config.go`, the no-arg branch already returns `Result{Kind: ResultOpenConfigEditor}` — after the rename it returns `Result{Kind: ResultOpenSettings}`. No further change (the rename covers it). The `/config key value` and `/config show` paths stay.

In `model.go`:
1. Register the new command near the other registrations (after `slash.RegisterContextView(reg)`):
```go
	slash.RegisterSettings(reg)
```
2. Add accent-token state to the `Model` struct (next to `promptBorderColor` ~line 138):
```go
	// promptColorToken is the token form of promptBorderColor ("palette:<key>"
	// or "#RRGGBB"), kept so the settings page can show the current selection.
	promptColorToken string
```
   Initialize it in the constructor next to `promptBorderColor: p.Accent,` (~line 263):
```go
		promptColorToken:   "palette:accent",
```
3. Update the `ResultSetPromptColor` handler (~line 1162) to also record the token:
```go
	case slash.ResultSetPromptColor:
		m.promptBorderColor = m.resolvePromptColor(res.Text)
		m.promptColorToken = res.Text
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "prompt color set"})
		m.refreshViewport()
```
4. Replace the `ResultOpenConfigEditor` case (~line 1146) — now `ResultOpenSettings` — to open the settings page:
```go
	case slash.ResultOpenSettings:
		sp, cmd := newSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, m.height)
		m.content = sp
		return m, cmd
```
5. Add a handler for `settingsColorMsg` in the root `Update` switch (near the `permissionModeChangedMsg` case ~line 904):
```go
	case settingsColorMsg:
		m.promptBorderColor = m.resolvePromptColor(msg.token)
		m.promptColorToken = msg.token
		m.refreshViewport()
		return m, nil
```
6. Delete `config_editor.go`:
```bash
git rm source/clients/cli/internal/ui/config_editor.go
```
   If anything still references `newConfigEditor`/`buildConfigRows`/`saveSingle`/`editorError`, grep and remove those references (the settings page supersedes them): `grep -rn "newConfigEditor\|buildConfigRows\|saveSingle\|editorError\|contentPageConfig" source/clients/cli`.

- [ ] **Step 4: Build, then run the full suite**

Run: `cd source/clients/cli && go build ./... && go test ./... -count=1`
Expected: build succeeds; all tests PASS (slash settings test included). Fix any leftover `ResultOpenConfigEditor` / `contentPageConfig` / `configEditor` references the compiler flags.

- [ ] **Step 5: Commit**

```bash
git add -A source/clients/cli/internal/slash/settings.go source/clients/cli/internal/slash/registry.go source/clients/cli/internal/slash/config.go source/clients/cli/internal/slash/settings_test.go source/clients/cli/internal/ui/model.go
git commit -m "feat(cli): wire /s /settings /config to the settings page; drop config editor"
```

---

### Task 9: Docs + manual acceptance

**Files:**
- Modify: `docs/agent/README.md` (slash command table — replace `/config` row, add `/s`)
- Modify: `docs/features/cli/settings/design.md` (flip Status to "Implemented")

- [ ] **Step 1: Update the slash table** in `docs/agent/README.md` — change the `/config` entry to "Open the settings page" and add `/s` / `/settings` aliases; note `/config key value` still does direct-set.

- [ ] **Step 2: Manual acceptance** (record results in the commit message):

```bash
cd source/clients/cli && go build -o bin/cercano-cli .
./bin/cercano-cli
```
Verify: `/s` opens the page; sections render (Local Model, Cloud, Routing, Permissions, UI/Theme, Server); ↑↓ navigates; enter edits a text field and saves; enter on a select opens the picker, ↑↓ + enter commits; changing permission-mode flips the status-bar chip; changing accent-color recolors the prompt border live; `/settings` and `/config` open the same page; `/config show` and `/config local-model x` still work; mid-page terminal resize re-flows cleanly.

- [ ] **Step 3: Commit**

```bash
git add docs/agent/README.md docs/features/cli/settings/design.md
git commit -m "docs(cli): document settings page; mark feature implemented"
```

---

## Self-Review

**Spec coverage:**
- Replace `/config` with sectioned page → Tasks 7, 8. ✓
- Extensible `internal/form` widget layer (text, masked, select, toggle, read-only) → Tasks 1-5. ✓
- Sections (Local, Cloud, Routing, Permissions, UI/Theme, Server) → Task 6 `buildSettingsSections`. ✓
- Pickers for enums, text for free-form, masked for secrets → Tasks 2-3, used in Task 6. ✓
- Commit sinks: config RPC / permission RPC + chip msg / color msg → Task 7 `onCommit` + Task 6 `classifyCommit`. ✓
- `/s` + `/settings` + `/config` alias → Task 8. ✓
- CLI/agent separation (msgs, not direct state mutation) → `settingsColorMsg`, reuse `permissionModeChangedMsg` (Tasks 7-8). ✓
- Accent = SelectField over palette + hex via retained `/color` → Task 6 `accentColorOptions`, noted in Task 8. ✓
- contentPage + scroller parity with `/m`,`/c` → Task 7. ✓
- Tests at every layer → Tasks 1-8. ✓

**Placeholder scan:** The only intentional "make it fail" placeholders (`down()` stub in Task 3, `agentclientConfigAlias` in Task 7) are explicitly removed/replaced in their own Step 3. No "TODO/handle edge cases" steps. ✓

**Type consistency:** `Field.Update(msg) (tea.Cmd, bool, string)` used identically across Tasks 1-5; `Form.OnCommit func(key, value string) (string, tea.Cmd, error)` matches `settingsPage.onCommit` signature (Task 7); `classifyCommit`/`commitAction`/`commitKind` consistent Task 6 ↔ 7; `permissionModeChangedMsg{mode}` and `settingsColorMsg{token}` consistent Tasks 7-8; `contentPageSettings` consistent Tasks 7-8. ✓

**API-name caveats flagged inline:** charm v2 key constants (`tea.KeyEnter`, etc.) and theme constructors (`NewCrackerPalette`/`NewStyles`) must be confirmed against the installed versions before first run — noted in Tasks 2, 3, 5. These are the only uncertain symbols; everything else is copied verbatim from existing code.
