package form

import (
	"regexp"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

var colorHexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ColorField edits a single #RRGGBB color, showing a live swatch.
type ColorField struct {
	key, label string
	hex        string
	editable   bool
	editing    bool
	input      textinput.Model
}

// NewColor builds a color field. editable=false renders a read-only swatch.
func NewColor(key, label, hex string, editable bool) *ColorField {
	return &ColorField{key: key, label: label, hex: strings.ToLower(hex), editable: editable}
}

func (f *ColorField) Key() string     { return f.key }
func (f *ColorField) Label() string   { return f.label }
func (f *ColorField) Display() string { return f.hex }
func (f *ColorField) Editing() bool   { return f.editing }
func (f *ColorField) Hex() string     { return f.hex }

func (f *ColorField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if !f.editing {
		if f.editable && msg.Code == tea.KeyEnter {
			ti := textinput.New()
			ti.CharLimit = 7
			cmd := ti.Focus()
			ti.SetValue(f.hex)
			ti.CursorEnd()
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
		val := strings.ToLower(strings.TrimSpace(f.input.Value()))
		if !colorHexRe.MatchString(val) {
			f.editing = false
			return nil, false, "" // reject; value unchanged
		}
		f.hex = val
		f.editing = false
		return nil, true, val
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd, false, ""
}

func (f *ColorField) View(focused bool, width int, s theme.Styles) string {
	swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(f.hex)).Render("███")
	if f.editing {
		return swatch + " " + f.input.View()
	}
	hex := s.Primary.Render(f.hex)
	if !f.editable {
		hex = s.Muted.Render(f.hex)
	}
	return swatch + " " + hex
}
