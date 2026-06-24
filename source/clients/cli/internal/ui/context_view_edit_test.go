package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newEditTestView() *contextView {
	return &contextView{
		width: 80, height: 24,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1",
		snapshot: contextSnapshot{Turns: []agentclient.ContextTurn{
			{ID: "a", Role: "user", Kind: "text", Preview: "debug tangent", EstTokens: 5},
			{ID: "b", Role: "assistant", Kind: "text", Preview: "api design", EstTokens: 5},
		}},
	}
}

func TestContextView_ProposalMarksTurnsAndRationale(t *testing.T) {
	cv := newEditTestView()
	cv.applyProposal(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "removed the tangent"})
	out := cv.View()
	if !strings.Contains(out, "removed the tangent") {
		t.Errorf("rationale not shown:\n%s", out)
	}
	if !cv.markedForDelete("a") || cv.markedForDelete("b") {
		t.Errorf("wrong turns marked: a=%v b=%v", cv.markedForDelete("a"), cv.markedForDelete("b"))
	}
}

func TestContextView_CancelProposalClears(t *testing.T) {
	cv := newEditTestView()
	cv.applyProposal(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "x"})
	cv.cancelProposal()
	if cv.markedForDelete("a") {
		t.Error("cancel did not clear the proposal")
	}
}
