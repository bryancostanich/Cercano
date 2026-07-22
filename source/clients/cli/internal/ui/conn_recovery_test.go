package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestConnRecovery_ReconnectedRefreshesAndAnnounces drives the
// Reconnecting → Connected transition: the model must announce the
// recovery in the scrollback and return a non-nil cmd (the batched
// re-fetch of config/tools/mode/vision/runtime snapshots, which may all
// be stale when the far end is a freshly spawned replacement server).
func TestConnRecovery_ReconnectedRefreshesAndAnnounces(t *testing.T) {
	m := New(nil, false)
	m.connState = agentclient.ConnStateReconnecting
	m.connAttempt = 5

	next, cmd := m.Update(connStateChangedMsg{state: agentclient.ConnStateConnected, attempt: 5})
	nm := next.(Model)

	if nm.connState != agentclient.ConnStateConnected {
		t.Fatalf("connState = %v, want Connected", nm.connState)
	}
	if cmd == nil {
		t.Fatal("recovery must return the re-fetch batch cmd, got nil")
	}
	found := false
	for _, e := range nm.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "agent reconnected") {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery should append a '✓ agent reconnected' system entry")
	}
}

// TestConnRecovery_SteadyConnectedIsSilent guards against the recovery
// branch firing on the initial Connected event at startup (prev is
// already Connected — the zero value): no announcement, no re-fetch.
func TestConnRecovery_PendingConfirmDoesNotRestorePromptIntoComposer(t *testing.T) {
	m := New(nil, false)
	m.connState = agentclient.ConnStateConnected
	m.streaming = true
	m.lastSubmittedPrompt = "continue"
	m.input.SetValue("")
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{"task":"check"}`, Permission: "X"})

	next, _ := m.Update(connStateChangedMsg{state: agentclient.ConnStateReconnecting, attempt: 1})
	nm := next.(Model)

	if nm.pendingConfirm == nil {
		t.Fatal("pending confirm should survive reconnecting transition")
	}
	if nm.input.Value() != "" {
		t.Fatalf("pending confirm reconnect should not restore last prompt into composer, got %q", nm.input.Value())
	}
	if nm.streaming {
		t.Fatal("streaming should be cleared while preserving the permission gate")
	}
	found := false
	for _, e := range nm.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "awaiting your tool decision") {
			found = true
		}
	}
	if !found {
		t.Fatal("reconnect should explain that the tool decision is still pending")
	}
}

func TestConfirmChatClearsStaleInput(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.input.SetValue("stale restored prompt")

	next, _ := m.resolveConfirmKey("c")
	if next.pendingConfirm != nil {
		t.Fatal("chat choice should dismiss pending confirm")
	}
	if next.composeToolUseID != "tool-1" {
		t.Fatalf("composeToolUseID = %q", next.composeToolUseID)
	}
	if next.input.Value() != "" {
		t.Fatalf("chat choice should clear stale input, got %q", next.input.Value())
	}
}

func TestConnRecovery_SteadyConnectedIsSilent(t *testing.T) {
	m := New(nil, false)

	next, _ := m.Update(connStateChangedMsg{state: agentclient.ConnStateConnected, attempt: 0})
	nm := next.(Model)

	for _, e := range nm.mainChat().Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "agent reconnected") {
			t.Fatal("steady Connected must not announce a recovery")
		}
	}
}

// TestConnStateChip_SlowLaneCopy locks the two-phase chip copy: bounded
// "(N/3)" during the fast burst, unbounded "retrying every 10s" once the
// SDK's slow lane takes over.
func TestConnStateChip_SlowLaneCopy(t *testing.T) {
	m := New(nil, false)
	m.connState = agentclient.ConnStateReconnecting

	m.connAttempt = 2
	if chip := m.renderConnStateChip(); !strings.Contains(chip, "(2/3)") {
		t.Errorf("fast-burst chip should show (2/3), got %q", chip)
	}
	m.connAttempt = 7
	chip := m.renderConnStateChip()
	if !strings.Contains(chip, "retrying every 10s") || !strings.Contains(chip, "attempt 7") {
		t.Errorf("slow-lane chip should show the retry cadence and attempt, got %q", chip)
	}
}
