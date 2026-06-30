package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// RowSelect is the sentinel a RowField commits when activated, signalling the
// host to expand this row's detail.
const RowSelect = "select"

// RowField is a selectable list row carrying a right-side status annotation
// (e.g. "✓ key   (active)" or "(untested)"). Activating it (enter/space) commits
// RowSelect so the Form's OnCommit routes it to a selection handler. It is never
// in editing mode — selection is host state, not field state.
type RowField struct {
	key, label, annotation string
	accent                 bool // render the annotation in the accent color (active row)
}

// NewRow builds a list row. accent=true highlights the annotation (active row).
func NewRow(key, label, annotation string, accent bool) *RowField {
	return &RowField{key: key, label: label, annotation: annotation, accent: accent}
}

func (f *RowField) Key() string     { return f.key }
func (f *RowField) Label() string   { return f.label }
func (f *RowField) Display() string { return f.annotation }
func (f *RowField) Editing() bool   { return false }

func (f *RowField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace {
		return nil, true, RowSelect
	}
	return nil, false, ""
}

func (f *RowField) View(focused bool, width int, s theme.Styles) string {
	if f.annotation == "" {
		return ""
	}
	if f.accent {
		return s.Accent.Render(f.annotation)
	}
	return s.Muted.Render(f.annotation)
}
