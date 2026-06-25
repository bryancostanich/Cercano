package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func expandTestView() *contextView {
	cv := &contextView{
		width: 70, height: 30,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1", expanded: map[string]bool{}, focusedTurn: -1,
	}
	cv.snapshot.Usage = &agentclient.ContextUsage{ModelMax: 1000}
	return cv
}

func TestContextView_AssistantShowsThreeLinesAndArrow(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text",
			Body: "L1\nL2\nL3\nL4\nL5\nL6", Preview: "L1 L2 L3 L4 L5 L6"},
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "L1") || !strings.Contains(out, "L3") {
		t.Errorf("assistant should show first lines:\n%s", out)
	}
	if strings.Contains(out, "L4") {
		t.Error("collapsed assistant should NOT show line 4")
	}
	if !strings.Contains(out, "▸") {
		t.Error("overflowing turn should show a ▸ arrow")
	}
}

func TestContextView_ExpandShowsAllAndCaret(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5\nL6"},
	}
	cv.toggleExpand("a")
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "L4") || !strings.Contains(out, "L6") {
		t.Errorf("expanded should show all body lines:\n%s", out)
	}
	if !strings.Contains(out, "▾") {
		t.Error("expanded turn should show a ▾ caret")
	}
}

func TestContextView_OneLineTurnNoArrow(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "u", Role: "user", Kind: "text", Body: "short", Preview: "short"},
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if strings.Contains(out, "▸") || strings.Contains(out, "▾") {
		t.Errorf("non-overflowing turn must have no arrow:\n%s", out)
	}
}

func TestContextView_ClickArrowToggles(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5\nL6"},
	}
	_, meta := cv.turnsLines()
	// find the arrow row (header of the expandable turn)
	row := -1
	for i, m := range meta {
		if m.arrowCell {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no arrow cell found")
	}
	if !cv.handleClick(0, row) { // x=0 (arrow column), yLocal=row (scroll offset 0)
		t.Fatal("click on arrow should be handled")
	}
	if !cv.expanded["a"] {
		t.Error("click should have expanded turn a")
	}
	// click off the arrow column → no toggle
	cv.handleClick(40, row)
	if !cv.expanded["a"] {
		t.Error("off-arrow click should not collapse")
	}
}

func TestContextView_ShiftTabFocusesLastExpandable(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5"}, // expandable, index 0
		{ID: "u", Role: "user", Kind: "text", Body: "short", Preview: "short"}, // not expandable, index 1
		{ID: "b", Role: "assistant", Kind: "text", Body: "M1\nM2\nM3\nM4\nM5"}, // expandable, index 2
	}
	// shift+tab from neutral (-1) should land on last expandable turn (index 2)
	cv.focusNextExpandable(-1)
	if cv.focusedTurn != 2 {
		t.Fatalf("shift+tab from -1 should focus last expandable (index 2), got %d", cv.focusedTurn)
	}
	// another shift+tab should wrap to index 0 (skipping non-expandable index 1)
	cv.focusNextExpandable(-1)
	if cv.focusedTurn != 0 {
		t.Fatalf("second shift+tab should wrap to index 0, got %d", cv.focusedTurn)
	}
	// forward tab from index 0 should land on index 2 (skipping non-expandable index 1)
	cv.focusNextExpandable(1)
	if cv.focusedTurn != 2 {
		t.Fatalf("tab from 0 should skip non-expandable and land on 2, got %d", cv.focusedTurn)
	}
}

func TestContextView_ExpandRendersMarkdown(t *testing.T) {
	cv := expandTestView()
	cv.md = render.NewMarkdown(theme.CrackerMarkdownStyle())
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text",
			Body: "# Heading\n\nSome **bold** text and a list:\n\n- one\n- two\n- three"},
	}
	cv.toggleExpand("a")
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))

	// Markdown markup is rendered away, not shown literally.
	if strings.Contains(out, "**bold**") {
		t.Errorf("expanded assistant body should not contain literal ** markup:\n%s", out)
	}
	if strings.Contains(out, "# Heading") {
		t.Errorf("heading marker '#' should be rendered away:\n%s", out)
	}
	// The content survives the formatting.
	for _, want := range []string{"Heading", "bold", "one", "two", "three"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded markdown should contain %q:\n%s", want, out)
		}
	}
}

func TestContextView_ToolTurnStaysPlainWhenExpanded(t *testing.T) {
	cv := expandTestView()
	cv.md = render.NewMarkdown(theme.CrackerMarkdownStyle())
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "t", Role: "assistant", Kind: "tool_use",
			Body: "read_file\nwith **stars** kept\nline3\nline4\nline5"},
	}
	cv.toggleExpand("t")
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))

	// Tool turns are not markdown — the raw markup must survive verbatim.
	if !strings.Contains(out, "**stars**") {
		t.Errorf("tool turn body should stay plain (markup preserved):\n%s", out)
	}
}

func TestContextView_TabFocusEnterToggles(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	cv.expanded = map[string]bool{}
	cv.focusedTurn = -1
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: "L1\nL2\nL3\nL4\nL5"},
	}
	// tab focuses the first expandable turn
	m, _ = m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyTab})
	if cv.focusedTurn != 0 {
		t.Fatalf("tab should focus turn 0, got %d", cv.focusedTurn)
	}
	// enter with empty input toggles the focused turn (not submit)
	m.input.SetValue("")
	m, _ = m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !cv.expanded["a"] {
		t.Error("enter on focused turn (empty input) should expand it")
	}
}
