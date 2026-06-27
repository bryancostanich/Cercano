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
