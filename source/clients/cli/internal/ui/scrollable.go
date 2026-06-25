package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// renderScrollable paints `height` rows of `lines` starting at `offset`, each
// truncated/padded to panelW, with a scrollbar glyph column appended after a
// one-space gutter. offset must already be clamped by the caller. Pure.
func renderScrollable(lines []string, height, panelW, offset int, styles theme.Styles) string {
	if height < 1 {
		height = 1
	}
	col := scrollbarColumn(len(lines), height, offset)
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if src := offset + i; src >= 0 && src < len(lines) {
			line = lines[src]
		}
		b.WriteString(padToWidth(ansi.Truncate(line, panelW, ""), panelW))
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(styles.Border.Render("█"))
			case '░':
				b.WriteString(styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
