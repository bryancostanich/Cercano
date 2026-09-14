package form

import (
	"strings"

	"cercano/source/clients/cli/internal/theme"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// SelectPairField is one navigable row with two independently editable values.
// Its commit key belongs to the active child, or key+"-reset" for its reset action.
type SelectPairField struct {
	Left, Right    *SelectField
	key, label     string
	active         int
	resetRequested bool
	Resettable     bool
	Note           string
	Help           string
}

func NewSelectPair(key, label string, left, right *SelectField, resettable bool, note string) *SelectPairField {
	return &SelectPairField{key: key, label: label, Left: left, Right: right, Resettable: resettable, Note: note}
}
func (p *SelectPairField) selected() *SelectField {
	if p.active == 1 {
		return p.Right
	}
	return p.Left
}
func (p *SelectPairField) Key() string {
	if p.resetRequested {
		return p.key + "-reset"
	}
	return p.selected().Key()
}
func (p *SelectPairField) Label() string   { return p.label }
func (p *SelectPairField) Display() string { return p.Left.Display() + " / " + p.Right.Display() }
func (p *SelectPairField) Editing() bool   { return p.selected().Editing() }
func (p *SelectPairField) FocusPart() int  { return p.active }
func (p *SelectPairField) RestoreFocusPart(part int) {
	if part == 0 || part == 1 {
		p.active = part
	}
}
func (p *SelectPairField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	p.resetRequested = false
	if p.Editing() {
		return p.selected().Update(msg)
	}
	switch msg.String() {
	case "left", "h", "right", "l", "tab", "shift+tab":
		p.active = 1 - p.active
		return nil, false, ""
	case "r":
		if p.Resettable {
			p.resetRequested = true
			return nil, true, ""
		}
		return nil, false, ""
	default:
		return p.selected().Update(msg)
	}
}

const pairMinWidth = 45

func pairWidths(width int) (int, int) { left := (width - 1) / 2; return left, width - 2 - left }
func (p *SelectPairField) View(focused bool, width int, s theme.Styles) string {
	values := []string{p.Left.Display(), p.Right.Display()}
	for i := range values {
		style := s.Primary
		if focused && p.active == i {
			style = s.Accent.Underline(true)
		}
		values[i] = style.Render(values[i])
	}
	var lines []string
	if width >= pairMinWidth {
		left, right := pairWidths(width)
		a := ansi.Truncate(values[0], left, "…")
		b := ansi.Truncate(values[1], right, "…")
		lines = append(lines, a+strings.Repeat(" ", max(0, left-lipgloss.Width(a)))+"  "+b)
	} else {
		lines = append(lines, s.Muted.Render(p.Left.Label()+": ")+values[0], s.Muted.Render(p.Right.Label()+": ")+values[1])
	}
	if p.Note != "" {
		lines = append(lines, s.Muted.Render(p.Note))
	}
	if focused {
		if p.Editing() {
			lines = append(lines, s.Muted.Render(p.selected().Label()+": ")+p.selected().View(true, max(1, width), s))
		} else {
			hint := "←/→ column · Enter choose"
			if p.Resettable {
				hint += " · r Restore defaults"
			}
			lines = append(lines, s.Dim.Render(hint))
			if p.Help != "" {
				lines = append(lines, s.Dim.Render(p.Help))
			}
		}
	}
	// Respect caller width even when narrow labels, notes or pickers wrap.
	return ansi.Hardwrap(strings.Join(lines, "\n"), max(1, width), false)
}
