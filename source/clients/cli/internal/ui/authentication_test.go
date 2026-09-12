package ui

import (
	"context"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func authFixture(id string) agentclient.AuthenticationRequired {
	return agentclient.AuthenticationRequired{ConversationID: "conv", RequestID: id, Provider: "anthropic", Profile: "work", RetrySafe: true, Fallback: "backup"}
}
func TestAuthenticationResolvedRemovesActiveAndQueuedGates(t *testing.T) {
	m := New(nil, false)
	m.convID = "conv"
	m, _ = m.receiveAuthentication(authFixture("a"))
	m, _ = m.receiveAuthentication(authFixture("b"))
	completed := authFixture("a")
	completed.Resolved = true
	m, _ = m.receiveAuthentication(completed)
	if m.pendingConfirm == nil || m.pendingConfirm.auth.RequestID != "b" || len(m.confirmQueue) != 0 {
		t.Fatal("resolved authentication gate retained")
	}
	completed.RequestID = "b"
	m, _ = m.receiveAuthentication(completed)
	if m.pendingConfirm != nil {
		t.Fatal("last resolved gate retained")
	}
}
func TestAuthenticationWaitsBehindPermission(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "original"
	permission := &confirmRequest{title: "existing"}
	m.pendingConfirm = permission
	m, _ = m.receiveAuthentication(authFixture("queued"))
	if m.pendingConfirm != permission || len(m.confirmQueue) != 1 || m.confirmQueue[0].retryPrompt != "original" {
		t.Fatal("authentication interrupted or was dropped behind permission")
	}
	m.pendingConfirm = nil
	m.advanceConfirmation()
	if m.pendingConfirm == nil || m.pendingConfirm.auth.RequestID != "queued" {
		t.Fatal("queued authentication not presented")
	}
}
func TestManualLoginLateFrameCannotCompleteAnotherProfile(t *testing.T) {
	m := New(nil, false)
	m.claudeLoginModal = newClaudeLoginModal("new-profile", "")
	updated, _ := m.Update(claudeLoginFrameMsg{frame: agentclient.ClaudeLoginMsg{ProfileName: "old-profile", Done: true, Ok: true}})
	next := updated.(Model)
	if next.claudeLoginModal.state != claudeLoginWaiting {
		t.Fatal("old login frame completed a newer modal")
	}
}
func TestAuthenticationCancelClearsModalAndDoesNotReplay(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "do work"
	m, _ = m.receiveAuthentication(authFixture("a"))
	c := m.pendingConfirm
	ctx, cancel := context.WithCancel(context.Background())
	m.authLogin = &authenticationLogin{id: "attempt", gate: c, cancel: cancel}
	m.claudeLoginModal = newClaudeLoginModal("work", "")
	next, _ := m.cancelAuthentication(c)
	if ctx.Err() == nil || next.authLogin != nil || next.claudeLoginModal != nil || next.pendingConfirm != nil {
		t.Fatal("cancel retained authentication state")
	}
	if next.lastSubmittedPrompt != "do work" {
		t.Fatal("cancel replaced original request")
	}
}

func TestOpeningExistingLoginModalDoesNotInvalidateItsAttempt(t *testing.T) {
	m := New(nil, false)
	m.claudeLoginModal = newClaudeLoginModal("work", "")
	m.claudeLoginAttempt = 7
	updated, _ := m.Update(openClaudeLoginModalMsg{profile: "work"})
	next := updated.(Model)
	if next.claudeLoginAttempt != 7 {
		t.Fatal("reopening an existing modal invalidated its running stream")
	}
}

func TestAuthenticationReconnectPreservesDecisionAndRetryText(t *testing.T) {
	m := New(nil, false)
	m.connState = agentclient.ConnStateConnected
	m.lastSubmittedPrompt = "original task"
	m.convID = "conv"
	m, _ = m.receiveAuthentication(authFixture("a"))
	c := m.pendingConfirm
	ctx, cancel := context.WithCancel(context.Background())
	m.authLogin = &authenticationLogin{gate: c, id: "attempt", cancel: cancel}
	m.claudeLoginModal = newClaudeLoginModal("work", "")
	updated, _ := m.Update(connStateChangedMsg{state: agentclient.ConnStateReconnecting})
	next := updated.(Model)
	if next.pendingConfirm != c || !c.stale || c.retryPrompt != "original task" || ctx.Err() == nil || next.authLogin != nil {
		t.Fatal("reconnect lost gate or retained old login")
	}
	next.lastSubmittedPrompt = "" // stream cleanup must not erase the gate's captured retry
	next.authLogin = &authenticationLogin{gate: c, id: "fresh-attempt", cancel: func() {}}
	next, _ = next.handleAuthenticationFrame(authenticationFrameMsg{id: "fresh-attempt", frame: agentclient.CloudLoginMsg{Done: true, Ok: true}})
	users := 0
	for _, e := range next.mainChat().Entries() {
		if e.Role == RoleUser && e.Content == "original task" {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("fresh retry user requests=%d", users)
	}
}
func TestStaleAuthenticationCancelAndLateFramesDoNotRetry(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "original"
	m, _ = m.receiveAuthentication(authFixture("a"))
	c := m.pendingConfirm
	c.stale = true
	m, _ = m.cancelAuthentication(c)
	m, _ = m.handleAuthenticationFrame(authenticationFrameMsg{id: "old", frame: agentclient.CloudLoginMsg{Done: true, Ok: true}})
	for _, e := range m.mainChat().Entries() {
		if e.Role == RoleUser {
			t.Fatal("canceled login resurrected a request")
		}
	}
}
func TestStaleFallbackApprovalIsSingleUseAndBoundToNewTurn(t *testing.T) {
	m := New(nil, false)
	m.lastSubmittedPrompt = "original"
	m, _ = m.receiveAuthentication(authFixture("old"))
	c := m.pendingConfirm
	c.stale = true
	m, _ = m.retryAuthentication(c, true)
	if m.authReplay == nil {
		t.Fatal("explicit fresh fallback choice was lost")
	}
	request := authFixture("new")
	m, _ = m.receiveAuthentication(request)
	if m.authReplay != nil || m.pendingConfirm != nil {
		t.Fatal("matching explicit fallback was not consumed once")
	}
	m, _ = m.receiveAuthentication(authFixture("another"))
	if m.pendingConfirm == nil {
		t.Fatal("fallback authorization leaked to another request")
	}
}
func TestStaleFallbackDoesNotAuthorizeDifferentDestination(t *testing.T) {
	m := New(nil, false)
	m.authReplay = &authenticationReplay{profile: "work", fallback: "old-backup", gen: m.turnGen}
	request := authFixture("new")
	m, _ = m.receiveAuthentication(request)
	if m.pendingConfirm == nil || m.authReplay != nil {
		t.Fatal("changed route reused stale authorization")
	}
}
func TestAuthenticationLateStartCancelsItsOrphanContext(t *testing.T) {
	m := New(nil, false)
	ctx, cancel := context.WithCancel(context.Background())
	m, _ = m.handleAuthenticationStarted(authenticationStartedMsg{id: "old", cancel: cancel})
	if ctx.Err() == nil || m.authLogin != nil {
		t.Fatal("orphan login was retained")
	}
}
func TestLegacySameProfileFramesAreAttemptScoped(t *testing.T) {
	m := New(nil, false)
	m.claudeLoginModal = newClaudeLoginModal("work", "")
	m.claudeLoginAttempt = 2
	updated, _ := m.Update(claudeLoginFrameMsg{attempt: 1, frame: agentclient.ClaudeLoginMsg{ProfileName: "work", Done: true, Ok: true}})
	next := updated.(Model)
	if next.claudeLoginModal.state != claudeLoginWaiting {
		t.Fatal("same-profile stale frame accepted")
	}
	next.chatgptLoginModal = newChatGPTLoginModal("work", "")
	next.chatgptLoginAttempt = 4
	updated, _ = next.Update(chatgptLoginFrameMsg{attempt: 3, frame: agentclient.ChatGPTLoginMsg{ProfileName: "work", Done: true, Ok: true}})
	next = updated.(Model)
	if next.chatgptLoginModal.state != chatgptLoginWaiting {
		t.Fatal("same-profile stale device frame accepted")
	}
}
func TestPartialResponseAuthenticationDoesNotOfferFallback(t *testing.T) {
	request := authFixture("partial")
	request.RetrySafe = false
	c := authenticationConfirm(request)
	if c.extras["f"] != nil {
		t.Fatal("unsafe replay fallback offered")
	}
}
