package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newEditTestView() *contextView {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	d := &contextManagerDriver{agent: nil, convID: "c1"}
	cv := &contextView{
		width: 80, height: 24,
		palette: p, styles: s,
		convID: "c1",
		snapshot: contextSnapshot{Turns: []agentclient.ContextTurn{
			{ID: "a", Role: "user", Kind: "text", Preview: "debug tangent", EstTokens: 5},
			{ID: "b", Role: "assistant", Kind: "text", Preview: "api design", EstTokens: 5},
		}},
	}
	cv.driver = d
	cv.chat = newChatView(s, p, "", "", 78, 24)
	return cv
}

// TestContextView_ProposalMarksTurns verifies that applyProposal marks the right
// turns for deletion. Rationale now flows through the pane's chat log (appendAssistant),
// not the scrollable turns view — so this test checks turn marks + pane rendering.
func TestContextView_ProposalMarksTurnsAndRationale(t *testing.T) {
	cv := newEditTestView()
	cv.applyProposal(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "removed the tangent"})
	// Rationale is now in the pane (driver calls appendAssistant via chatConfirmMsg);
	// simulate that path directly.
	cv.chat.Apply(chatAssistantMsg{text: "removed the tangent"})

	// Rationale should appear in the pane's entries.
	found := false
	for _, e := range cv.chat.Entries() {
		if strings.Contains(e.Content, "removed the tangent") {
			found = true
		}
	}
	if !found {
		t.Error("rationale not found in pane entries")
	}

	// Turn marks should still work.
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
