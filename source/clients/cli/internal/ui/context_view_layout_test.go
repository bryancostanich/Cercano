package ui

import (
	"fmt"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// With an empty chat pane, the turns list must fill most of the panel — the bug
// was the pane being sized to the full panel height, eating the space (and
// nesting a second scrollbar) so only a few turns showed below a big empty area.
func TestContextView_SplitLayout_EmptyPaneDoesNotEatPanel(t *testing.T) {
	cv := &contextView{
		width: 80, height: 30,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1",
	}
	for i := 0; i < 40; i++ {
		cv.snapshot.Turns = append(cv.snapshot.Turns, agentclient.ContextTurn{
			ID: fmt.Sprint(i), Role: "user", Preview: fmt.Sprintf("turn-%d", i), EstTokens: 1,
		})
	}
	cv.snapshot.Usage = &agentclient.ContextUsage{ModelMax: 1000}
	cv.pane = newChatPane(&contextManagerDriver{}, cv.styles, cv.palette, cv.width, cv.height) // empty

	out := stripAnsiCSI(cv.View())
	lines := strings.Split(out, "\n")
	totalH := dashboardContentHeight(30)
	if len(lines) != totalH {
		t.Fatalf("View height = %d lines, want %d (no overflow/underflow)", len(lines), totalH)
	}
	// Empty pane → turns fill most of the panel.
	turnCount := strings.Count(out, "turn-")
	if turnCount < totalH/2 {
		t.Errorf("empty chat pane should not eat the panel: only %d turn lines visible of height %d", turnCount, totalH)
	}
}

// A pane with several messages grows (up to half the panel) and the turns region
// shrinks accordingly — but never to zero, and total height is preserved.
func TestContextView_SplitLayout_PaneGrowsCapped(t *testing.T) {
	cv := &contextView{
		width: 80, height: 30,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1",
	}
	cv.snapshot.Turns = []agentclient.ContextTurn{{ID: "a", Role: "user", Preview: "ctx", EstTokens: 1}}
	cv.snapshot.Usage = &agentclient.ContextUsage{ModelMax: 1000}
	cv.pane = newChatPane(&contextManagerDriver{}, cv.styles, cv.palette, cv.width, cv.height)
	for i := 0; i < 50; i++ {
		cv.pane.Apply(chatAssistantMsg{text: fmt.Sprintf("msg-%d", i)})
	}
	out := cv.View()
	lines := strings.Split(out, "\n")
	totalH := dashboardContentHeight(30)
	if len(lines) != totalH {
		t.Fatalf("View height = %d, want %d", len(lines), totalH)
	}
	// chat content visible, capped at half the panel
	stripped := stripAnsiCSI(out)
	if !strings.Contains(stripped, "msg-49") { // stuck to bottom → latest visible
		t.Errorf("latest chat message should be visible (stick-to-bottom):\n%s", stripped)
	}
	// turns region still present (header/ctx not fully crowded out)
	if !strings.Contains(stripped, "ctx") {
		t.Error("turns region should still render its content")
	}
}
