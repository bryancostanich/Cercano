// Package render hosts the dedicated render primitives the cercano CLI relies
// on instead of passing raw output through to the terminal: Tables, diffs,
// tool-call sub-frames. The Table primitive is the non-negotiable from the
// design spec — markdown tables from the agent get intercepted and routed
// through this so they never scramble on a narrow terminal.
package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// Column declares one table column. Wrappable marks the column whose cells may
// wrap across multiple lines to help a wide table fit without losing data.
type Column struct {
	Name      string
	Wrappable bool
}

// Table is the typed alternative to raw markdown tables. Rows are keyed by
// Column.Name.
type Table struct {
	Cols []Column
	Rows []map[string]string
}

// minWrapWidth is the narrowest the wrappable column shrinks to before the table
// gives up on a grid and transposes instead.
const minWrapWidth = 12

// Render returns a styled string that fits within maxWidth WITHOUT dropping any
// data. It draws a grid when the columns fit — wrapping the wrappable column
// across lines if that helps — and otherwise transposes to a lossless key:value
// layout, one block per row. It never drops a column and never truncates a cell.
func (t Table) Render(maxWidth int, styles theme.Styles) string {
	if len(t.Cols) == 0 || len(t.Rows) == 0 {
		return styles.Muted.Render("(empty table)")
	}

	widths := computeColWidths(t.Cols, t.Rows)
	if totalGridWidth(widths) <= maxWidth {
		return renderGrid(t.Cols, widths, t.Rows, styles)
	}
	// Try wrapping the wrappable column across lines to make the grid fit.
	if i := wrappableIdx(t.Cols); i >= 0 && widths[i] > minWrapWidth {
		over := totalGridWidth(widths) - maxWidth
		newW := widths[i] - over
		if newW < minWrapWidth {
			newW = minWrapWidth
		}
		widths[i] = newW
		if totalGridWidth(widths) <= maxWidth {
			return renderGrid(t.Cols, widths, t.Rows, styles)
		}
	}
	// Still too wide for a grid → responsive transpose; every column survives.
	return renderTransposed(t.Cols, t.Rows, maxWidth, styles)
}

// totalGridWidth = sum of widths + 1 left border + 1 right border + (cols-1) inner separators + 2 padding per col.
func totalGridWidth(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	sum := 0
	for _, w := range widths {
		sum += w + 2 // 1-space pad on each side
	}
	return sum + len(widths) + 1 // borders + separators
}

func computeColWidths(cols []Column, rows []map[string]string) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = lipgloss.Width(c.Name)
	}
	for _, r := range rows {
		for i, c := range cols {
			cw := lipgloss.Width(r[c.Name])
			if cw > w[i] {
				w[i] = cw
			}
		}
	}
	return w
}

func wrappableIdx(cols []Column) int {
	for i, c := range cols {
		if c.Wrappable {
			return i
		}
	}
	return -1
}

func renderGrid(cols []Column, widths []int, rows []map[string]string, styles theme.Styles) string {
	var b strings.Builder

	// Top border: ┌──┬──┐
	b.WriteString(styles.Border.Render(borderRow("┌", "┬", "┐", "─", widths)))
	b.WriteString("\n")

	// Header row.
	b.WriteString(styles.Border.Render("│"))
	for i, c := range cols {
		cell := padCell(c.Name, widths[i])
		b.WriteString(" ")
		b.WriteString(styles.Accent.Render(cell))
		b.WriteString(" ")
		b.WriteString(styles.Border.Render("│"))
	}
	b.WriteString("\n")

	// Header separator: ├──┼──┤
	b.WriteString(styles.Border.Render(borderRow("├", "┼", "┤", "─", widths)))
	b.WriteString("\n")

	// Data rows. Cells wrap (never truncate) to their column width, so a row
	// can span multiple terminal lines; non-wrapping cells sit on the first
	// line with blanks beneath.
	for _, r := range rows {
		cellLines := make([][]string, len(cols))
		rowH := 1
		for i, c := range cols {
			cellLines[i] = wrapCell(r[c.Name], widths[i])
			if len(cellLines[i]) > rowH {
				rowH = len(cellLines[i])
			}
		}
		for k := 0; k < rowH; k++ {
			b.WriteString(styles.Border.Render("│"))
			for i := range cols {
				cell := ""
				if k < len(cellLines[i]) {
					cell = cellLines[i][k]
				}
				cell = padCell(cell, widths[i])
				b.WriteString(" ")
				b.WriteString(styles.Primary.Render(cell))
				b.WriteString(" ")
				b.WriteString(styles.Border.Render("│"))
			}
			b.WriteString("\n")
		}
	}

	// Bottom border: └──┴──┘
	b.WriteString(styles.Border.Render(borderRow("└", "┴", "┘", "─", widths)))
	return b.String()
}

func renderTransposed(cols []Column, rows []map[string]string, maxWidth int, styles theme.Styles) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(styles.Border.Render(strings.Repeat("─", min(maxWidth, 40))))
			b.WriteString("\n")
		}
		for _, c := range cols {
			val := r[c.Name]
			label := styles.Accent.Render(c.Name + ":")
			b.WriteString(label)
			b.WriteString(" ")
			// Wrap value at maxWidth - label width - 1 space.
			labelW := lipgloss.Width(c.Name) + 2
			wrapAt := maxWidth - labelW
			if wrapAt < 10 {
				wrapAt = 10
			}
			b.WriteString(styles.Primary.Render(softWrap(val, wrapAt, labelW)))
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func borderRow(left, sep, right, fill string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		b.WriteString(strings.Repeat(fill, w+2))
		if i < len(widths)-1 {
			b.WriteString(sep)
		}
	}
	b.WriteString(right)
	return b.String()
}

// padCell right-pads val with spaces so its visible width equals targetWidth.
func padCell(val string, targetWidth int) string {
	w := lipgloss.Width(val)
	if w >= targetWidth {
		return val
	}
	return val + strings.Repeat(" ", targetWidth-w)
}

// wrapCell word-wraps s to fit within width columns, returning one string per
// line (always at least one). Words longer than the column are hard-broken so
// nothing is lost — wrap, never truncate.
func wrapCell(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		// Hard-break a word that's wider than the whole column.
		for lipgloss.Width(w) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			head, tail := cutRunes(w, width)
			lines = append(lines, head)
			w = tail
		}
		switch {
		case cur == "":
			cur = w
		case lipgloss.Width(cur)+1+lipgloss.Width(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// cutRunes splits s into a head of at most width runes and the remainder.
func cutRunes(s string, width int) (head, tail string) {
	r := []rune(s)
	if len(r) <= width {
		return s, ""
	}
	return string(r[:width]), string(r[width:])
}

// softWrap wraps text to fit lineWidth columns. Continuation lines are
// left-padded by hangIndent so they align with the start of value text after
// the label.
func softWrap(text string, lineWidth, hangIndent int) string {
	if lipgloss.Width(text) <= lineWidth {
		return text
	}
	var out strings.Builder
	col := 0
	for _, r := range text {
		w := lipgloss.Width(string(r))
		if col+w > lineWidth {
			out.WriteString("\n")
			out.WriteString(strings.Repeat(" ", hangIndent))
			col = 0
		}
		out.WriteRune(r)
		col += w
	}
	return out.String()
}

