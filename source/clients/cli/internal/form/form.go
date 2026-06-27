package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Section is a titled group of fields.
type Section struct {
	Title  string
	Fields []Field
}

// Form arranges sections of fields with nav + commit routing.
type Form struct {
	Sections []Section
	// OnCommit fires when a field commits a new value. Optional.
	OnCommit func(key, value string) (status string, cmd tea.Cmd, err error)
	// OnReload returns a fresh section snapshot after a successful commit. Optional.
	OnReload func() []Section

	cursor      int // index into the flattened field list
	status      string
	focusedLine int // output line (0-based) of the focused field; refreshed on each View call
}

// New builds a form positioned on the first focusable field.
func New(sections []Section) *Form { return &Form{Sections: sections} }

// Cursor returns the flattened-field index of the focused field.
func (f *Form) Cursor() int { return f.cursor }

// FocusedLine returns the zero-based output line of the focused field as of
// the last View (or Lines) call.
func (f *Form) FocusedLine() int { return f.focusedLine }

func (f *Form) flat() []Field {
	var out []Field
	for _, sec := range f.Sections {
		out = append(out, sec.Fields...)
	}
	return out
}

// Update routes a key event. Returns (cmd, closed). closed=true means the host
// should dismiss the page.
func (f *Form) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	fields := f.flat()
	if len(fields) == 0 {
		if msg.String() == "esc" || msg.String() == "q" {
			return nil, true
		}
		return nil, false
	}
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	cur := fields[f.cursor]

	if cur.Editing() {
		cmd, committed, val := cur.Update(msg)
		if committed {
			return f.commit(cur.Key(), val, cmd), false
		}
		return cmd, false
	}

	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "up", "k":
		if f.cursor > 0 {
			f.cursor--
		}
	case "down", "j":
		if f.cursor < len(fields)-1 {
			f.cursor++
		}
	case "home", "g":
		f.cursor = 0
	case "end", "G":
		f.cursor = len(fields) - 1
	default:
		// Forward activation keys (enter/space) to the field.
		cmd, committed, val := cur.Update(msg)
		if committed {
			return f.commit(cur.Key(), val, cmd), false
		}
		return cmd, false
	}
	return nil, false
}

func (f *Form) commit(key, val string, fieldCmd tea.Cmd) tea.Cmd {
	if f.OnCommit == nil {
		f.status = "no commit handler"
		return fieldCmd
	}
	status, cmd, err := f.OnCommit(key, val)
	if err != nil {
		f.status = "save failed: " + err.Error()
		return fieldCmd
	}
	f.status = status
	if f.OnReload != nil {
		f.Sections = f.OnReload()
	}
	return tea.Batch(fieldCmd, cmd)
}

// Lines renders the form as individual lines (for scroller hosts).
func (f *Form) Lines(width int, palette theme.Palette, styles theme.Styles) []string {
	return strings.Split(f.View(width, palette, styles), "\n")
}

// View renders all sections as titled panels plus a footer.
func (f *Form) View(width int, palette theme.Palette, styles theme.Styles) string {
	panelW := width - 6
	if panelW < 40 {
		panelW = 40
	}
	labelW := 0
	for _, sec := range f.Sections {
		for _, fld := range sec.Fields {
			if lw := lipgloss.Width(fld.Label()); lw > labelW {
				labelW = lw
			}
		}
	}

	var out strings.Builder
	idx := 0
	for _, sec := range f.Sections {
		var body strings.Builder
		title := styles.Accent.Render("─ " + sec.Title + " ")
		body.WriteString(title + styles.BorderDim.Render(strings.Repeat("─", max(0, panelW-lipgloss.Width(title)))) + "\n\n")
		sectionFocusedIdx := -1
		fieldInSection := 0
		for _, fld := range sec.Fields {
			focused := idx == f.cursor
			marker := "   "
			if focused {
				marker = styles.Accent.Render(" ▶ ")
				sectionFocusedIdx = fieldInSection
			}
			label := styles.Muted.Render(fld.Label())
			if focused && !fld.Editing() {
				label = styles.Bright.Render(fld.Label())
			}
			pad := strings.Repeat(" ", labelW-lipgloss.Width(fld.Label())+2)
			body.WriteString(marker + label + pad + fld.View(focused, panelW, styles) + "\n")
			idx++
			fieldInSection++
		}
		// Capture base before writing this section's box so focusedLine is a
		// valid index into strings.Split(View(...), "\n").
		base := strings.Count(out.String(), "\n")
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(palette.Border).
			Padding(0, 1).
			Width(panelW).
			Render(body.String())
		if sectionFocusedIdx >= 0 {
			// Box layout: line 0 = top border, line 1 = section title,
			// line 2 = blank (from the "\n\n" after the title), line 3+ = fields.
			f.focusedLine = base + 3 + sectionFocusedIdx
		}
		out.WriteString(box)
		out.WriteString("\n")
	}

	footer := styles.Muted.Render("   ↑↓ navigate  enter edit/select  esc close")
	if f.status != "" {
		footer += styles.BorderDim.Render("   ·   ") + styles.Accent.Render(f.status)
	}
	out.WriteString(footer)
	return out.String()
}
