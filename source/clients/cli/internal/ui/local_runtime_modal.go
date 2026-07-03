// Package ui — local_runtime_modal.go: the install modal that appears on top
// of whatever page is active when the user hits F1 (or clicks the chip) after
// switching local_runtime to a llama_server config the agent can't detect.
//
// Two responsibilities:
//   - Owns modal state (idle → running → done|failed) and log-line buffer.
//   - Renders the bordered box; the caller composites it over the base frame
//     with composeOverlay.
package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
	"github.com/charmbracelet/x/ansi"
)

// runtimeModalState walks a small state machine driven by user keypress and
// the InstallLocalRuntime stream.
type runtimeModalState int

const (
	// runtimeModalIdle is the initial state — the modal shows the error,
	// the suggested command, and prompts the user to install or cancel.
	runtimeModalIdle runtimeModalState = iota
	// runtimeModalRunning means the install RPC is streaming. Buttons are
	// hidden and we accumulate log lines.
	runtimeModalRunning
	// runtimeModalDone means the install completed successfully and the
	// LocalRuntimeStatusChanged event flipped ok=true. Auto-dismisses on
	// the next key.
	runtimeModalDone
	// runtimeModalFailed means the install RPC returned an error (nonzero
	// exit, missing brew, unsupported platform). Offers "Retry" and "Close".
	runtimeModalFailed
	// runtimeModalNeedsModel means the install subprocess itself succeeded
	// (llama-server binary is now available) but post-install detection
	// still can't proceed because no default GGUF model is set. This is
	// distinct from Failed — retrying the install won't help; the user
	// needs to drop a .gguf file into ~/.cercano/models/ or set
	// llama_server.default_model in config.yaml.
	runtimeModalNeedsModel
)

// localRuntimeInstallModal owns the modal's transient state — the reason we
// opened (a snapshot of the status event), the state machine, buffered log
// lines, and the terminal error if any. Kept separate from Model so tests
// can exercise state transitions without wiring the full TUI.
type localRuntimeInstallModal struct {
	// status is the snapshot that opened the modal; it's what we render at
	// the top ("llama-server not installed" etc). Not updated live — we
	// close on success rather than mutating in place.
	status agentclient.LocalRuntimeStatus
	// state is the current step in the install lifecycle.
	state runtimeModalState
	// logLines accumulates streamed install output — one entry per line.
	// The tail is what's visible; older lines scroll off the top when the
	// log pane is smaller than the buffer.
	logLines []string
	// errMsg is populated when the install RPC returns a non-nil error or
	// the stream carried ok=false on its terminal message.
	errMsg string
	// cancel tears down the install RPC's context — set when the install
	// stream is opened, called on Esc during running-state to abort. nil
	// when no install is in flight.
	cancel context.CancelFunc
}

// newLocalRuntimeInstallModal opens the modal in idle state carrying the
// status snapshot that triggered it. Caller stashes the returned pointer on
// Model and re-renders.
func newLocalRuntimeInstallModal(st agentclient.LocalRuntimeStatus) *localRuntimeInstallModal {
	return &localRuntimeInstallModal{status: st, state: runtimeModalIdle}
}

// openLocalRuntimeInstallModalMsg is emitted from the settings page when the
// user tries to switch to a runtime that isn't ready. Carries the status
// snapshot to render in the idle state, plus the runtime id whose switch is
// queued to fire after a successful install. If the user cancels, the
// pending switch is dropped — no UpdateConfig is dispatched, and the
// runtime toggle reverts to whatever the server currently has.
type openLocalRuntimeInstallModalMsg struct {
	status  agentclient.LocalRuntimeStatus
	pending string // runtime id to switch to on install success ("" = none)
}

// appendLog records one line of streamed install output. Trimming trailing
// whitespace keeps the render tidy even if the subprocess emits CR+LF.
func (mo *localRuntimeInstallModal) appendLog(line string) {
	mo.logLines = append(mo.logLines, strings.TrimRight(line, " \r\n\t"))
}

// setFailed transitions to the failed state carrying an error message.
func (mo *localRuntimeInstallModal) setFailed(msg string) {
	mo.state = runtimeModalFailed
	mo.errMsg = msg
}

// setNeedsModel transitions to the "install succeeded, but pick a model"
// state. Retry is disabled here — retrying the install can't fix a
// missing / ambiguous GGUF selection.
func (mo *localRuntimeInstallModal) setNeedsModel(msg string) {
	mo.state = runtimeModalNeedsModel
	mo.errMsg = msg
}

// installErrorIsMissingModel reports whether an install-done terminal error
// indicates the install itself succeeded but post-install detection can't
// pick a GGUF model. Matches the format from
// llamaserver.DetectError.Error() (kept coupled deliberately —
// TestInstallErrorIsMissingModel guards the string contract).
func installErrorIsMissingModel(errMsg string) bool {
	return strings.Contains(errMsg, "install completed but detection still fails") &&
		strings.Contains(errMsg, "detection: model:")
}

// View renders the modal as a bordered box sized to fit the given terminal
// frame. Width caps at 80 cells to keep the box readable; height caps at
// max(20, frameHeight-6) to reserve room for header + prompt.
func (mo *localRuntimeInstallModal) View(styles theme.Styles, palette theme.Palette, frameW, frameH int) string {
	// Sizing.
	boxW := 80
	if frameW-4 < boxW {
		boxW = frameW - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := frameH - 6
	if boxH < 12 {
		boxH = 12
	}
	if boxH > 24 {
		boxH = 24
	}

	// Content.
	var sections []string
	sections = append(sections, mo.renderHeader(styles))
	sections = append(sections, "")
	sections = append(sections, mo.renderBody(styles, boxW-4)) // -4 for border padding
	sections = append(sections, "")
	sections = append(sections, mo.renderLogs(styles, boxW-4, boxH-11))
	sections = append(sections, "")
	sections = append(sections, mo.renderActions(styles))

	inner := strings.Join(sections, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(palette.Border).
		Padding(0, 1).
		Width(boxW - 2). // -2 for border cells
		Height(boxH - 2).
		Render(inner)
	return box
}

// modalDim returns the (width, height) the last-View'd box would occupy in
// the frame. Used by the compositor to center the modal.
func (mo *localRuntimeInstallModal) modalDim(frameW, frameH int) (int, int) {
	// Match View sizing exactly.
	boxW := 80
	if frameW-4 < boxW {
		boxW = frameW - 4
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := frameH - 6
	if boxH < 12 {
		boxH = 12
	}
	if boxH > 24 {
		boxH = 24
	}
	return boxW, boxH
}

func (mo *localRuntimeInstallModal) renderHeader(styles theme.Styles) string {
	var title string
	switch mo.state {
	case runtimeModalIdle:
		switch mo.status.Missing {
		case "binary":
			title = "Install llama-server?"
		case "model":
			title = "Add a GGUF model?"
		default:
			title = "Local runtime setup"
		}
	case runtimeModalRunning:
		title = "Installing…"
	case runtimeModalDone:
		title = "Install complete"
	case runtimeModalFailed:
		title = "Install failed"
	case runtimeModalNeedsModel:
		title = "llama-server ready — pick a GGUF model"
	}
	return styles.Primary.Bold(true).Render(title)
}

func (mo *localRuntimeInstallModal) renderBody(styles theme.Styles, w int) string {
	msg := strings.TrimSpace(mo.status.Message)
	if msg == "" {
		msg = "Detection couldn't find the required prerequisites."
	}
	wrapped := ansi.Wrap(msg, w, "")
	body := styles.Muted.Render(wrapped)

	cmd := strings.TrimSpace(mo.status.SuggestedCommand)
	if cmd == "" || mo.status.Missing != "binary" {
		return body
	}
	label := styles.Muted.Render("Command:")
	block := lipgloss.NewStyle().
		Foreground(styles.Primary.GetForeground()).
		Render("  " + cmd)
	return body + "\n" + label + "\n" + block
}

func (mo *localRuntimeInstallModal) renderLogs(styles theme.Styles, w, h int) string {
	if mo.state == runtimeModalIdle {
		return styles.Muted.Render("(logs will appear here once install starts)")
	}
	if h < 1 {
		h = 1
	}
	tail := mo.logLines
	if len(tail) > h {
		tail = tail[len(tail)-h:]
	}
	var rendered []string
	for _, ln := range tail {
		// Truncate lines that exceed the pane width so they don't wrap
		// into the next slot; scroll bar is single-line to keep the log
		// pane density high.
		if ansi.StringWidth(ln) > w {
			ln = ansi.Truncate(ln, w, "…")
		}
		rendered = append(rendered, ln)
	}
	// Pad up to h rows so the border doesn't jump as content grows.
	for len(rendered) < h {
		rendered = append(rendered, "")
	}
	return styles.Muted.Render(strings.Join(rendered, "\n"))
}

func (mo *localRuntimeInstallModal) renderActions(styles theme.Styles) string {
	switch mo.state {
	case runtimeModalIdle:
		primary := styles.Success.Bold(true).Render("[Enter] Install now")
		secondary := styles.Muted.Render("[Esc] Cancel")
		return primary + "    " + secondary
	case runtimeModalRunning:
		return styles.Muted.Render("Installing… (Esc to abort)")
	case runtimeModalDone:
		return styles.Success.Render("✓ Ready — [any key] to close")
	case runtimeModalFailed:
		primary := styles.Primary.Bold(true).Render("[Enter] Retry")
		secondary := styles.Muted.Render("[Esc] Close")
		errLine := ""
		if mo.errMsg != "" {
			errLine = "\n" + styles.Error.Render("  "+mo.errMsg)
		}
		return primary + "    " + secondary + errLine
	case runtimeModalNeedsModel:
		// The install itself succeeded — Retry would just reinstall
		// llama-server pointlessly. The user needs to either drop a
		// .gguf into ~/.cercano/models/ or set llama_server.default_model
		// in config.yaml. Copy walks them through both paths.
		primary := styles.Success.Bold(true).Render("[Esc] Close")
		hint := styles.Muted.Render("Next steps:\n" +
			"  • add a .gguf model to ~/.cercano/models/\n" +
			"  • or set llama_server.default_model in ~/.config/cercano/config.yaml\n" +
			"Then reopen this modal (F1) to verify.")
		return primary + "\n" + hint
	}
	return ""
}

// runtimeInstallProgressMsg is one non-terminal frame from the install
// stream. next re-arms the drain loop, mirroring how event-subscription
// commands work. line is the raw subprocess line to append.
type runtimeInstallProgressMsg struct {
	line string
	next tea.Cmd
}

// runtimeInstallDoneMsg is the terminal frame — Ok reflects the
// subprocess's exit status (nonzero exit → Ok=false), err is set only if
// the stream itself failed or the subprocess couldn't be launched.
type runtimeInstallDoneMsg struct {
	ok  bool
	err string
}

// handleLocalRuntimeModalKey resolves a key while the install modal is
// open. Returns the new Model + any cmd. Modal-active state is the only
// place these bindings apply — global key handling stays untouched.
func (m Model) handleLocalRuntimeModalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.localRuntimeModal == nil {
		return m, nil
	}
	key := msg.String()
	switch m.localRuntimeModal.state {
	case runtimeModalIdle:
		if key == "enter" {
			m.localRuntimeModal.state = runtimeModalRunning
			return m, startLocalRuntimeInstallCmd(m.agent, "llama_server")
		}
		if key == "esc" || key == "q" {
			m.localRuntimeModal = nil
			m.pendingRuntimeSwitch = "" // user rejected the switch
		}
	case runtimeModalRunning:
		if key == "esc" {
			// Cancel the install: the stream context gets torn down
			// when the modal closes; the server-side subprocess is
			// killed and the drain loop exits. The queued switch is
			// dropped — the runtime isn't ready.
			if m.localRuntimeModal.cancel != nil {
				m.localRuntimeModal.cancel()
			}
			m.localRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
		}
	case runtimeModalDone:
		// Any key dismisses. pendingRuntimeSwitch was already cleared
		// by runtimeInstallDoneMsg (either dispatched or dropped).
		m.localRuntimeModal = nil
	case runtimeModalFailed:
		if key == "enter" {
			m.localRuntimeModal.state = runtimeModalRunning
			m.localRuntimeModal.errMsg = ""
			m.localRuntimeModal.logLines = nil
			return m, startLocalRuntimeInstallCmd(m.agent, "llama_server")
		}
		if key == "esc" || key == "q" {
			m.localRuntimeModal = nil
			m.pendingRuntimeSwitch = "" // failed install; drop the queued switch too
		}
	case runtimeModalNeedsModel:
		// Any key dismisses — there's no in-modal recovery for a
		// missing GGUF; the user resolves it out-of-band and reopens.
		m.localRuntimeModal = nil
		m.pendingRuntimeSwitch = ""
	}
	return m, nil
}

// dispatchLocalRuntimeSwitch fires an UpdateConfig(local-runtime=runtime) in
// the background after a successful install. Errors are swallowed — the
// server broadcasts its own ConfigChanged / LocalRuntimeStatusChanged events
// so any status change becomes visible via the normal channels without a
// custom msg round-trip.
func dispatchLocalRuntimeSwitch(ag *agentclient.Client, runtime string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, agentclient.ConfigUpdate{LocalRuntime: runtime})
		return nil
	}
}

// runtimeInstallStartedMsg is the first message emitted by
// startLocalRuntimeInstallCmd — it wires the RPC's cancel func back to
// Model so Esc-during-running can abort the subprocess, and it kicks off
// the drain loop. err is set when opening the stream itself failed.
type runtimeInstallStartedMsg struct {
	cancel context.CancelFunc
	next   tea.Cmd
	err    error
}

// startLocalRuntimeInstallCmd opens the InstallLocalRuntime streaming RPC
// and returns a bubbletea command whose first message is a
// runtimeInstallStartedMsg carrying (a) the cancel func Model must stash on
// the modal, and (b) a drain cmd that emits one InstallProgress frame per
// tick and re-arms itself until the stream terminates. Runtime is the
// server-side runtime id ("llama_server" for now).
func startLocalRuntimeInstallCmd(ag *agentclient.Client, runtime string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := ag.InstallLocalRuntime(ctx, runtime)
		if err != nil {
			cancel()
			return runtimeInstallStartedMsg{err: err}
		}
		var drain tea.Cmd
		drain = func() tea.Msg {
			frame, ok := <-ch
			if !ok {
				return runtimeInstallDoneMsg{err: "install stream ended unexpectedly"}
			}
			if frame.Err != nil {
				return runtimeInstallDoneMsg{err: frame.Err.Error()}
			}
			if frame.Done {
				return runtimeInstallDoneMsg{ok: frame.Ok, err: frame.Error}
			}
			return runtimeInstallProgressMsg{line: frame.Line, next: drain}
		}
		return runtimeInstallStartedMsg{cancel: cancel, next: drain}
	}
}
