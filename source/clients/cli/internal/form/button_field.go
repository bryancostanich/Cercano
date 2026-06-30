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
