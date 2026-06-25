package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func expandTestView() *contextView {
	cv := &contextView{
		width: 70, height: 30,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1", expanded: map[string]bool{},
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
