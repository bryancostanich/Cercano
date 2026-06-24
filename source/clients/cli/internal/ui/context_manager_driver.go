package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// contextManagerDriver is the first ChatDriver: it edits the conversation's
// context via ProposeContextEdit (→ a confirm) and DeleteConversationTurns.
type contextManagerDriver struct {
	agent  *agentclient.Client
	convID string
	// onDeleted is set by the /c page so it can reload its turns list and mark
	// proposals after a delete.
	onDeleted func(ids []string)
	// mark/unmark let the driver tell the /c page which turns a live proposal
	// targets (so they render with ✗). Optional.
	mark   func(ids []string)
	unmark func()
}

func (d *contextManagerDriver) Name() string { return "context manager" }

func (d *contextManagerDriver) Submit(ctx context.Context, input string) tea.Cmd {
	ag, convID := d.agent, d.convID
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, err := ag.ProposeContextEdit(c, convID, input)
		return d.proposalToMsg(p, err)
	}
}

// proposalToMsg maps a propose result to the right pane event. Pure (no I/O) so
// it is unit-testable.
func (d *contextManagerDriver) proposalToMsg(p agentclient.Proposal, err error) tea.Msg {
	if err != nil {
		return chatErrorMsg{err: err}
	}
	if len(p.DeleteIDs) == 0 {
		return chatDoneMsg{text: "nothing to remove."}
	}
	ids := p.DeleteIDs
	return chatConfirmMsg{
		assistant: p.Rationale + fmt.Sprintf("  (will remove %d turn(s) — y/n)", len(ids)),
		onYes:     d.deleteCmd(ids),
		onNo:      func() tea.Msg { return chatDoneMsg{text: "kept everything."} },
	}
}

func (d *contextManagerDriver) deleteCmd(ids []string) tea.Cmd {
	ag, convID := d.agent, d.convID
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		n, err := ag.DeleteConversationTurns(c, convID, ids)
		if err != nil {
			return chatErrorMsg{err: err}
		}
		if d.onDeleted != nil {
			d.onDeleted(ids)
		}
		return chatDoneMsg{text: fmt.Sprintf("removed %d turn(s).", n)}
	}
}
