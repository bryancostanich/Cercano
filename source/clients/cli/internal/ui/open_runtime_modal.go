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

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
	"github.com/charmbracelet/x/ansi"
)

// runtimeModalState walks a small state machine driven by user keypress and
// the InstallOpenRuntime stream.
type runtimeModalState int

const (
	// runtimeModalIdle is the initial state — the modal shows the error,
	// the suggested command, and prompts the user to install or cancel.
	runtimeModalIdle runtimeModalState = iota
	// runtimeModalRunning means the install RPC is streaming. Buttons are
	// hidden and we accumulate log lines.
	runtimeModalRunning
	// runtimeModalDone means the install completed successfully and the
	// OpenRuntimeStatusChanged event flipped ok=true. Auto-dismisses on
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
	// runtimeModalOfferSwitch means the install completed successfully
	// but the runtime we just made available (llama_server) is not the
	// runtime currently selected in config.local_runtime. Ask the user
	// whether to make it the active runtime. This is only entered when
	// the modal was opened from the F1 status-chip path — the settings-
	// dropdown path pre-queues the switch via pendingRuntimeSwitch and
	// bypasses this state.
	runtimeModalOfferSwitch
	// runtimeModalScanningModels is the opening state when detection said
	// the BINARY is fine but the model is missing/ambiguous. Installing
	// the runtime can't help, so instead of idle's "Install now" we fetch
	// the discovered GGUF list to decide: zero models → NeedsModel
	// (browse/download), one or more → PickModel (disambiguate).
	runtimeModalScanningModels
	// runtimeModalPickModel lists the GGUFs detection found so the user
	// can pick one as llama_server.default_model. Enter dispatches the
	// pick + runtime switch in one UpdateConfig round trip.
	runtimeModalPickModel
)

// openRuntimeInstallModal owns the modal's transient state — the reason we
// opened (a snapshot of the status event), the state machine, buffered log
// lines, and the terminal error if any. Kept separate from Model so tests
// can exercise state transitions without wiring the full TUI.
type openRuntimeInstallModal struct {
	// status is the snapshot that opened the modal; it's what we render at
	// the top ("llama-server not installed" etc). Not updated live — we
	// close on success rather than mutating in place.
	status agentclient.OpenRuntimeStatus
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
	// offerRuntime / activeRuntime carry the copy for runtimeModalOfferSwitch.
	// offerRuntime is the runtime id we'd activate (e.g. "llama_server");
	// activeRuntime is what's currently active (e.g. "ollama") so the
	// [Esc] Keep <name> label can name it correctly.
	offerRuntime  string
	activeRuntime string
	// picker drives runtimeModalPickModel: the shared RowList overlay
	// listing the downloaded llama_server GGUFs detection found. Replaces
	// the bespoke pick widget so selection matches the wizard and /m
	// dashboard; its OnSelect commits default_model + runtime. Nil outside
	// the pick step.
	picker *overlay.RowList
}

// modalOpensScanning reports whether the modal should open in the
// scanning-models state instead of idle: detection said the binary is fine
// but the model is missing/ambiguous, so idle's "Install now" can't help.
// Callers that see true must batch fetchModalGGUFsCmd alongside opening.
func modalOpensScanning(st agentclient.OpenRuntimeStatus) bool {
	return st.Runtime == "llama_server" && st.Missing == "model"
}

func modalIsBundledModelMissing(st agentclient.OpenRuntimeStatus) bool {
	return st.Runtime == "mistralrs" && st.Missing == "model"
}

// newOpenRuntimeInstallModal opens the modal carrying the status snapshot
// that triggered it. Missing=="model" starts in scanning state (the caller
// must batch fetchModalGGUFsCmd — see modalOpensScanning); everything else
// starts idle. Caller stashes the returned pointer on Model and re-renders.
func newOpenRuntimeInstallModal(st agentclient.OpenRuntimeStatus) *openRuntimeInstallModal {
	state := runtimeModalIdle
	if modalOpensScanning(st) {
		state = runtimeModalScanningModels
	}
	return &openRuntimeInstallModal{status: st, state: state}
}

// setPickModel transitions to the GGUF picker, building the shared RowList
// overlay from the discovered models. OnSelect commits the chosen GGUF as
// llama_server.default_model and switches the runtime in one round trip
// (runtime defaults to llama_server on the F1 path where no switch was
// queued). ag may be nil in tests — the dispatch cmd is only run on Enter.
func (mo *openRuntimeInstallModal) setPickModel(ag *agentclient.Client, runtime string, models []agentclient.RuntimeModel, sysRAMBytes int64) {
	mo.state = runtimeModalPickModel
	if runtime == "" {
		runtime = "llama_server"
	}
	rows := make([]overlay.Row, 0, len(models))
	for _, mdl := range models {
		annot := mdl.Quantization
		if mdl.SizeBytes > 0 {
			if annot != "" {
				annot += "  "
			}
			annot += formatBytes(mdl.SizeBytes)
		}
		// Surface a coarse fit verdict at load time — this picker is the one
		// path that loads a model without showing the dashboard's fit line.
		if fit := compactFitAnnot(mdl.SizeBytes, sysRAMBytes); fit != "" {
			if annot != "" {
				annot += "  "
			}
			annot += fit
		}
		rows = append(rows, overlay.Row{
			Key:   mdl.Path,
			Label: firstNonEmpty(mdl.DisplayName, mdl.Path),
			Value: annot,
		})
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			return "", true, dispatchOpenModelPick(ag, runtime, row.Key)
		},
	}
	picker := overlay.New("pick a GGUF model", rows, hooks)
	mo.picker = &picker
}

// pickerBoxWidth sizes the GGUF picker panel to match the /m dashboard's
// floating tier picker: a touch under the frame, capped for readability.
func pickerBoxWidth(frameW int) int {
	boxW := frameW - 8
	if boxW > 72 {
		boxW = 72
	}
	if boxW < 40 {
		boxW = 40
	}
	return boxW
}

// modalModelsLoadedMsg carries the reply to fetchModalGGUFsCmd — the
// downloaded llama_server GGUFs, or the fetch error.
type modalModelsLoadedMsg struct {
	models      []agentclient.RuntimeModel
	sysRAMBytes int64 // from the catalog; drives the picker's fit glyph
	err         error
}

// fetchModalGGUFsCmd lists runtime models and filters to downloaded
// llama_server GGUFs — the same set detection scanned. The reply routes the
// scanning state to NeedsModel (zero) or PickModel (one or more).
func fetchModalGGUFsCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		catalog, err := ag.ListRuntimeModels(ctx)
		if err != nil {
			return modalModelsLoadedMsg{err: err}
		}
		var ggufs []agentclient.RuntimeModel
		for _, mdl := range catalog.Models {
			if mdl.Runtime == "llama_server" && mdl.DownloadState == "downloaded" {
				ggufs = append(ggufs, mdl)
			}
		}
		return modalModelsLoadedMsg{models: ggufs, sysRAMBytes: catalog.SystemRAMBytes}
	}
}

// openOpenRuntimeInstallModalMsg is emitted from the settings page when the
// user tries to switch to a runtime that isn't ready. Carries the status
// snapshot to render in the idle state, plus the runtime id whose switch is
// queued to fire after a successful install. If the user cancels, the
// pending switch is dropped — no UpdateConfig is dispatched, and the
// runtime toggle reverts to whatever the server currently has.
type openOpenRuntimeInstallModalMsg struct {
	status  agentclient.OpenRuntimeStatus
	pending string // runtime id to switch to on install success ("" = none)
}

// appendLog records one line of streamed install output. Trimming trailing
// whitespace keeps the render tidy even if the subprocess emits CR+LF.
func (mo *openRuntimeInstallModal) appendLog(line string) {
	mo.logLines = append(mo.logLines, strings.TrimRight(line, " \r\n\t"))
}

// setFailed transitions to the failed state carrying an error message.
func (mo *openRuntimeInstallModal) setFailed(msg string) {
	mo.state = runtimeModalFailed
	mo.errMsg = msg
}

// setNeedsModel transitions to the "install succeeded, but pick a model"
// state. Retry is disabled here — retrying the install can't fix a
// missing / ambiguous GGUF selection.
func (mo *openRuntimeInstallModal) setNeedsModel(msg string) {
	mo.state = runtimeModalNeedsModel
	mo.errMsg = msg
}

// setOfferSwitch transitions to the "install succeeded, switch runtime?"
// state. offerRuntime is the runtime id (e.g. "llama_server") that would
// be activated on Enter; activeRuntime is the currently-active id used
// to render the "keep <name>" option so users know what they're
// declining.
func (mo *openRuntimeInstallModal) setOfferSwitch(offerRuntime, activeRuntime string) {
	mo.state = runtimeModalOfferSwitch
	mo.offerRuntime = offerRuntime
	mo.activeRuntime = activeRuntime
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
// frame. Most states stay compact and content-sized; install/log states keep
// a taller pane so streamed output has room to breathe.
func (mo *openRuntimeInstallModal) View(styles theme.Styles, palette theme.Palette, frameW, frameH int) string {
	if mo.picker != nil {
		return mo.picker.ViewPanel(pickerBoxWidth(frameW), palette, styles)
	}
	boxW, boxH := mo.modalDim(frameW, frameH)

	// Content.
	var sections []string
	sections = append(sections, mo.renderHeader(styles))
	sections = append(sections, "")
	sections = append(sections, mo.renderBody(styles, boxW-4)) // -4 for border padding
	if mo.shouldRenderLogPane() {
		sections = append(sections, "")
		sections = append(sections, mo.renderLogs(styles, boxW-4, boxH-11))
	}
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
func (mo *openRuntimeInstallModal) modalDim(frameW, frameH int) (int, int) {
	if mo.picker != nil {
		// Width is all the compositor needs; it centers on the rendered
		// panel's line count.
		return pickerBoxWidth(frameW), 0
	}
	boxW := 80
	if mo.state == runtimeModalOfferSwitch {
		boxW = 72
	}
	if frameW-4 < boxW {
		boxW = frameW - 4
	}
	if boxW < 40 {
		boxW = 40
	}

	boxH := 12
	if mo.shouldRenderLogPane() {
		boxH = frameH - 6
		if boxH < 12 {
			boxH = 12
		}
		if boxH > 24 {
			boxH = 24
		}
	}
	if frameH-2 < boxH {
		boxH = frameH - 2
	}
	if boxH < 8 {
		boxH = 8
	}
	return boxW, boxH
}

func (mo *openRuntimeInstallModal) shouldRenderLogPane() bool {
	switch mo.state {
	case runtimeModalRunning, runtimeModalFailed, runtimeModalScanningModels:
		return true
	default:
		return false
	}
}

func (mo *openRuntimeInstallModal) renderHeader(styles theme.Styles) string {
	var title string
	switch mo.state {
	case runtimeModalIdle:
		switch mo.status.Missing {
		case "binary":
			title = "Install llama-server?"
		case "model":
			if mo.status.Runtime == "mistralrs" {
				title = "mistral.rs model needed"
			} else {
				title = "Add a GGUF model?"
			}
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
		if mo.status.Runtime == "mistralrs" {
			title = "mistral.rs needs a model"
		} else {
			title = "llama-server ready — pick a GGUF model"
		}
	case runtimeModalOfferSwitch:
		if mo.offerRuntime == "mistralrs" {
			title = "Switch to mistral.rs?"
		} else {
			title = "llama-server ready — switch to it?"
		}
	case runtimeModalScanningModels:
		title = "Checking GGUF models…"
	}
	return styles.Primary.Bold(true).Render(title)
}

func (mo *openRuntimeInstallModal) renderBody(styles theme.Styles, w int) string {
	if mo.state == runtimeModalOfferSwitch && mo.offerRuntime == "mistralrs" && mo.status.Missing == "model" {
		return mo.renderMistralDownloadOfferBody(styles, w)
	}

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

func (mo *openRuntimeInstallModal) renderMistralDownloadOfferBody(styles theme.Styles, w int) string {
	model := strings.TrimSpace(mo.status.DefaultModel)
	if model == "" {
		model = "the configured default model"
	}
	lines := []string{
		"mistral.rs needs its default model before it can run:",
		"",
		"  " + model,
		"",
		"Cercano will switch runtimes now and download the model in the background.",
	}
	for i, line := range lines {
		if ansi.StringWidth(line) > w {
			lines[i] = ansi.Wrap(line, w, "")
		}
	}
	return styles.Muted.Render(strings.Join(lines, "\n"))
}

func runtimeDisplayName(runtime string) string {
	switch runtime {
	case "llama_server":
		return "llama-server"
	case "mistralrs":
		return "mistral.rs"
	case "":
		return "ollama"
	default:
		return runtime
	}
}

func (mo *openRuntimeInstallModal) renderLogs(styles theme.Styles, w, h int) string {
	if mo.state == runtimeModalIdle {
		return styles.Muted.Render("(logs will appear here once install starts)")
	}
	// The log-pane slot doubles as the picker region — there are never
	// install logs and a model list on screen at the same time.
	if mo.state == runtimeModalScanningModels {
		return styles.Muted.Render("(scanning model directories…)")
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

func (mo *openRuntimeInstallModal) renderActions(styles theme.Styles) string {
	switch mo.state {
	case runtimeModalIdle:
		primary := styles.Success.Bold(true).Render("[Enter] Install now")
		secondary := styles.Muted.Render("[Esc] Cancel")
		return primary + "    " + secondary
	case runtimeModalScanningModels:
		return styles.Muted.Render("Scanning… ([Esc] Cancel)")
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
		primary := styles.Success.Bold(true).Render("[Enter] Browse models")
		secondary := styles.Muted.Render("[Esc] Close")
		hintText := "Or, out-of-band:\n" +
			"  • add a .gguf to ~/.cercano/models/\n" +
			"  • or set llama_server.default_model in ~/.config/cercano/config.yaml"
		if mo.status.Runtime == "mistralrs" {
			hintText = "Choose/download a mistral.rs model from the runtime dashboard,\n" +
				"or set mistralrs.default_model in ~/.config/cercano/config.yaml."
		}
		hint := styles.Muted.Render(hintText)
		return primary + "    " + secondary + "\n" + hint
	case runtimeModalOfferSwitch:
		// Ask explicitly rather than deciding. For bundled runtimes with a
		// configured-but-missing default model, switching starts the server-side
		// background download and the top-bar chip moves to o:downloading.
		label := "[Enter] Switch to " + runtimeDisplayName(mo.offerRuntime)
		if mo.offerRuntime == "mistralrs" && mo.status.Missing == "model" {
			label = "[Enter] Switch and download"
		}
		primary := styles.Success.Bold(true).Render(label)
		keepLabel := "[Esc] Stay on " + runtimeDisplayName(mo.activeRuntime)
		secondary := styles.Muted.Render(keepLabel)
		return primary + "    " + secondary
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

// handleOpenRuntimeModalKey resolves a key while the install modal is
// open. Returns the new Model + any cmd. Modal-active state is the only
// place these bindings apply — global key handling stays untouched.
func (m Model) handleOpenRuntimeModalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.openRuntimeModal == nil {
		return m, nil
	}
	key := msg.String()
	switch m.openRuntimeModal.state {
	case runtimeModalIdle:
		if key == "enter" {
			runtime := m.openRuntimeModal.status.Runtime
			if runtime == "" {
				runtime = "llama_server"
			}
			m.openRuntimeModal.state = runtimeModalRunning
			return m, startOpenRuntimeInstallCmd(m.agent, runtime)
		}
		if key == "esc" || key == "q" {
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = "" // user rejected the switch
		}
	case runtimeModalRunning:
		if key == "esc" {
			// Cancel the install: the stream context gets torn down
			// when the modal closes; the server-side subprocess is
			// killed and the drain loop exits. The queued switch is
			// dropped — the runtime isn't ready.
			if m.openRuntimeModal.cancel != nil {
				m.openRuntimeModal.cancel()
			}
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
		}
	case runtimeModalDone:
		// Any key dismisses. pendingRuntimeSwitch was already cleared
		// by runtimeInstallDoneMsg (either dispatched or dropped).
		m.openRuntimeModal = nil
	case runtimeModalFailed:
		if key == "enter" {
			runtime := m.openRuntimeModal.status.Runtime
			if runtime == "" {
				runtime = "llama_server"
			}
			m.openRuntimeModal.state = runtimeModalRunning
			m.openRuntimeModal.errMsg = ""
			m.openRuntimeModal.logLines = nil
			return m, startOpenRuntimeInstallCmd(m.agent, runtime)
		}
		if key == "esc" || key == "q" {
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = "" // failed install; drop the queued switch too
		}
	case runtimeModalNeedsModel:
		// Enter or "b" opens the runtime dashboard where the online
		// catalog is browsable. Anything else dismisses (user chose
		// to add a GGUF out-of-band).
		if key == "enter" || key == "b" {
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
			return m, openRuntimeDashboardCmd()
		}
		m.openRuntimeModal = nil
		m.pendingRuntimeSwitch = ""
	case runtimeModalOfferSwitch:
		if key == "enter" {
			// Confirmed: fire UpdateConfig(local-runtime=offerRuntime).
			runtime := m.openRuntimeModal.offerRuntime
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
			return m, dispatchOpenRuntimeSwitch(m.agent, runtime)
		}
		if key == "esc" || key == "q" {
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
		}
	case runtimeModalScanningModels:
		// The GGUF-list fetch is in flight; only cancel is meaningful.
		if key == "esc" || key == "q" {
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
		}
	case runtimeModalPickModel:
		if m.openRuntimeModal.picker == nil {
			return m, nil
		}
		next, cmd, closed := m.openRuntimeModal.picker.Update(msg, m.styles)
		if closed {
			// Enter (OnSelect → dispatch cmd) and Esc (cmd nil) both close
			// the modal; the pick's default_model + runtime switch rides
			// out on cmd.
			m.openRuntimeModal = nil
			m.pendingRuntimeSwitch = ""
			return m, cmd
		}
		m.openRuntimeModal.picker = &next
		return m, cmd
	}
	return m, nil
}

// dispatchOpenModelPick fires the picker's outcome in the background: set
// llama_server.default_model to the chosen GGUF and switch to the runtime,
// one UpdateConfig round trip. Errors are swallowed for the same reason as
// dispatchOpenRuntimeSwitch — the server broadcasts ConfigChanged /
// OpenRuntimeStatusChanged either way, so outcomes surface via the chip.
func dispatchOpenModelPick(ag *agentclient.Client, runtime, ggufPath string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, agentclient.ConfigUpdate{
			OpenRuntime:      runtime,
			OpenDefaultModel: ggufPath,
		})
		return nil
	}
}

// dispatchOpenRuntimeSwitch fires an UpdateConfig(local-runtime=runtime) in
// the background after a successful install. Errors are swallowed — the
// server broadcasts its own ConfigChanged / OpenRuntimeStatusChanged events
// so any status change becomes visible via the normal channels without a
// custom msg round-trip.
func dispatchOpenRuntimeSwitch(ag *agentclient.Client, runtime string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ag.UpdateConfig(ctx, agentclient.ConfigUpdate{OpenRuntime: runtime})
		return nil
	}
}

// runtimeInstallStartedMsg is the first message emitted by
// startOpenRuntimeInstallCmd — it wires the RPC's cancel func back to
// Model so Esc-during-running can abort the subprocess, and it kicks off
// the drain loop. err is set when opening the stream itself failed.
type runtimeInstallStartedMsg struct {
	cancel context.CancelFunc
	next   tea.Cmd
	err    error
}

// startOpenRuntimeInstallCmd opens the InstallOpenRuntime streaming RPC
// and returns a bubbletea command whose first message is a
// runtimeInstallStartedMsg carrying (a) the cancel func Model must stash on
// the modal, and (b) a drain cmd that emits one InstallProgress frame per
// tick and re-arms itself until the stream terminates. Runtime is the
// server-side runtime id ("llama_server" for now).
func startOpenRuntimeInstallCmd(ag *agentclient.Client, runtime string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := ag.InstallOpenRuntime(ctx, runtime)
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

// openRuntimeDashboardMsg is emitted by the install modal's "Browse
// models" action. Model's Update handler catches it and swaps the
// content page to a fresh runtime dashboard — same as pressing the
// isRuntimeDashboardKey (Cmd+M).
type openRuntimeDashboardMsg struct{}

// openRuntimeDashboardCmd wraps openRuntimeDashboardMsg in a tea.Cmd so
// key handlers can request the transition without knowing about the
// dashboard type.
func openRuntimeDashboardCmd() tea.Cmd {
	return func() tea.Msg { return openRuntimeDashboardMsg{} }
}
