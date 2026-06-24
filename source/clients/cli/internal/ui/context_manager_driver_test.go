package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestContextManagerDriver_ProposalToConfirm verifies that a proposal with
// DeleteIDs produces a chatConfirmMsg with assistant text and both callbacks.
func TestContextManagerDriver_ProposalToConfirm(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "removed tangent"}, nil)
	cm, ok := msg.(chatConfirmMsg)
	if !ok {
		t.Fatalf("want chatConfirmMsg, got %T", msg)
	}
	if cm.assistant == "" || cm.onYes == nil || cm.onNo == nil {
		t.Errorf("confirm msg incomplete: %+v", cm)
	}
}

// TestContextManagerDriver_ProposalError verifies that a propose error yields chatErrorMsg.
func TestContextManagerDriver_ProposalError(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{}, errString("nope"))
	if _, ok := msg.(chatErrorMsg); !ok {
		t.Fatalf("want chatErrorMsg on error, got %T", msg)
	}
}

// TestContextManagerDriver_EmptyProposalDone verifies that an empty DeleteIDs
// proposal yields chatDoneMsg with non-empty text.
func TestContextManagerDriver_EmptyProposalDone(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{DeleteIDs: nil, Rationale: "nothing"}, nil)
	if dm, ok := msg.(chatDoneMsg); !ok || dm.text == "" {
		t.Fatalf("want chatDoneMsg with text on empty proposal, got %T", msg)
	}
}
