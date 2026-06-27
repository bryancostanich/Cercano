package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	case tea.KeyLeft:
		// Options are laid out horizontally, so left/right moves the cursor.
		if f.cursor > 0 {
			f.cursor--
		}
	case tea.KeyRight:
		if f.cursor < len(f.options)-1 {
			f.cursor++
		}
	case tea.KeyEnter:
		if len(f.options) == 0 {
			f.open = false
			return nil, false, ""
		}
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
	// Options render horizontally, separated by " · ", and wrap onto further
	// lines when they don't fit in width. The caller (Form) indents wrapped
	// lines so they align as a right-hand column under the first option.
	if width < 1 {
		width = 1
	}
	sep := s.BorderDim.Render(" · ")
	sepW := lipgloss.Width(sep)
	var lines []string
	var cur strings.Builder
	curW := 0
	for i, o := range f.options {
		var seg string
		if i == f.cursor {
			seg = s.Accent.Render("‹" + o.Label + "›")
		} else {
			seg = s.Muted.Render(o.Label)
		}
		segW := lipgloss.Width(seg)
		if curW > 0 && curW+sepW+segW > width {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		if curW > 0 {
			cur.WriteString(sep)
			curW += sepW
		}
		cur.WriteString(seg)
		curW += segW
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n")
}
