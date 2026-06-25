package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// Every row of the /c scrollable region must be the same visible width so the
// scrollbar glyph sits at a fixed far-right column, not at each row's text-end.
func TestContextView_ScrollbarFixedColumn(t *testing.T) {
	cv := &contextView{width: 60, height: 30, palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker())}
	full := "short\n" + strings.Repeat("x", 200) + "\n[tool_use] ≈65 Read foo\nz"
	out := cv.renderScrollableContent(full, 4)
	rows := strings.Split(out, "\n")
	wantW := dashboardPanelWidth(60) + 2 // padded content + 1 gap + 1 scrollbar col
	for i, r := range rows {
		if w := ansi.StringWidth(r); w != wantW {
			t.Errorf("row %d width=%d want %d (scrollbar not at fixed column): %q", i, w, wantW, ansi.Strip(r))
		}
	}
}
