package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// Section is a titled group of fields. When Groups is non-empty, Fields is
// ignored and each Group renders as a subheading with its own fields.
type Section struct {
	Title  string
	Fields []Field
	Groups []Group
}

// Group is a titled sub-cluster of fields within a section — a subheading that
// keeps related fields visually clustered under one section title. Zero-title
// groups render their fields with no header line.
type Group struct {
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

// SetCursor moves the focused-field index, clamped to the valid range. Used to
// preserve the focus position when a host rebuilds the form (e.g. a live theme
// edit that reconstructs the sections).
func (f *Form) SetCursor(i int) {
	n := len(f.flat())
	if n == 0 {
		f.cursor = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i > n-1 {
		i = n - 1
	}
	f.cursor = i
}

// FocusedLine returns the zero-based output line of the focused field as of
// the last View (or Lines) call.
func (f *Form) FocusedLine() int { return f.focusedLine }

func (f *Form) flat() []Field {
	var out []Field
	for _, sec := range f.Sections {
		if len(sec.Groups) > 0 {
			for _, grp := range sec.Groups {
				out = append(out, grp.Fields...)
			}
			continue
		}
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
		// Reload on failure too: the failed call may still have applied on
		// the server (e.g. a client deadline expiring while the server
		// finishes the work), and hosts invalidate their snapshot caches on
		// error — re-snapshot so the form shows the server's truth rather
		// than pre-commit state. With caches intact this is a no-op repaint.
		if f.OnReload != nil {
			f.Sections = f.OnReload()
		}
		return tea.Batch(fieldCmd, cmd)
	}
	f.status = status
	if f.OnReload != nil {
		f.Sections = f.OnReload()
	}
	return tea.Batch(fieldCmd, cmd)
}

// SetStatus replaces the footer status line. Used by hosts that complete a
// commit asynchronously — OnCommit returned before the outcome was known, so
// the host pushes the real result (or a spinner frame) in later.
func (f *Form) SetStatus(s string) { f.status = s }

// Reload re-snapshots sections via OnReload. Async-commit hosts call this
// once fresh values are cached so the rebuilt fields reflect the server.
func (f *Form) Reload() {
	if f.OnReload != nil {
		f.Sections = f.OnReload()
	}
}

// Lines renders the form as individual lines (for scroller hosts).
func (f *Form) Lines(width int, palette theme.Palette, styles theme.Styles) []string {
	return strings.Split(f.View(width, palette, styles), "\n")
}

// View renders all sections as titled panels plus a footer.
func (f *Form) View(width int, palette theme.Palette, styles theme.Styles) string {
	// Match the scrollbar-reserving width the host renders into (width-2, the
	// same as dashboardPanelWidth) so the boxes fill the content region. A low
	// floor lets the box shrink with a narrow terminal instead of overflowing
	// and getting truncated.
	panelW := width - 2
	if panelW < 24 {
		panelW = 24
	}
	labelW := 0
	for _, sec := range f.Sections {
		fields := sec.Fields
		if len(sec.Groups) > 0 {
			fields = nil
			for _, grp := range sec.Groups {
				fields = append(fields, grp.Fields...)
			}
		}
		for _, fld := range fields {
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
		// Title line, then an underline rule, then a blank line — written as
		// explicit lines with the rule sized to the box text width (panelW-4:
		// border+padding both sides) so lipgloss never wraps them. Wrapping would
		// desync the focusedLine accounting below, which assumes this fixed
		// 3-line section header.
		body.WriteString(title + "\n")
		body.WriteString(styles.BorderDim.Render(strings.Repeat("─", max(0, panelW-4))) + "\n")
		body.WriteString("\n")
		// bodyLine tracks how many lines have been written into body. The section
		// header (title, rule, blank) occupies body lines 0-2, so the first field
		// starts at line 3. We track real line counts (not field index) because a
		// field can render multiple lines — an open select picker, or any field in
		// the narrow under-label layout.
		bodyLine := 3
		focusedBodyLine := -1
		renderField := func(fld Field) {
			focused := idx == f.cursor
			marker := "   "
			if focused {
				marker = styles.Accent.Render(" ▶ ")
				focusedBodyLine = bodyLine
			}
			label := styles.Muted.Render(fld.Label())
			if focused && !fld.Editing() {
				label = styles.Bright.Render(fld.Label())
			}
			pad := strings.Repeat(" ", labelW-lipgloss.Width(fld.Label())+2)
			// The value column starts after the marker (3 cols) + label/pad
			// (labelW+2). When there's room, the value (and any wrapped lines,
			// e.g. an open select picker's options) forms a right-hand second
			// column aligned under that offset. When the box is too narrow for a
			// usable second column, the value drops under the label with a small
			// indent instead.
			colOffset := 3 + labelW + 2
			// The bordered, padded box reserves 4 columns (1 border + 1 padding
			// per side), so usable text width is panelW-4. Size the value column
			// to fit, or lipgloss re-wraps the overflow flush to the left margin.
			innerW := panelW - 4
			if innerW < 8 {
				innerW = 8
			}
			valueW := innerW - colOffset
			narrow := valueW < 16
			fieldW := valueW
			indent := colOffset
			if narrow {
				fieldW = innerW - 4
				indent = 4
			}
			if fieldW < 1 {
				fieldW = 1
			}
			// In the narrow under-label layout, a field may opt into a single
			// vertical column (one item per line) so it doesn't read as a cramped
			// horizontal list. Otherwise use the normal horizontal render.
			rendered := fld.View(focused, fieldW, styles)
			if narrow {
				if sf, ok := fld.(stackable); ok {
					rendered = sf.ViewStacked(focused, fieldW, styles)
				}
			}
			// Hard-wrap each rendered line to the value-column width so an
			// over-long value (URL, model name) wraps as a hanging indent in the
			// value column instead of overflowing the box and being re-wrapped
			// flush against the left margin by lipgloss.
			var cellLines []string
			for _, raw := range strings.Split(rendered, "\n") {
				cellLines = append(cellLines, strings.Split(ansi.Hardwrap(raw, fieldW, false), "\n")...)
			}
			if narrow {
				// Label on its own line; value(s) wrap beneath, indented.
				body.WriteString(marker + label + "\n")
				bodyLine++
				for _, cl := range cellLines {
					body.WriteString(strings.Repeat(" ", indent) + cl + "\n")
					bodyLine++
				}
			} else {
				body.WriteString(marker + label + pad + cellLines[0] + "\n")
				bodyLine++
				for _, cl := range cellLines[1:] {
					body.WriteString(strings.Repeat(" ", indent) + cl + "\n")
					bodyLine++
				}
			}
			idx++
		}
		if len(sec.Groups) > 0 {
			for gi, grp := range sec.Groups {
				if gi > 0 {
					body.WriteString("\n")
					bodyLine++
				}
				if grp.Title != "" {
					body.WriteString("  " + styles.Muted.Render(grp.Title) + "\n")
					bodyLine++
				}
				for _, fld := range grp.Fields {
					renderField(fld)
				}
			}
		} else {
			for _, fld := range sec.Fields {
				renderField(fld)
			}
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
		if focusedBodyLine >= 0 {
			// +1 for the box's top border line above the body.
			f.focusedLine = base + 1 + focusedBodyLine
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
