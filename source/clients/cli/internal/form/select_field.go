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
