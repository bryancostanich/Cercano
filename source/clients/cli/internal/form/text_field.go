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
