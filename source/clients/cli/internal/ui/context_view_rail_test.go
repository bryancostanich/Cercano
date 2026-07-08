package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// Expanded /c turns get the same collapse rail as the main chat's tool
// expand: │ down the body's left gutter, ╰ on the last line, and a click on
// that gutter collapses the turn.
func TestContextView_ExpandedTurnRailAndCollapse(t *testing.T) {
	p := theme.Cracker()
	cv := &contextView{
		palette:  p,
		styles:   theme.NewStyles(p),
		convID:   "conv-rail",
		width:    100,
		height:   40,
		expanded: map[string]bool{},
		md:       render.NewMarkdown(theme.MarkdownStyle(p)),
	}
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "t1", Role: "user", Kind: "tool_result",
			Body: "line one\nline two\nline three\nline four\nline five"},
	}
	cv.expanded["t1"] = true

	lines, meta := cv.turnsLines()
	if len(lines) != len(meta) {
		t.Fatalf("lines/meta length mismatch: %d vs %d", len(lines), len(meta))
	}

	// Collect the rail-marked body lines.
	var railIdx []int
	for i, m := range meta {
		if m.railCell {
			railIdx = append(railIdx, i)
		}
	}
	if len(railIdx) < 2 {
		t.Fatalf("expected >=2 railCell body lines, got %d", len(railIdx))
	}

	// Every railed line carries the rail glyph at column 2: │ on all but the
	// last, ╰ on the last.
	for n, i := range railIdx {
		plain := ansi.Strip(lines[i])
		want := "│"
		if n == len(railIdx)-1 {
			want = "╰"
		}
		if !strings.HasPrefix(plain, "  "+want) {
			t.Errorf("body line %d = %q, want rail %q at col 2", i, plain, want)
		}
	}

	// A click on the rail gutter collapses the turn.
	yLocal := railIdx[1] - cv.scrollOffset
	if !cv.handleClick(2, yLocal) {
		t.Fatal("rail gutter click should be claimed")
	}
	if cv.expanded["t1"] {
		t.Error("rail click should collapse the turn")
	}

	// Re-expand: a click on body CONTENT (x=10) must not collapse.
	cv.expanded["t1"] = true
	if cv.handleClick(10, yLocal) {
		t.Error("content click should not be claimed")
	}
	if !cv.expanded["t1"] {
		t.Error("content click must not collapse the turn")
	}

	// Collapsed turns carry no rail metadata.
	cv.expanded["t1"] = false
	_, meta2 := cv.turnsLines()
	for i, m := range meta2 {
		if m.railCell {
			t.Errorf("collapsed turn should have no railCell lines (line %d)", i)
		}
	}
}
