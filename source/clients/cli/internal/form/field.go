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

// stackable is an optional Field capability: render as a single vertical column
// (one item per line) for the narrow under-label layout, where the normal
// horizontal render would read as a cramped list. Fields that don't implement
// it fall back to View.
type stackable interface {
	ViewStacked(focused bool, width int, s theme.Styles) string
}
