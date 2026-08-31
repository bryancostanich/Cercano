package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newTestContextView(snap contextSnapshot) *contextView {
	return &contextView{
		width: 80, height: 24,
		palette:  theme.Cracker(),
		styles:   theme.NewStyles(theme.Cracker()),
		convID:   "c1",
		snapshot: snap,
	}
}

func TestContextView_RendersTurnsAndTotal(t *testing.T) {
	cv := newTestContextView(contextSnapshot{
		Turns: []agentclient.ContextTurn{
			{Role: "user", Kind: "text", Preview: "hello there", EstTokens: 12},
			{Role: "assistant", Kind: "text", Preview: "hi back", EstTokens: 8},
		},
		Usage: &agentclient.ContextUsage{TokensUsed: 4321, ModelMax: 200000, Percent: 0.0216},
	})
	out := cv.View()
	for _, want := range []string{"hello there", "hi back", "4,321", "200,000"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n%s", want, out)
		}
	}
}

func TestContextView_HeaderShowsEstimatedRequestAccounting(t *testing.T) {
	cv := newTestContextView(contextSnapshot{
		Usage: &agentclient.ContextUsage{
			TokensUsed:             95000,
			ModelMax:               128000,
			RawTokens:              400000,
			MessageTokens:          95000,
			SystemTokens:           3000,
			ToolSchemaTokens:       12000,
			OutputReserveTokens:    4096,
			EstimatedRequestTokens: 114096,
			ContextWindowKnown:     false,
		},
	})
	out := stripAnsiCSI(cv.renderHeader())
	for _, want := range []string{"context est", "114,096", "128,000", "95,000 messages", "3,000 system", "12,000 tools", "4,096 output", "window estimated"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q\n%s", want, out)
		}
	}
}

func TestContextView_HeaderKeepsLegacyUsageFallback(t *testing.T) {
	cv := newTestContextView(contextSnapshot{
		Usage: &agentclient.ContextUsage{TokensUsed: 4321, ModelMax: 200000, Percent: 0.0216, ContextWindowKnown: true},
	})
	out := stripAnsiCSI(cv.renderHeader())
	if !strings.Contains(out, "context") || !strings.Contains(out, "4,321") || !strings.Contains(out, "200,000") {
		t.Fatalf("legacy header missing usage fields\n%s", out)
	}
	if strings.Contains(out, "context est") || strings.Contains(out, "messages") || strings.Contains(out, "window estimated") {
		t.Fatalf("legacy header should not show estimated accounting labels\n%s", out)
	}
}

func TestContextView_EmptyAndNoConversation(t *testing.T) {
	if got := newTestContextView(contextSnapshot{}).View(); !strings.Contains(got, "context is empty") {
		t.Errorf("empty state: %q", got)
	}
	noConv := newTestContextView(contextSnapshot{})
	noConv.convID = ""
	if got := noConv.View(); !strings.Contains(got, "no conversation yet") {
		t.Errorf("no-conversation state: %q", got)
	}
}

func TestContextView_ScrollState(t *testing.T) {
	turns := make([]agentclient.ContextTurn, 100)
	for i := range turns {
		turns[i] = agentclient.ContextTurn{Role: "user", Kind: "text", Preview: "line", EstTokens: 1}
	}
	cv := newTestContextView(contextSnapshot{Turns: turns, Usage: &agentclient.ContextUsage{ModelMax: 1000}})
	st0 := cv.ScrollState()
	cv.ScrollBy(10)
	if cv.ScrollState().Offset <= st0.Offset {
		t.Errorf("ScrollBy did not advance offset: %d -> %d", st0.Offset, cv.ScrollState().Offset)
	}
}

func TestContextTurns_SentViewHidesFrozenShowsSummary(t *testing.T) {
	cv := expandTestView()
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "user", Kind: "text", Preview: "FROZEN-1"},
		{ID: "b", Role: "assistant", Kind: "text", Preview: "FROZEN-2"},
		{ID: "c", Role: "user", Kind: "text", Preview: "LIVE-1"},
	}
	cv.snapshot.Compaction = &agentclient.CompactionState{
		FrozenTurns: 2, LiveTurns: 1,
		ConsolidatedSummary: "[conversation summary]\nGoal: SUMMARY-GOAL",
	}
	out := stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("sent view should show the consolidated summary")
	}
	if !strings.Contains(out, "LIVE-1") {
		t.Error("sent view should show live turns")
	}
	if strings.Contains(out, "FROZEN-1") || strings.Contains(out, "FROZEN-2") {
		t.Error("sent view must hide frozen turns (they're in the summary)")
	}

	cv.showOriginal = true
	out = stripAnsiCSI(strings.Join(cv.turnsLinesOnly(), "\n"))
	if !strings.Contains(out, "FROZEN-1") || strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("original view should show all turns and no summary")
	}
}

func TestFocusNextExpandable_SkipsFrozenInSentView(t *testing.T) {
	cv := expandTestView()
	// Two frozen turns (hidden in sent view) + one live, all expandable.
	long := "L1\nL2\nL3\nL4\nL5\nL6"
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "a", Role: "assistant", Kind: "text", Body: long}, // frozen index 0
		{ID: "b", Role: "assistant", Kind: "text", Body: long}, // frozen index 1
		{ID: "c", Role: "assistant", Kind: "text", Body: long}, // live index 2
	}
	cv.snapshot.Compaction = &agentclient.CompactionState{FrozenTurns: 2, ConsolidatedSummary: "Goal: G"}

	// Tab from no focus must land on the first VISIBLE turn (index 2), never a
	// hidden frozen one (0 or 1).
	cv.focusNextExpandable(+1)
	if cv.focusedTurn != 2 {
		t.Errorf("sent view: tab should focus the first live turn (2), got %d", cv.focusedTurn)
	}
	// Shift+Tab from no focus also lands on a visible turn.
	cv.focusedTurn = -1
	cv.focusNextExpandable(-1)
	if cv.focusedTurn < 2 {
		t.Errorf("sent view: shift+tab must not focus a hidden frozen turn, got %d", cv.focusedTurn)
	}
	// In original view all turns are focusable again.
	cv.showOriginal = true
	cv.focusedTurn = -1
	cv.focusNextExpandable(+1)
	if cv.focusedTurn != 0 {
		t.Errorf("original view: tab should focus turn 0, got %d", cv.focusedTurn)
	}
}
