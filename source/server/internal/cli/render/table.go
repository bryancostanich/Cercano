// Package render hosts the dedicated render primitives the cercano CLI relies
// on instead of passing raw output through to the terminal: Tables, diffs,
// tool-call sub-frames. The Table primitive is the non-negotiable from the
// design spec — markdown tables from the agent get intercepted and routed
// through this so they never scramble on a narrow terminal.
package render

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/server/internal/cli/theme"
)

// Column declares one table column. Priority orders drop behavior: when the
// table is too wide for the terminal, columns are dropped from lowest Priority
// upward until the rest fits.
type Column struct {
	Name      string
	Priority  int  // lower drops first
	Wrappable bool // if true, this column is the one we truncate with `…` before transposing
}

// Table is the typed alternative to raw markdown tables. Rows are keyed by
// Column.Name.
type Table struct {
	Cols []Column
	Rows []map[string]string
}

// Render returns a styled string fitting within maxWidth columns. Width-fit
// rules (in order): drop lowest-priority columns → truncate the wrappable
// column with `…` → transpose to key:value pairs. Always readable; never
// scrambled.
func (t Table) Render(maxWidth int, styles theme.Styles) string {
	if len(t.Cols) == 0 || len(t.Rows) == 0 {
		return styles.Muted.Render("(empty table)")
	}

	// Start with all columns; drop in priority order until it fits.
	cols := append([]Column(nil), t.Cols...)
	dropped := []string{}

	for {
		widths := computeColWidths(cols, t.Rows)
		total := totalGridWidth(widths)
		if total <= maxWidth {
			return renderGrid(cols, widths, t.Rows, styles, dropped)
		}
		// Try truncating the wrappable column to fit, before dropping more.
		if i := wrappableIdx(cols); i >= 0 && widths[i] > 8 {
			over := total - maxWidth
			newW := widths[i] - over
			if newW < 6 {
				newW = 6
			}
			widths[i] = newW
			total = totalGridWidth(widths)
			if total <= maxWidth {
				return renderGrid(cols, widths, t.Rows, styles, dropped)
			}
		}
		// Drop the lowest-priority column.
		idx := lowestPriorityIdx(cols)
		if idx < 0 || len(cols) <= 1 {
			break // can't drop further; fall through to transpose
		}
		dropped = append(dropped, cols[idx].Name)
		cols = append(cols[:idx], cols[idx+1:]...)
	}

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

func lowestPriorityIdx(cols []Column) int {
	if len(cols) == 0 {
		return -1
	}
	idx := 0
	for i := 1; i < len(cols); i++ {
		if cols[i].Priority < cols[idx].Priority {
			idx = i
		}
	}
	return idx
}

func renderGrid(cols []Column, widths []int, rows []map[string]string, styles theme.Styles, dropped []string) string {
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

	// Data rows.
	for _, r := range rows {
		b.WriteString(styles.Border.Render("│"))
		for i, c := range cols {
			val := r[c.Name]
			if lipgloss.Width(val) > widths[i] {
				val = truncate(val, widths[i])
			}
			cell := padCell(val, widths[i])
			b.WriteString(" ")
			b.WriteString(styles.Primary.Render(cell))
			b.WriteString(" ")
			b.WriteString(styles.Border.Render("│"))
		}
		b.WriteString("\n")
	}

	// Bottom border: └──┴──┘
	b.WriteString(styles.Border.Render(borderRow("└", "┴", "┘", "─", widths)))

	if len(dropped) > 0 {
		sort.Strings(dropped)
		b.WriteString("\n")
		b.WriteString(styles.Muted.Render("(dropped: " + strings.Join(dropped, ", ") + ")"))
	}
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

// truncate cuts val to maxWidth visible columns, ending with `…` if cut.
func truncate(val string, maxWidth int) string {
	if maxWidth <= 1 {
		return "…"
	}
	cum := 0
	for i, r := range val {
		w := lipgloss.Width(string(r))
		if cum+w > maxWidth-1 {
			return val[:i] + "…"
		}
		cum += w
	}
	return val
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

