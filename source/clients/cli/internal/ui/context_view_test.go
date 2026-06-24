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
