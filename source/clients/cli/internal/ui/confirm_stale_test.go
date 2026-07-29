package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestConfirmStale_ReconnectMarksToolGateStale drives a tool-permission gate
// through an agent restart: while the y/n/d/c prompt is up, the agent goes
// Reconnecting then Connected. The gate's paused tool call died with the old
// process, so the model must mark the confirm stale and tell the user the
// decision was lost.
func TestConfirmStale_ReconnectMarksToolGateStale(t *testing.T) {
	m := New(nil, false)
	m.connState = agentclient.ConnStateReconnecting
	m.lastSubmittedPrompt = "push"
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(connStateChangedMsg{state: agentclient.ConnStateConnected, attempt: 3})
	nm := next.(Model)

	if nm.pendingConfirm == nil {
		t.Fatal("confirm gate should survive reconnect so the user can still answer")
	}
	if !nm.pendingConfirm.stale {
		t.Fatal("reconnect must mark the tool gate stale — its server-side turn is dead")
	}
	found := false
	for _, e := range nm.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "lost when the agent restarted") {
			found = true
		}
	}
	if !found {
		t.Fatal("reconnect should explain the tool decision was lost")
	}
}

// TestConfirmStale_YesReRunsOriginalPrompt: answering yes to a stale gate must
// NOT fire AllowToolCall (which would resolve nothing) — it re-submits the
// user's original prompt as a fresh turn instead of orphaning the call.
func TestConfirmStale_YesReRunsOriginalPrompt(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "push"
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.pendingConfirm.stale = true

	next, _ := m.resolveConfirmKey("y")

	if next.pendingConfirm != nil {
		t.Fatal("yes on a stale gate should clear the confirm")
	}
	sawReRun := false
	sawUserTurn := false
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "re-running your request") {
			sawReRun = true
		}
		if e.Role == RoleUser && strings.Contains(e.Content, "push") {
			sawUserTurn = true
		}
	}
	if !sawReRun {
		t.Error("yes on a stale gate should announce the re-run")
	}
	if !sawUserTurn {
		t.Error("yes on a stale gate should re-submit the original prompt as a new user turn")
	}
}

// TestConfirmStale_NoDropsGate: no/esc on a stale gate clears it with a note,
// and does not re-run anything.
func TestConfirmStale_NoDropsGate(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "push"
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.pendingConfirm.stale = true

	next, _ := m.resolveConfirmKey("n")

	if next.pendingConfirm != nil {
		t.Fatal("no on a stale gate should clear the confirm")
	}
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleUser {
			t.Fatal("no on a stale gate must not re-submit the prompt")
		}
	}
	found := false
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "dropped the lost tool decision") {
			found = true
		}
	}
	if !found {
		t.Error("no on a stale gate should note the drop")
	}
}
