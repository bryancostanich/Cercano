// Package render hosts the dedicated render primitives the cercano CLI relies
// on instead of passing raw output through to the terminal: Tables, diffs,
// tool-call sub-frames. The Table primitive is the non-negotiable from the
// design spec — markdown tables from the agent get intercepted and routed
// through this so they never scramble on a narrow terminal.
package render

import (
	"strings"
	"unicode"
	"unicode/utf8"

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

// minWrapWidth is the narrowest a wrappable column shrinks to before the table
// gives up on a grid and transposes instead. It is a legibility floor as much
// as a fit floor: below ~16 columns, prose wraps into shredded 1-2 word lines
// and hard-breaks mid-word, so the transposed key:value layout reads better.
const minWrapWidth = 16

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
	// Too wide at natural widths → shrink the wrappable columns so their cells
	// wrap across lines. The overflow is distributed proportionally across all
	// wrappable columns (down to minWrapWidth each); rigid columns keep their
	// natural width. This lets a wide decision matrix — several long option
	// columns — still render as a grid instead of collapsing to a transpose.
	if shrinkWrappable(t.Cols, widths, maxWidth) && totalGridWidth(widths) <= maxWidth {
		return renderGrid(t.Cols, widths, t.Rows, styles)
	}
	// Still too wide for a grid → responsive transpose; every column survives.
	return renderTransposed(t.Cols, t.Rows, maxWidth, styles)
}

// RenderMarkdown preserves Table's custom responsive geometry while rendering
// each header and cell through the Markdown renderer. The table still decides
// grid-vs-transpose, wrapping, padding, and borders; Markdown only supplies the
// styled inline/block content inside each cell.
func (t Table) RenderMarkdown(maxWidth int, styles theme.Styles, md *Markdown) string {
	if md == nil {
		return t.Render(maxWidth, styles)
	}
	if len(t.Cols) == 0 || len(t.Rows) == 0 {
		return styles.Muted.Render("(empty table)")
	}

	widths := computeMarkdownColWidths(t.Cols, t.Rows, md)
	if totalGridWidth(widths) <= maxWidth {
		return renderMarkdownGrid(t.Cols, widths, t.Rows, styles, md)
	}
	if shrinkWrappable(t.Cols, widths, maxWidth) && totalGridWidth(widths) <= maxWidth {
		return renderMarkdownGrid(t.Cols, widths, t.Rows, styles, md)
	}
	return renderMarkdownTransposed(t.Cols, t.Rows, maxWidth, styles, md)
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

func computeMarkdownColWidths(cols []Column, rows []map[string]string, md *Markdown) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = maxRenderedLineWidth(renderTableMarkdown(c.Name, 512, md))
	}
	for _, r := range rows {
		for i, c := range cols {
			cw := maxRenderedLineWidth(renderTableMarkdown(r[c.Name], 512, md))
			if cw > w[i] {
				w[i] = cw
			}
		}
	}
	return w
}

// shrinkWrappable reduces the widths of wrappable columns so the grid fits
// within maxWidth, distributing the required reduction proportionally to each
// wrappable column's current width. No column shrinks below minWrapWidth. It
// mutates widths in place and reports whether any shrinking was possible
// (false when there are no wrappable columns wider than the floor).
func shrinkWrappable(cols []Column, widths []int, maxWidth int) bool {
	// Slack each wrappable column can still give up before hitting the floor.
	type wcol struct{ idx, slack int }
	var wraps []wcol
	totalSlack := 0
	for i, c := range cols {
		if c.Wrappable && widths[i] > minWrapWidth {
			s := widths[i] - minWrapWidth
			wraps = append(wraps, wcol{i, s})
			totalSlack += s
		}
	}
	if len(wraps) == 0 {
		return false
	}
	over := totalGridWidth(widths) - maxWidth
	if over <= 0 {
		return true
	}
	// Can't fit even at the floor; shrink everything to the floor and let the
	// caller fall through to transpose.
	if over >= totalSlack {
		for _, w := range wraps {
			widths[w.idx] = minWrapWidth
		}
		return true
	}
	// First pass: distribute the overflow proportionally to each column's slack.
	// Integer division rounds every share DOWN, so this pass always under-shrinks
	// by a few columns; we track how much is left over.
	remaining := over
	for _, w := range wraps {
		take := over * w.slack / totalSlack
		if take > w.slack {
			take = w.slack
		}
		widths[w.idx] -= take
		remaining -= take
	}
	// Second pass: redistribute the rounding leftover across columns that still
	// have slack, one column at a time. Because we only reach here when
	// over < totalSlack (a floor-fit exists), enough slack remains to absorb the
	// leftover in full, so the grid is guaranteed to fit. This replaces the old
	// "last column absorbs the remainder" scheme, which capped that column's
	// take at its own (possibly tiny) slack and silently leaked the rest —
	// leaving the grid a couple of columns over budget and forcing a needless
	// transpose (the VERIFROG - GTM screenshot regression).
	for remaining > 0 {
		progressed := false
		for _, w := range wraps {
			if remaining == 0 {
				break
			}
			if widths[w.idx] > minWrapWidth {
				widths[w.idx]--
				remaining--
				progressed = true
			}
		}
		if !progressed {
			break // no column has slack left; caller falls through to transpose
		}
	}
	return true
}

func renderGrid(cols []Column, widths []int, rows []map[string]string, styles theme.Styles) string {
	var b strings.Builder

	// Top border: ┌──┬──┐
	b.WriteString(styles.Border.Render(borderRow("┌", "┬", "┐", "─", widths)))
	b.WriteString("\n")

	// Header row. Header cells wrap to their column width exactly like data
	// cells — a wrappable column can be shrunk below its name's natural width
	// by shrinkWrappable, so the header name must break across lines too rather
	// than overflow the cell border.
	headerLines := make([][]string, len(cols))
	headerH := 1
	for i, c := range cols {
		headerLines[i] = wrapCell(c.Name, widths[i])
		if len(headerLines[i]) > headerH {
			headerH = len(headerLines[i])
		}
	}
	for k := 0; k < headerH; k++ {
		b.WriteString(styles.Border.Render("│"))
		for i := range cols {
			cell := ""
			if k < len(headerLines[i]) {
				cell = headerLines[i][k]
			}
			cell = padCell(cell, widths[i])
			b.WriteString(" ")
			b.WriteString(styles.Accent.Render(cell))
			b.WriteString(" ")
			b.WriteString(styles.Border.Render("│"))
		}
		b.WriteString("\n")
	}

	// Header separator: ├──┼──┤
	b.WriteString(styles.Border.Render(borderRow("├", "┼", "┤", "─", widths)))
	b.WriteString("\n")

	// Data rows. Cells wrap (never truncate) to their column width, so a row
	// can span multiple terminal lines; non-wrapping cells sit on the first
	// line with blanks beneath.
	for rowIdx, r := range rows {
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
		if rowIdx < len(rows)-1 {
			b.WriteString(styles.Border.Render(borderRow("├", "┼", "┤", "─", widths)))
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

func renderMarkdownGrid(cols []Column, widths []int, rows []map[string]string, styles theme.Styles, md *Markdown) string {
	var b strings.Builder

	b.WriteString(styles.Border.Render(borderRow("┌", "┬", "┐", "─", widths)))
	b.WriteString("\n")

	headerLines := make([][]string, len(cols))
	headerH := 1
	for i, c := range cols {
		headerLines[i] = markdownCellLines(c.Name, widths[i], md)
		if len(headerLines[i]) > headerH {
			headerH = len(headerLines[i])
		}
	}
	for k := 0; k < headerH; k++ {
		b.WriteString(styles.Border.Render("│"))
		for i := range cols {
			cell := ""
			if k < len(headerLines[i]) {
				cell = headerLines[i][k]
			}
			b.WriteString(" ")
			b.WriteString(padCell(cell, widths[i]))
			b.WriteString(" ")
			b.WriteString(styles.Border.Render("│"))
		}
		b.WriteString("\n")
	}

	b.WriteString(styles.Border.Render(borderRow("├", "┼", "┤", "─", widths)))
	b.WriteString("\n")

	for rowIdx, r := range rows {
		cellLines := make([][]string, len(cols))
		rowH := 1
		for i, c := range cols {
			cellLines[i] = markdownCellLines(r[c.Name], widths[i], md)
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
				b.WriteString(" ")
				b.WriteString(padCell(cell, widths[i]))
				b.WriteString(" ")
				b.WriteString(styles.Border.Render("│"))
			}
			b.WriteString("\n")
		}
		if rowIdx < len(rows)-1 {
			b.WriteString(styles.Border.Render(borderRow("├", "┼", "┤", "─", widths)))
			b.WriteString("\n")
		}
	}

	b.WriteString(styles.Border.Render(borderRow("└", "┴", "┘", "─", widths)))
	return b.String()
}

func renderMarkdownTransposed(cols []Column, rows []map[string]string, maxWidth int, styles theme.Styles, md *Markdown) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(styles.Border.Render(strings.Repeat("─", min(maxWidth, 40))))
			b.WriteString("\n")
		}
		for _, c := range cols {
			label := styles.Accent.Render(c.Name + ":")
			labelW := lipgloss.Width(c.Name) + 2
			wrapAt := maxWidth - labelW
			if wrapAt < 10 {
				wrapAt = 10
			}
			val := renderTableMarkdown(r[c.Name], wrapAt, md)
			b.WriteString(label)
			if strings.TrimSpace(stripANSIForTable(val)) == "" {
				b.WriteString("\n")
				continue
			}
			if strings.Contains(val, "\n") || lipgloss.Width(label)+1+maxRenderedLineWidth(val) > maxWidth {
				b.WriteString("\n")
				b.WriteString(indentRenderedLines(val, 2))
				b.WriteString("\n")
			} else {
				b.WriteString(" ")
				b.WriteString(val)
				b.WriteString("\n")
			}
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
	w := visibleTableWidth(val)
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

func markdownCellLines(s string, width int, md *Markdown) []string {
	if width < 1 {
		width = 1
	}
	rendered := renderTableMarkdown(s, width, md)
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	for i, line := range lines {
		if visibleTableWidth(line) <= width {
			continue
		}
		// Glamour should respect WithWordWrap(width), but keep table geometry safe
		// if a long unbreakable rendered span still exceeds the cell.
		wrappedPlain := wrapCell(stripANSIForTable(line), width)
		replacement := make([]string, len(wrappedPlain))
		copy(replacement, wrappedPlain)
		lines = append(lines[:i], append(replacement, lines[i+1:]...)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func renderTableMarkdown(s string, width int, md *Markdown) string {
	if md == nil {
		return s
	}
	if width < 1 {
		width = 1
	}
	// A trailing newline nudges Glamour/Goldmark to treat the fragment as a
	// complete paragraph while Render trims Glamour's trailing newlines back off.
	// Glamour can also pad rendered paragraphs out to the wrap width; trim that
	// padding per line so cell width decisions reflect visible content, not the
	// fragment renderer's fill spaces.
	return trimRenderedLinePadding(strings.Trim(md.Render(s+"\n", width), "\n"))
}

func trimRenderedLinePadding(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = trimTrailingVisibleSpace(line)
	}
	return strings.Join(lines, "\n")
}

func trimTrailingVisibleSpace(s string) string {
	end := len(s)
	for {
		i, r, ok := lastVisibleRuneBefore(s, end)
		if !ok {
			return ""
		}
		if !unicode.IsSpace(r) {
			return s[:end]
		}
		end = i
	}
}

func maxRenderedLineWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := visibleTableWidth(line); w > max {
			max = w
		}
	}
	return max
}

func visibleTableWidth(s string) int {
	return lipgloss.Width(strings.TrimRight(stripANSIForTable(s), " \t"))
}

func indentRenderedLines(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

func stripANSIForTable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if j, ok := skipTerminalControl(s, i); ok {
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func lastVisibleRuneBefore(s string, end int) (int, rune, bool) {
	lastI := -1
	var lastR rune
	for i := 0; i < end; {
		if j, ok := skipTerminalControl(s[:end], i); ok {
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:end])
		if r == utf8.RuneError && size == 0 {
			break
		}
		lastI = i
		lastR = r
		i += size
	}
	if lastI < 0 {
		return 0, 0, false
	}
	return lastI, lastR, true
}

func skipTerminalControl(s string, i int) (int, bool) {
	if i+1 >= len(s) || s[i] != 0x1b {
		return i, false
	}
	switch s[i+1] {
	case '[': // CSI: SGR colors, cursor/control sequences.
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			return j + 1, true
		}
	case ']': // OSC: Glamour emits OSC-8 hyperlinks for markdown links.
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 { // BEL terminator
				return j + 1, true
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST terminator
				return j + 2, true
			}
			j++
		}
	}
	return i, false
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
