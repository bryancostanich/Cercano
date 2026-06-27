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
