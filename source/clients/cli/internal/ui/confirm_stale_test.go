package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// TestConfirmStale_YesUsesCapturedRetryPrompt reproduces the screenshot: the
// stream close can clear lastSubmittedPrompt before reconnect marks the gate
// stale. The pending gate must keep its own copy of the prompt so [y] still
// has something to re-run.
func TestConfirmStale_YesUsesCapturedRetryPrompt(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "fixed. push"
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.pendingConfirm.retryPrompt = m.lastSubmittedPrompt
	m.pendingConfirm.stale = true
	m.lastSubmittedPrompt = ""

	next, _ := m.resolveConfirmKey("y")

	if next.pendingConfirm != nil {
		t.Fatal("yes on a stale gate should clear the confirm")
	}
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "nothing to re-run") {
			t.Fatal("yes should not lose the captured retry prompt")
		}
	}
	found := false
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleUser && strings.Contains(e.Content, "fixed. push") {
			found = true
		}
	}
	if !found {
		t.Fatal("yes should re-submit the captured retry prompt")
	}
}

// TestConfirmStale_TextSubmitsFreshPrompt ensures ordinary typing is not
// swallowed by the stale y/n gate. A dead tool call cannot receive chat
// steering, so typed text should become a fresh request after reconnect.
func TestConfirmStale_TextSubmitsFreshPrompt(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.pendingConfirm.retryPrompt = "fixed. push"
	m.pendingConfirm.stale = true
	m.input.SetValue("show summary of changes")

	nextModel, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := nextModel.(Model)

	if next.pendingConfirm != nil {
		t.Fatal("typing a fresh prompt after reconnect should clear the stale confirm")
	}
	found := false
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleUser && strings.Contains(e.Content, "show summary of changes") {
			found = true
		}
	}
	if !found {
		t.Fatal("typed text should submit as a fresh user turn")
	}
}
