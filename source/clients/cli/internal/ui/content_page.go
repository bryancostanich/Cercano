package ui

import tea "charm.land/bubbletea/v2"

type contentPageID string

const (
	contentPageConfig  contentPageID = "config"
	contentPageHistory contentPageID = "history"
	contentPageModels  contentPageID = "models"
)

// contentPage owns the TUI's main content region between the header and the
// prompt chrome. The root model keeps global keys, prompt/status, and terminal
// layout; pages handle their local controls only.
type contentPage interface {
	ID() contentPageID
	SetSize(width, height int)
	Update(tea.KeyPressMsg) (cmd tea.Cmd, closed bool)
	View() string
}
