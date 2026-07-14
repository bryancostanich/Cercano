// Package ui — claude_login_modal.go: the modal that drives Claude
// subscription sign-in from the settings page's Cloud Providers section.
//
// The Claude sibling of chatgpt_login_modal.go. It differs in the mechanism:
// the agent runs a PKCE loopback (not device-code), so there is no user code
// to display or copy — the first frame carries the authorize URL, which the
// modal opens in the browser automatically; the user approves and the agent's
// loopback catches the redirect. State machine and streaming plumbing mirror
// the ChatGPT modal.
package ui

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
	"github.com/charmbracelet/x/ansi"
)

// claudeLoginState is the modal's small state machine.
type claudeLoginState int

const (
	// claudeLoginWaiting shows the authorize URL and a "waiting for approval…"
	// prompt while the loopback flow blocks on the browser redirect.
	claudeLoginWaiting claudeLoginState = iota
	// claudeLoginDone means the terminal frame reported ok=true.
	claudeLoginDone
	// claudeLoginFailed means the RPC errored or the terminal frame carried
	// ok=false (declined, timed out, network).
	claudeLoginFailed
)

// claudeLoginModal owns the modal's transient state. Kept separate from Model
// so tests can exercise transitions without wiring the full TUI.
type claudeLoginModal struct {
	state claudeLoginState
	// authorizeURL is populated from the first stream frame — what the user
	// approves in the browser.
	authorizeURL string
	// profile + model are what we asked the agent to create; shown in the copy.
	profile string
	model   string
	// errMsg is set in the failed state.
	errMsg string
	// cancel tears down the sign-in RPC context — called on Esc during
	// waiting to abort, nil once the stream has settled.
	cancel context.CancelFunc
	// browserOpened guards the one-shot auto-open so re-draws / extra frames
	// don't spawn a browser tab twice.
	browserOpened bool
}

// newClaudeLoginModal opens the modal in the waiting state for a given profile
// + model. The caller batches startClaudeLoginCmd alongside.
func newClaudeLoginModal(profile, model string) *claudeLoginModal {
	return &claudeLoginModal{state: claudeLoginWaiting, profile: profile, model: model}
}

// setURL records the authorize URL from the first frame.
func (mo *claudeLoginModal) setURL(authorizeURL string) { mo.authorizeURL = authorizeURL }

// setDone transitions to the success state.
func (mo *claudeLoginModal) setDone() {
	mo.state = claudeLoginDone
	mo.cancel = nil
}

// setFailed transitions to the failure state carrying a reason.
func (mo *claudeLoginModal) setFailed(msg string) {
	mo.state = claudeLoginFailed
	mo.errMsg = msg
	mo.cancel = nil
}

// --- streaming plumbing ---

// openClaudeLoginModalMsg is emitted from the settings page when the user
// activates the "sign in with Claude" button. Carries the profile name +
// model to create and whether to activate it on success.
type openClaudeLoginModalMsg struct {
	profile   string
	model     string
	setActive bool
}

// claudeLoginStartedMsg carries the opened stream (or the open error).
type claudeLoginStartedMsg struct {
	cancel context.CancelFunc
	ch     <-chan agentclient.ClaudeLoginMsg
	err    error
}

// claudeLoginFrameMsg carries one drained frame plus the channel to keep
// draining (nil once the stream is exhausted).
type claudeLoginFrameMsg struct {
	frame agentclient.ClaudeLoginMsg
	ch    <-chan agentclient.ClaudeLoginMsg
}

// startClaudeLoginCmd opens the StartClaudeLogin streaming RPC under a
// cancellable context and returns the stream (or error) for the drain loop.
func startClaudeLoginCmd(ag *agentclient.Client, profile, model string, setActive bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := ag.StartClaudeLogin(ctx, profile, model, setActive)
		if err != nil {
			cancel()
			return claudeLoginStartedMsg{err: err}
		}
		return claudeLoginStartedMsg{cancel: cancel, ch: ch}
	}
}

// drainClaudeLoginCmd reads one frame from the stream channel and re-arms. A
// closed channel without a terminal frame is reported as a failure so the
// modal never hangs in the waiting state.
func drainClaudeLoginCmd(ch <-chan agentclient.ClaudeLoginMsg) tea.Cmd {
	return func() tea.Msg {
		frame, ok := <-ch
		if !ok {
			return claudeLoginFrameMsg{
				frame: agentclient.ClaudeLoginMsg{Done: true, Ok: false, Error: "sign-in stream closed unexpectedly"},
			}
		}
		return claudeLoginFrameMsg{frame: frame, ch: ch}
	}
}

// --- rendering ---

// modalDim returns the box dimensions for the compositor to center.
func (mo *claudeLoginModal) modalDim(frameW, frameH int) (int, int) {
	boxW := 64
	if frameW-4 < boxW {
		boxW = frameW - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 13
	if boxH > frameH-4 {
		boxH = frameH - 4
	}
	if boxH < 10 {
		boxH = 10
	}
	return boxW, boxH
}

// View renders the modal as a bordered box sized to the frame.
func (mo *claudeLoginModal) View(styles theme.Styles, palette theme.Palette, frameW, frameH int) string {
	boxW, boxH := mo.modalDim(frameW, frameH)

	var sections []string
	sections = append(sections, styles.Primary.Bold(true).Render(mo.title()))
	sections = append(sections, "")
	sections = append(sections, mo.renderBody(styles, boxW-4))
	sections = append(sections, "")
	sections = append(sections, mo.renderActions(styles))

	inner := strings.Join(sections, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(palette.Border).
		Padding(0, 1).
		Width(boxW - 2).
		Height(boxH - 2).
		Render(inner)
}

func (mo *claudeLoginModal) title() string {
	switch mo.state {
	case claudeLoginDone:
		return "Claude sign-in complete"
	case claudeLoginFailed:
		return "Claude sign-in failed"
	}
	return "Sign in with Claude"
}

func (mo *claudeLoginModal) renderBody(styles theme.Styles, w int) string {
	switch mo.state {
	case claudeLoginWaiting:
		if mo.authorizeURL == "" {
			return styles.Muted.Render("Starting sign-in…")
		}
		var b strings.Builder
		b.WriteString(styles.Muted.Render("Approve the sign-in in your browser. If it didn't open,"))
		b.WriteString("\n")
		b.WriteString(styles.Muted.Render("open this URL:"))
		b.WriteString("\n  ")
		b.WriteString(hyperlink(mo.authorizeURL, styles.Primary.Render("claude.ai authorize")))
		return b.String()
	case claudeLoginDone:
		msg := "Signed in. Profile " + strconv.Quote(mo.profile) + " is ready"
		if mo.model != "" {
			msg += " on " + mo.model
		}
		msg += "."
		return styles.Muted.Render(ansi.Wrap(msg, w, ""))
	case claudeLoginFailed:
		reason := mo.errMsg
		if reason == "" {
			reason = "unknown error"
		}
		return styles.Error.Render(ansi.Wrap(reason, w, ""))
	}
	return ""
}

func (mo *claudeLoginModal) renderActions(styles theme.Styles) string {
	switch mo.state {
	case claudeLoginWaiting:
		return styles.Muted.Render("[o] open browser  [Esc] Cancel  (waiting for approval…)")
	case claudeLoginDone:
		return styles.Success.Render("✓ [any key] to close")
	case claudeLoginFailed:
		return styles.Muted.Render("[Esc] Close")
	}
	return ""
}

// handleClaudeLoginModalKey routes a keypress while the sign-in modal is open.
// In the waiting state, Esc cancels the in-flight stream and closes the modal;
// once settled (done/failed) any key dismisses it.
func (m Model) handleClaudeLoginModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mo := m.claudeLoginModal
	if mo.state == claudeLoginWaiting {
		switch msg.String() {
		case "esc":
			if mo.cancel != nil {
				mo.cancel()
			}
			m.claudeLoginModal = nil
		case "o":
			if mo.authorizeURL != "" {
				return m, openBrowserCmd(mo.authorizeURL)
			}
		}
		return m, nil
	}
	m.claudeLoginModal = nil
	return m, nil
}
