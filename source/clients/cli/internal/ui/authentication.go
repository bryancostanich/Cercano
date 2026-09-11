package ui

import (
	"context"
	"fmt"
	"time"

	"cercano/source/server/pkg/agentclient"
	tea "charm.land/bubbletea/v2"
)

type authenticationRequiredMsg struct {
	request agentclient.AuthenticationRequired
}
type authenticationStartedMsg struct {
	id     string
	ch     <-chan agentclient.CloudLoginMsg
	cancel context.CancelFunc
	err    error
}
type authenticationFrameMsg struct {
	id    string
	frame agentclient.CloudLoginMsg
	ch    <-chan agentclient.CloudLoginMsg
}
type authenticationResolvedMsg struct {
	id  string
	err error
}
type authenticationLogin struct {
	gate   *confirmRequest
	id     string
	cancel context.CancelFunc
	failed bool
}
type authenticationReplay struct {
	profile, fallback string
	gen               int
}

func (m *Model) enqueueConfirmation(c *confirmRequest) {
	if c.retryPrompt == "" {
		c.retryPrompt = m.lastSubmittedPrompt
	}
	if m.pendingConfirm == nil {
		m.pendingConfirm = c
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmRequest(c)})
	} else {
		m.confirmQueue = append(m.confirmQueue, c)
	}
	m.refreshViewport()
}
func (m *Model) advanceConfirmation() {
	if m.pendingConfirm == nil && len(m.confirmQueue) > 0 {
		m.pendingConfirm = m.confirmQueue[0]
		m.confirmQueue = m.confirmQueue[1:]
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmRequest(m.pendingConfirm)})
		m.refreshViewport()
	}
}
func (m Model) receiveAuthentication(req agentclient.AuthenticationRequired) (Model, tea.Cmd) {
	if req.Resolved {
		m.removeAuthentication(req.ConversationID, req.RequestID)
		return m, nil
	}

	if req.RequestID == "" || req.Profile == "" {
		return m, nil
	}
	if m.pendingConfirm != nil && m.pendingConfirm.auth != nil && m.pendingConfirm.auth.RequestID == req.RequestID {
		return m, nil
	}
	for _, c := range m.confirmQueue {
		if c.auth != nil && c.auth.RequestID == req.RequestID {
			return m, nil
		}
	}
	if replay := m.authReplay; replay != nil && replay.gen == m.turnGen {
		m.authReplay = nil
		if replay.profile == req.Profile && replay.fallback == req.Fallback && req.Fallback != "" {
			return m, resolveAuthenticationCmd(m.agent, req, "fallback")
		}
	}
	c := authenticationConfirm(req)
	m.enqueueConfirmation(c)
	return m, nil
}
func authenticationConfirm(req agentclient.AuthenticationRequired) *confirmRequest {
	c := &confirmRequest{auth: &req, title: fmt.Sprintf("%s login required — %s", req.Provider, req.Profile), details: []string{"The request is paused. Login does not change your active profile or configuration."}, hints: "[y] log in / [n] cancel / [d] details", extras: make(map[string]func(Model) (Model, tea.Cmd))}
	c.onYes = func(m Model) (Model, tea.Cmd) { return m.startAuthentication(c) }
	c.onNo = func(m Model) (Model, tea.Cmd) { return m.cancelAuthentication(c) }
	c.hints += " / [c] chat"
	c.extras["c"] = func(m Model) (Model, tea.Cmd) {
		next, cancel := m.cancelAuthentication(c)
		next.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "Authentication canceled. Type a new message to continue."})
		next.refreshViewport()
		return next, tea.Batch(cancel, next.input.Focus())
	}
	c.extras["C"] = c.extras["c"]
	if !req.RetrySafe {
		c.details = append(c.details, "A response has already started. Login can repair credentials, but continuing requires an explicit fresh request; partial output will not be replayed.")
	}
	if req.Fallback != "" && req.RetrySafe {
		c.hints += " / [f] use " + req.Fallback
		c.extras["f"] = func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			if c.stale {
				return m.retryAuthentication(c, true)
			}
			m.advanceConfirmation()
			return m, resolveAuthenticationCmd(m.agent, req, "fallback")
		}
		c.extras["F"] = c.extras["f"]
	}
	c.extras["d"] = func(m Model) (Model, tea.Cmd) {
		text := fmt.Sprintf("Profile: %s\nProvider: %s\nReason: %s", req.Profile, req.Provider, req.Reason)
		if c.stale {
			text += "\nThe previous request was lost. Login/fallback will explicitly start a fresh turn."
		}
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: text})
		m.refreshViewport()
		return m, nil
	}
	c.extras["D"] = c.extras["d"]
	return c
}
func (m Model) startAuthentication(c *confirmRequest) (Model, tea.Cmd) {
	if m.agent == nil || c.auth == nil {
		return m, nil
	}
	m.authEpoch++
	m.claudeLoginAttempt++
	m.chatgptLoginAttempt++
	id := fmt.Sprintf("%s/%d", c.auth.RequestID, m.authEpoch)
	ctx, cancel := context.WithCancel(context.Background())
	m.authLogin = &authenticationLogin{gate: c, id: id, cancel: cancel}
	if c.auth.Provider == "anthropic" {
		m.claudeLoginModal = newClaudeLoginModal(c.auth.Profile, "")
	} else if c.auth.Provider == "openai-responses" {
		m.chatgptLoginModal = newChatGPTLoginModal(c.auth.Profile, "")
	} else {
		cancel()
		m.authLogin = nil
		return m.cancelAuthentication(c)
	}
	agent, profile := m.agent, c.auth.Profile
	return m, func() tea.Msg {
		ch, err := agent.ReauthenticateCloud(ctx, profile, id)
		return authenticationStartedMsg{id: id, ch: ch, cancel: cancel, err: err}
	}
}
func drainAuthentication(id string, ch <-chan agentclient.CloudLoginMsg) tea.Cmd {
	return func() tea.Msg {
		frame, ok := <-ch
		if !ok {
			return authenticationFrameMsg{id: id, frame: agentclient.CloudLoginMsg{Err: fmt.Errorf("login stream ended without completion")}}
		}
		return authenticationFrameMsg{id: id, frame: frame, ch: ch}
	}
}
func (m Model) handleAuthenticationStarted(msg authenticationStartedMsg) (Model, tea.Cmd) {
	if m.authLogin == nil || m.authLogin.id != msg.id {
		if msg.cancel != nil {
			msg.cancel()
		}
		return m, nil
	}
	if msg.err != nil {
		return m.handleAuthenticationFrame(authenticationFrameMsg{id: msg.id, frame: agentclient.CloudLoginMsg{Err: msg.err}})
	}
	return m, drainAuthentication(msg.id, msg.ch)
}
func (m Model) handleAuthenticationFrame(msg authenticationFrameMsg) (Model, tea.Cmd) {
	a := m.authLogin
	if a == nil || a.id != msg.id {
		return m, nil
	}
	f := msg.frame
	if f.Err != nil || f.Done && !f.Ok {
		text := f.Error
		if f.Err != nil {
			text = f.Err.Error()
		}
		a.failed = true
		if m.claudeLoginModal != nil {
			m.claudeLoginModal.setFailed(text)
		}
		if m.chatgptLoginModal != nil {
			m.chatgptLoginModal.setFailed(text)
		}
		return m, nil
	}
	if f.Done {
		a.cancel()
		m.authLogin = nil
		m.claudeLoginModal = nil
		m.chatgptLoginModal = nil
		m.pendingConfirm = nil
		if a.gate.stale {
			return m.retryAuthentication(a.gate, false)
		}
		m.advanceConfirmation()
		return m, nil // The shared service wakes live gates after the credential commit.
	}
	var open tea.Cmd
	if m.claudeLoginModal != nil && f.AuthorizeURL != "" {
		m.claudeLoginModal.setURL(f.AuthorizeURL)
		if !m.claudeLoginModal.browserOpened {
			m.claudeLoginModal.browserOpened = true
			open = openBrowserCmd(f.AuthorizeURL)
		}
	}
	if m.chatgptLoginModal != nil && f.VerificationURL != "" {
		m.chatgptLoginModal.setCode(f.VerificationURL, f.UserCode)
		if !m.chatgptLoginModal.browserOpened {
			m.chatgptLoginModal.browserOpened = true
			open = openBrowserCmd(f.VerificationURL)
		}
	}
	return m, tea.Batch(open, drainAuthentication(msg.id, msg.ch))
}
func (m Model) cancelAuthentication(c *confirmRequest) (Model, tea.Cmd) {
	if m.authLogin != nil {
		m.authLogin.cancel()
		m.authLogin = nil
		m.claudeLoginModal = nil
		m.chatgptLoginModal = nil
	}
	m.pendingConfirm = nil
	m.advanceConfirmation()
	if c.stale || c.auth == nil {
		return m, nil
	}
	return m, resolveAuthenticationCmd(m.agent, *c.auth, "cancel")
}
func resolveAuthenticationCmd(agent *agentclient.Client, r agentclient.AuthenticationRequired, decision string) tea.Cmd {
	if agent == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return authenticationResolvedMsg{id: r.RequestID, err: agent.ResolveAuthentication(ctx, r.ConversationID, r.RequestID, decision)}
	}
}
func (m Model) retryAuthentication(c *confirmRequest, fallback bool) (Model, tea.Cmd) {
	prompt := c.retryPrompt
	if prompt == "" {
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "Original request unavailable; submit a new request."})
		m.advanceConfirmation()
		return m, nil
	}
	// All queued decisions belonged to lost waiters. A fresh turn will issue its
	// own gates; never replay approvals for the obsolete ones.
	m.confirmQueue = nil
	m.pendingConfirm = nil
	m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "↻ starting a fresh request; the previous turn cannot be resumed"})
	next, cmd := m.submit(prompt, nil)
	if model, ok := next.(Model); ok {
		if fallback {
			model.authReplay = &authenticationReplay{profile: c.auth.Profile, fallback: c.auth.Fallback, gen: model.turnGen}
		}
		return model, cmd
	}
	return m, cmd
}

// removeAuthentication affects only the named gate, never another request's
// permission or fallback choice. Successful login broadcasts use this path.
func (m *Model) removeAuthentication(conversation, id string) {
	matches := func(c *confirmRequest) bool {
		return c != nil && c.auth != nil && c.auth.RequestID == id && c.auth.ConversationID == conversation
	}
	if m.authLogin != nil && matches(m.authLogin.gate) {
		m.authLogin.cancel()
		m.authLogin = nil
		m.claudeLoginModal = nil
		m.chatgptLoginModal = nil
	}
	if matches(m.pendingConfirm) {
		m.pendingConfirm = nil
	}
	kept := m.confirmQueue[:0]
	for _, c := range m.confirmQueue {
		if !matches(c) {
			kept = append(kept, c)
		}
	}
	m.confirmQueue = kept
	m.advanceConfirmation()
	m.refreshViewport()
}
func (m *Model) markAuthenticationStale() {
	if m.authLogin != nil {
		m.authLogin.cancel()
		m.authLogin = nil
		m.claudeLoginModal = nil
		m.chatgptLoginModal = nil
	}
	for _, c := range append([]*confirmRequest{m.pendingConfirm}, m.confirmQueue...) {
		if c != nil && c.tool != nil {
			c.stale = true
		}
		if c != nil && c.auth != nil && !c.stale {
			c.stale = true
			c.details = append(c.details, "The old request was lost. Login/fallback explicitly starts a fresh turn; cancellation does not retry it.")
		}
	}
}
func (m *Model) clearAuthenticationForConversation(conversation string) {
	var ids []string
	for _, c := range append([]*confirmRequest{m.pendingConfirm}, m.confirmQueue...) {
		if c != nil && c.auth != nil && c.auth.ConversationID == conversation {
			ids = append(ids, c.auth.RequestID)
		}
	}
	for _, id := range ids {
		m.removeAuthentication(conversation, id)
	}
	m.authReplay = nil
}
