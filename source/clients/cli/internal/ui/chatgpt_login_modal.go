// Package ui — chatgpt_login_modal.go: the modal that drives ChatGPT
// subscription sign-in from the settings page's Cloud Providers section.
//
// Two responsibilities, mirroring open_runtime_modal.go:
//   - Owns modal state (waiting → done|failed) plus the device code + URL the
//     user must act on, and the cancel func that aborts the sign-in stream.
//   - Renders the bordered box; the caller composites it over the base frame
//     with composeOverlay.
//
// The flow: the settings page emits openChatGPTLoginModalMsg; the root model
// opens this modal in the waiting state and kicks startChatGPTLoginCmd, which
// opens the StartChatGPTLogin streaming RPC. The first frame carries the
// user_code + verification_url (shown prominently so the user can approve in
// their browser); the terminal frame settles the modal to done or failed.
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

// chatgptLoginState is the modal's small state machine.
type chatgptLoginState int

const (
	// chatgptLoginWaiting shows the device code + URL and a "waiting for
	// approval…" prompt while the sign-in RPC polls.
	chatgptLoginWaiting chatgptLoginState = iota
	// chatgptLoginDone means the terminal frame reported ok=true. The
	// profile is created + (optionally) active. Auto-dismisses on next key.
	chatgptLoginDone
	// chatgptLoginFailed means the RPC errored or the terminal frame carried
	// ok=false (declined, timed out, network). Offers close.
	chatgptLoginFailed
)

// chatgptLoginModal owns the modal's transient state. Kept separate from Model
// so tests can exercise transitions without wiring the full TUI.
type chatgptLoginModal struct {
	state chatgptLoginState
	// verificationURL + userCode are populated from the first stream frame;
	// they're what the user needs to complete sign-in in the browser.
	verificationURL string
	userCode        string
	// profile + model are what we asked the agent to create; shown in the
	// waiting/done copy so the user knows which profile this is.
	profile string
	model   string
	// accountID is populated on success (from the terminal frame).
	accountID string
	// errMsg is set in the failed state.
	errMsg string
	// cancel tears down the sign-in RPC context — called on Esc during
	// waiting to abort, nil once the stream has settled.
	cancel context.CancelFunc
	// browserOpened guards the one-shot auto-open so re-draws / extra frames
	// don't spawn a browser tab per poll.
	browserOpened bool
	// copied is set once the user copies the device code, so the actions line
	// can confirm it.
	copied bool
}

// newChatGPTLoginModal opens the modal in the waiting state for a given
// profile + model. The caller batches startChatGPTLoginCmd alongside.
func newChatGPTLoginModal(profile, model string) *chatgptLoginModal {
	return &chatgptLoginModal{state: chatgptLoginWaiting, profile: profile, model: model}
}

// setCode records the device code + verification URL from the first frame.
func (mo *chatgptLoginModal) setCode(verificationURL, userCode string) {
	mo.verificationURL = verificationURL
	mo.userCode = userCode
}

// setDone transitions to the success state carrying the created profile's
// account id.
func (mo *chatgptLoginModal) setDone(accountID string) {
	mo.state = chatgptLoginDone
	mo.accountID = accountID
	mo.cancel = nil
}

// setFailed transitions to the failure state carrying a reason.
func (mo *chatgptLoginModal) setFailed(msg string) {
	mo.state = chatgptLoginFailed
	mo.errMsg = msg
	mo.cancel = nil
}

// --- streaming plumbing ---

// openChatGPTLoginModalMsg is emitted from the settings page when the user
// activates the "sign in with ChatGPT" button. Carries the profile name +
// model to create and whether to activate it on success.
type openChatGPTLoginModalMsg struct {
	profile   string
	model     string
	setActive bool
}

// chatgptLoginStartedMsg carries the opened stream (or the open error). The
// root model stashes cancel and kicks the drain loop.
type chatgptLoginStartedMsg struct {
	cancel context.CancelFunc
	ch     <-chan agentclient.ChatGPTLoginMsg
	err    error
}

// chatgptLoginFrameMsg carries one drained frame plus the channel to keep
// draining (nil once the stream is exhausted).
type chatgptLoginFrameMsg struct {
	frame agentclient.ChatGPTLoginMsg
	ch    <-chan agentclient.ChatGPTLoginMsg
}

// startChatGPTLoginCmd opens the StartChatGPTLogin streaming RPC under a
// cancellable context and returns the stream (or error) for the drain loop.
func startChatGPTLoginCmd(ag *agentclient.Client, profile, model string, setActive bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := ag.StartChatGPTLogin(ctx, profile, model, setActive)
		if err != nil {
			cancel()
			return chatgptLoginStartedMsg{err: err}
		}
		return chatgptLoginStartedMsg{cancel: cancel, ch: ch}
	}
}

// drainChatGPTLoginCmd reads one frame from the stream channel and re-arms.
// A closed channel without a terminal frame is reported as a failure so the
// modal never hangs in the waiting state.
func drainChatGPTLoginCmd(ch <-chan agentclient.ChatGPTLoginMsg) tea.Cmd {
	return func() tea.Msg {
		frame, ok := <-ch
		if !ok {
			return chatgptLoginFrameMsg{
				frame: agentclient.ChatGPTLoginMsg{Done: true, Ok: false, Error: "sign-in stream closed unexpectedly"},
			}
		}
		return chatgptLoginFrameMsg{frame: frame, ch: ch}
	}
}

// --- rendering ---

// modalDim returns the box dimensions for the compositor to center. Matches
// View sizing exactly.
func (mo *chatgptLoginModal) modalDim(frameW, frameH int) (int, int) {
	boxW := 64
	if frameW-4 < boxW {
		boxW = frameW - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 14
	if boxH > frameH-4 {
		boxH = frameH - 4
	}
	if boxH < 10 {
		boxH = 10
	}
	return boxW, boxH
}

// View renders the modal as a bordered box sized to the frame.
func (mo *chatgptLoginModal) View(styles theme.Styles, palette theme.Palette, frameW, frameH int) string {
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

func (mo *chatgptLoginModal) title() string {
	switch mo.state {
	case chatgptLoginWaiting:
		return "Sign in with ChatGPT"
	case chatgptLoginDone:
		return "ChatGPT sign-in complete"
	case chatgptLoginFailed:
		return "ChatGPT sign-in failed"
	}
	return "Sign in with ChatGPT"
}

func (mo *chatgptLoginModal) renderBody(styles theme.Styles, w int) string {
	switch mo.state {
	case chatgptLoginWaiting:
		if mo.userCode == "" {
			return styles.Muted.Render("Starting sign-in…")
		}
		var b strings.Builder
		b.WriteString(styles.Muted.Render("In your browser, open:"))
		b.WriteString("\n  ")
		b.WriteString(hyperlink(mo.verificationURL, styles.Primary.Render(mo.verificationURL)))
		b.WriteString("\n\n")
		b.WriteString(styles.Muted.Render("and enter this code:"))
		b.WriteString("\n  ")
		b.WriteString(styles.Success.Bold(true).Render(mo.userCode))
		return b.String()
	case chatgptLoginDone:
		msg := "Signed in. Profile " + strconv.Quote(mo.profile) + " is ready"
		if mo.model != "" {
			msg += " on " + mo.model
		}
		msg += "."
		return styles.Muted.Render(ansi.Wrap(msg, w, ""))
	case chatgptLoginFailed:
		reason := mo.errMsg
		if reason == "" {
			reason = "unknown error"
		}
		return styles.Error.Render(ansi.Wrap(reason, w, ""))
	}
	return ""
}

func (mo *chatgptLoginModal) renderActions(styles theme.Styles) string {
	switch mo.state {
	case chatgptLoginWaiting:
		if mo.copied {
			return styles.Success.Render("code copied ✓") + styles.Muted.Render("   [o] open browser  [Esc] Cancel")
		}
		return styles.Muted.Render("[c] copy code  [o] open browser  [Esc] Cancel  (waiting for approval…)")
	case chatgptLoginDone:
		return styles.Success.Render("✓ [any key] to close")
	case chatgptLoginFailed:
		return styles.Muted.Render("[Esc] Close")
	}
	return ""
}

// handleChatGPTLoginModalKey routes a keypress while the sign-in modal is
// open. In the waiting state, Esc cancels the in-flight sign-in stream and
// closes the modal; once settled (done/failed) any key dismisses it.
func (m Model) handleChatGPTLoginModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mo := m.chatgptLoginModal
	if mo.state == chatgptLoginWaiting {
		switch msg.String() {
		case "esc":
			if mo.cancel != nil {
				mo.cancel()
			}
			m.chatgptLoginModal = nil
		case "o":
			if mo.verificationURL != "" {
				return m, openBrowserCmd(mo.verificationURL)
			}
		case "c":
			if mo.userCode != "" {
				mo.copied = true
				return m, selectionClipboardCmd(mo.userCode)
			}
		}
		return m, nil
	}
	m.chatgptLoginModal = nil
	return m, nil
}
