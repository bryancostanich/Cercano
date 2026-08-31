// Package ui hosts the Bubble Tea root model for cercano-cli.
package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/banner"
	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/slash"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/clients/cli/internal/uiconfig"
	"cercano/source/server/pkg/agentclient"
)

// Role tags a scrollback entry's origin so the renderer can style it.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
	RoleSystem   // /help output, errors, progress notes
	RoleDivider  // full-width horizontal rule with a centered label, used to mark the freeze boundary on resume
	RoleWatchdog // supervisor callout (challenge/block header + body, or dim echo line)
)

// Entry is one item in the scrollback. Stored raw; re-wrapped on every render.
type Entry struct {
	Role      Role
	Content   string // grows live for streaming assistant turns
	Streaming bool   // true while tokens are flowing in

	// SubAgentStart, when non-nil, renders the synthetic launch metadata for a
	// delegated child tab as a measured card. Role/Content are ignored; the card
	// is live-updated as the start, tool-grant, and prompt events arrive.
	SubAgentStart *subAgentStartEntry
	// Status is the current pre-stream progress note (e.g. "classifying
	// intent", "selecting provider", "generating response"). Set by
	// progress messages while Content is empty; shown in place of the
	// "thinking…" placeholder. Cleared as soon as tokens start arriving.
	Status string

	// Superseded marks an assistant reply that a watchdog challenge censored
	// and the model then rewrote (rather than overruling via justify). It
	// renders folded to a dim one-liner; a click expands it back to the full
	// body (SupersededOpen), with the same left rail to collapse as tool
	// entries. Only meaningful on RoleAssistant entries.
	Superseded     bool
	SupersededOpen bool

	// Tool, when non-nil, makes this entry a tool-call line — Role/Content
	// are ignored and renderToolEntry produces the visible row. expand /
	// collapse via tab-focus is a follow-up; V1 renders folded.
	Tool *ToolEntry

	// Banner, when non-nil, makes this entry the CERCANO wordmark banner —
	// Role/Content are ignored. Rendered static (no shimmer) from the live
	// palette at paint time, so it recolors on theme switches; falls back to
	// a compact one-liner when the viewport is narrower than the banner.
	Banner *banner.Meta
}

// Model is the Bubble Tea root model.
type Model struct {
	width, height int

	// scrollbarTop is the absolute screen row of the viewport's first line,
	// used to hit-test scrollbar mouse events. Set in relayout().
	scrollbarTop int

	// stripShown records whether the chat tab strip occupied its rows at the
	// last relayout(). refreshViewport() re-lays-out when this drifts from the
	// live strip state, so a tab appearing or vanishing between layouts (a
	// sub-agent tab created mid-turn, or the last one closed) cannot leave bodyH
	// and scrollbarTop stale — which pushes the status bar off-screen and
	// misaligns the tab-click row until some other event forces a relayout.
	stripShown bool

	// contentScrollbarDragging is the same gesture, but for a reusable content
	// page scrollbar rather than the main chat scrollback.
	contentScrollbarDragging bool

	// root and home are resolved once at construction; used to humanize tool-call
	// path arguments (relative to the project root, ~-abbreviated under home).
	root string
	home string

	palette theme.Palette
	styles  theme.Styles
	theme   theme.Theme
	themes  *theme.Registry

	agent  *agentclient.Client
	convID string

	registry *slash.Registry

	splashShown     bool // hide after first user input
	splash          banner.AnimModel
	chatTabs        chatTabSurface
	selectionNotice string
	input           promptInput

	// inputHistory holds every submitted prompt (messages and slash commands),
	// oldest first, for shell-style ↑/↓ recall. historyIdx is the browse
	// position: len(inputHistory) means "at the live input". historyStash holds
	// the unsubmitted input saved when browsing begins, restored on ↓ past the
	// newest entry.
	inputHistory []string
	historyIdx   int
	historyStash string

	streaming bool
	// cancelStream cancels the context of the in-flight StreamChat so Esc can
	// abort a running prompt. Nil when nothing is streaming. The driver returns
	// it from Submit; the host stores it here.
	cancelStream context.CancelFunc
	// turnGen identifies the live turn. Bumped on every submit AND every
	// cancel; stream events carry the gen they were born under, and events
	// whose gen doesn't match are ghosts of a dead turn — drained, never
	// applied. Without this, a canceled turn's late error/close events pass
	// the m.streaming guard once the next turn starts, paint "context
	// canceled" into the new transcript, and worst of all run the completion
	// path that invokes m.cancelStream — the NEW turn's cancel func.
	turnGen int

	tokIn, tokOut           int
	cumIn, cumOut           int
	ctxRaw                  int
	ctxMessageTokens        int
	ctxSystemTokens         int
	ctxToolSchemaTokens     int
	ctxOutputReserveTokens  int
	ctxEstimatedRequest     int
	ctxWindowKnown          bool
	compacting              bool
	ctxPollTicks            int
	ctxPolling              bool      // a ctxUsageTick loop is currently running (avoid double-scheduling)
	animTickActive          bool      // a progressAnimTick loop is currently running (avoid double-scheduling)
	lastAnimViewportRefresh time.Time // last expensive viewport rebuild done only to advance an animation glyph
	// chatDirty marks transcript changes whose repaint was deferred to the
	// next progressAnimTick frame. High-frequency stream events (token
	// deltas, progress notes) set it instead of rebuilding per event, so the
	// rebuild rate is capped at the tick rate instead of the token rate.
	chatDirty bool
	// bannerTickActive tracks whether the banner.TickMsg chain is alive — it
	// serves the splash first, then the scrollback banner. applyResume checks
	// it to restart the loop when resuming without ever having shown a splash.
	bannerTickActive   bool
	lastLatencyMs      int
	modelMaxTokens     int
	openModel          string // configured local/open model name, used for the o: header chip
	lastModel          string // model/provider that served the last completed turn
	cloudModel         string // cloud model name (from config), used for the c: header chip; empty when no cloud configured
	activeCloudProfile string // active cloud profile name, used as a fallback when the model is unknown
	cloudState         string // "" = unknown, "NONE" = absent, "ok" = real cloud configured
	ctrlCArmed         bool   // first ctrl-c on empty input arms quit; any other key disarms
	errMsg             string

	// Live turn telemetry, surfaced by renderStatus while a turn streams. Reset
	// when a turn begins; the engine fields fill in on the RouteSelected event.
	turnStart       time.Time // wall clock when streaming began (for elapsed)
	turnActivity    string    // current verb: thinking → routing → running <tool> → writing
	turnTokOut      int       // output tokens seen so far this turn (approximate, live)
	turnModel       string    // engine handling the turn (from RouteSelected)
	turnCloud       bool      // true when the turn routed to a cloud engine
	turnToolStarted int       // tool calls started in this turn, for long-turn progress visibility
	turnToolDone    int       // tool executions completed in this turn, for long-turn progress visibility
	hadTurn         bool      // a turn has completed; gate the idle token counter

	content contentPage

	// configSurface is non-nil while the unified /config tabbed surface is
	// open; m.content then holds the active tab's page. See config_surface.go.
	configSurface *configSurface

	recap string // living one-line work summary; shown in the chat footer

	// taskPane is the V1 task drawer: a right-side collapsible pane. It is only a
	// shell for now; TaskChange consumption fills it in later. Kept as layout
	// state rather than hardcoding "right pane" into the task model so future
	// docking (left/right/top/bottom) can replace this without touching task data.
	taskPane taskPaneState

	// nextPromptSuggestion is a locally-generated "what to do next" one-liner
	// fetched after each streamEndMsg. Renders as ghost text in the empty
	// input (via input.Suggestion); Tab accepts it into the value. Overwritten
	// by the next successful fetch; not actively cleared on typing (the
	// promptInput render already hides ghost text when value is non-empty).
	nextPromptSuggestion string

	// convRef shares the current convID with the slash registry by reference,
	// so /rename always targets whatever conversation the model currently has
	// active (including after /resume).
	convRef *struct{ id string }

	// wdRef shares the current workDirOverride with the slash registry by
	// reference, so /context always reads from the active work dir (set by /d,
	// cleared by /clear and /resume).
	wdRef *struct{ dir string }

	openHistoryOnStart bool // -r flag → open the history picker after first WindowSizeMsg
	openWizardOnStart  bool // -s/-setup, first run, or wizard resume → open the setup wizard after first WindowSizeMsg

	// promptBorderColor is the color of the lines immediately above and
	// below the input row. Defaults to the palette's accent (lime). /color
	// sets it at runtime.
	promptBorderColor color.Color

	// promptColorToken is the token form of promptBorderColor ("palette:<key>"
	// or "#RRGGBB"), kept so the settings page can show the current selection.
	promptColorToken string

	// sessionTitle is the current conversation's display title. Shown in the
	// header. Empty for fresh sessions (header omits the title slot until
	// /rename or /resume sets one).
	sessionTitle string

	// headerSelection backs drag-select/copy for the centered session title. The
	// terminal mouse mode used for scrollback selection prevents native terminal
	// selection, so header text needs the same app-owned selection path.
	headerSelection textSelection

	// toolCache is the registry of available tools, fetched at startup so
	// the CLI can decide locally (no extra RPC) whether to prompt before
	// invoking a tool. Keyed by tool name.
	toolCache map[string]agentclient.ToolInfo

	// pendingConfirm carries a pending confirmation gate waiting on a y/n/esc
	// (and optional extra) keypress. While non-nil, all key events route to
	// the confirm resolver instead of the input or scrollback.
	pendingConfirm *confirmRequest

	// composeToolUseID is set while the user composes a "chat about this"
	// redirect after pressing [c] on a tool confirm: the prompt is dismissed,
	// keys flow to the input, and enter sends the text as a FollowUp denial
	// (server records it as the tool_result and continues the turn). esc/ctrl+c
	// cancels with a plain deny.
	composeToolUseID string

	// permissionMode caches the agent's current session permission mode
	// ("strict" | "permissive" | "bypass") so the status bar can render a
	// colored chip without an RPC round-trip every frame. Updated by the
	// startup fetch (permissionModeMsg) and by the /strict /permissive
	// /bypass /mode slash handlers.
	permissionMode string

	// sessionProfile is the active capability profile for THIS conversation
	// ("" / "default" = unrestricted; "plan" = read-only planning fence). It is
	// orthogonal to permissionMode and rendered alongside it in the mode chip
	// (e.g. "mode: planning | bypass"). Kept live by sessionProfileChangedMsg,
	// which the agent broadcasts whenever the profile flips (via /plan, an
	// approved suggest_plan, plan_exit, or request_plan_approval).
	sessionProfile string

	// workDirOverride, when non-empty, replaces os.Getwd() as the work_dir
	// sent with every turn. Set by /d (development mode); empty = normal.
	workDirOverride string

	// supportsVision is true when the active provider can accept image
	// inputs. Fetched once at startup via fetchVisionCmd. Used by
	// visionNotice to show a dim warning when images are attached but the
	// model can't see them. Interim until capability-aware routing lands.
	supportsVision bool

	// openRuntimeStatus is the most recent OpenRuntimeStatusChanged event
	// from the agent — populated on a config-driven runtime swap when the
	// server's headless detection can't find the binary or a model. nil
	// (or ok=true) means the chip stays hidden and the install modal is
	// not offered.
	openRuntimeStatus *agentclient.OpenRuntimeStatus

	// connState mirrors the SDK's connection health so the status bar can
	// render a "reconnecting…" chip when the agent server dies and the
	// reconnect loop is trying to bring it back. Zero value is Connected,
	// matching the SDK convention.
	connState      agentclient.ConnState
	connAttempt    int // current reconnect attempt number, 1-based (0 when Connected)
	connFailErrMsg string
	// lastSubmittedPrompt stashes the user's last submitted turn text so
	// that if the agent crashes mid-stream we can restore it into the
	// input for one-key re-submit. Cleared on successful streamEndMsg.
	lastSubmittedPrompt string

	// currentOpenRuntime mirrors config.open_runtime — updated by the
	// configLoadedMsg handler on startup and after each config edit.
	// The install modal reads this to decide whether to prompt the user
	// to switch runtimes after a successful install (only meaningful
	// when the install target differs from what's currently active).
	currentOpenRuntime string

	// openRuntimeModal is the open install-modal state (nil = closed). It
	// walks a small state machine — idle → running → done|failed — driven
	// by InstallOpenRuntime stream events.
	openRuntimeModal *openRuntimeInstallModal

	// pendingRuntimeSwitch, when non-empty, is a runtime id ("llama_server")
	// whose UpdateConfig(local-runtime=...) call is queued to fire once the
	// install-modal reports success. Set by openOpenRuntimeInstallModalMsg
	// (emitted by the settings page when the user picks a runtime that
	// isn't ready), cleared by every modal-close path (dispatched on
	// success, dropped on cancel/failed).
	pendingRuntimeSwitch string

	// chatgptLoginModal is the open ChatGPT subscription sign-in modal (nil =
	// closed), driven by the StartChatGPTLogin stream.
	chatgptLoginModal *chatgptLoginModal
	// claudeLoginModal is the open Claude subscription sign-in modal (nil =
	// closed), driven by the StartClaudeLogin loopback stream.
	claudeLoginModal *claudeLoginModal
}

// pendingToolCall is a queued tool invocation awaiting user confirmation.
type pendingToolCall struct {
	// ToolUseID identifies the paused server-side tool call. Set when the
	// confirm prompt is raised by a PermissionRequired streaming event;
	// empty for legacy local-/tool invocations that route directly through
	// InvokeTool. The y/n resolver uses it to RPC Allow/DenyToolCall back
	// to the agent so the server-side tool loop can unblock.
	ToolUseID   string
	Name        string
	Args        string
	Permission  string // "R" | "W" | "X" — R never reaches here, but kept for symmetry
	Destructive bool   // display-only ⚠ hint (MCP destructiveHint)
}

// confirmRequest is a generic confirmation gate. Any feature raises one; the
// model routes y / n / esc (and optional extra keys) to it. onYes/onNo
// resolve and should clear m.pendingConfirm; extras run without resolving.
type confirmRequest struct {
	tool    *pendingToolCall
	title   string
	details []string
	hints   string
	onYes   func(Model) (Model, tea.Cmd)
	onNo    func(Model) (Model, tea.Cmd)
	extras  map[string]func(Model) (Model, tea.Cmd)
	// retryPrompt is the user turn that produced this gate. Keep it on the gate
	// itself because the normal lastSubmittedPrompt rehydration cache is cleared
	// when the stream closes, which can happen before reconnect recovery marks the
	// gate stale.
	retryPrompt string
	// stale marks a tool-permission gate whose server-side turn died — e.g.
	// the agent restarted while the y/n/d/c prompt was up. The paused tool
	// call and its blocked waiter no longer exist in the fresh agent process,
	// so answering can never resolve it (AllowToolCall would Resolve nothing).
	// When set, resolveConfirmHotkey stops pretending the gate is live: it
	// short-circuits the Allow/Deny RPCs and, on yes, re-submits the user's
	// original prompt as a fresh turn instead of orphaning it.
	stale bool
}

const defaultInputPlaceholder = "type a message, /help for commands"
const steerInputPlaceholder = "type to queue"
const armedInputPlaceholder = "(press ^C again to quit, or type a message)"

// New builds the root model. The provided agent client must already be Dial'd.
// openHistoryOnStart=true makes the CLI open the /history picker as soon as
// the terminal size is known (used by the `cercano -r` flag).
func New(ag *agentclient.Client, openHistoryOnStart bool) Model {
	themes := theme.NewRegistry(theme.BuiltinThemes())
	for _, ct := range uiconfig.LoadCustomThemes() {
		_ = themes.Add(ct) // skip collisions silently
	}
	activeName := uiconfig.LoadActiveTheme()
	active, ok := themes.Get(activeName)
	if !ok {
		active, _ = themes.Get("cr4k3r_j4x")
	}
	p := active.Palette
	s := theme.NewStyles(p)

	ti := newPromptInput()
	ti.Placeholder = defaultInputPlaceholder
	// Grow/shrink to fit wrapped content, from one line up to the cap; beyond
	// the cap the prompt scrolls internally.
	ti.MinHeight = 1
	ti.MaxHeight = maxInputLines
	ti.Focus()

	reg := slash.New()
	slash.RegisterBasics(reg)
	slash.RegisterConfig(reg, ag)
	slash.RegisterColor(reg)
	slash.RegisterContextRegen(reg)
	slash.RegisterCompact(reg)
	slash.RegisterClearCompactedContext(reg)
	slash.RegisterElideContext(reg)
	slash.RegisterExport(reg)
	// wdRef is shared with the slash registry so /context tracks the active
	// workDir even after /d, /clear, or /resume update it.
	wdRef := &struct{ dir string }{}
	slash.RegisterContext(reg, func() string {
		if wdRef.dir != "" {
			return wdRef.dir
		}
		wd, _ := os.Getwd()
		return wd
	})
	slash.RegisterTools(reg, ag)
	slash.RegisterMcp(reg, ag)
	slash.RegisterPermissions(reg, ag)
	slash.RegisterPlan(reg, ag)
	slash.RegisterAuto(reg, ag)
	// currentConv is captured by reference so it always returns the active
	// conversation id even after /resume swaps it.
	convRef := &struct{ id string }{}
	slash.RegisterHistory(reg, ag, func() string { return convRef.id })
	slash.RegisterRuntime(reg)
	slash.RegisterLocus(reg, ag)
	slash.RegisterContextView(reg)
	slash.RegisterDev(reg)
	slash.RegisterRestartAgent(reg)
	slash.RegisterSettings(reg)
	slash.RegisterSetup(reg)
	slash.RegisterTheme(reg)

	splash := banner.NewAnimModel(p, banner.Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		// Model is left empty here and filled in by the configLoadedMsg
		// handler once the agent reports the active locus + model, so the
		// splash reflects the real primary profile instead of a hard-coded
		// name. The banner render omits the segment while it's empty.
	})

	initialConvID := newConvID()
	convRef.id = initialConvID

	// Resolved once for humanizing tool-call path args (relative-to-root, ~-home).
	root, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	m := Model{
		root:               root,
		home:               home,
		palette:            p,
		styles:             s,
		theme:              active,
		themes:             themes,
		agent:              ag,
		convID:             initialConvID,
		convRef:            convRef,
		wdRef:              wdRef,
		registry:           reg,
		splashShown:        !openHistoryOnStart,
		bannerTickActive:   true, // Init batches splash.Init(), which starts the chain
		splash:             splash,
		input:              ti,
		lastModel:          "qwen3-coder",
		modelMaxTokens:     128_000, // placeholder until the agent serves real ctx limits
		openHistoryOnStart: openHistoryOnStart,
		promptBorderColor:  p.Accent,
		promptColorToken:   "palette:accent",
	}
	m.setMainChat(newChatView(m.styles, m.palette, m.root, m.home, 80, 10))
	m.applyInputStyles()
	return m
}

func newConvID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SeedAssistantMarkdown pre-loads a finished assistant entry containing the
// given markdown, for the `--mdtest` render-testing mode. Hides the splash so
// the rendered doc is visible immediately. No agent round-trip occurs.
func (m Model) SeedAssistantMarkdown(doc string) Model {
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: doc})
	m.splashShown = false
	return m
}

// OpenWizardOnStart marks the setup wizard page to open on the first sized
// frame (used by -s / -setup, first run, and wizard resume). Chainable like
// SeedAssistantMarkdown; splash handling mirrors openHistoryOnStart.
func (m Model) OpenWizardOnStart() Model {
	m.openWizardOnStart = true
	m.splashShown = false
	return m
}

// applyInputStyles (re)applies the prompt marker + text/placeholder/selection
// styles from the current theme. Called at startup and on every theme switch so
// the live input line recolors like everything else.
func (m *Model) applyInputStyles() {
	s := m.styles
	p := m.palette
	m.input.SetPromptFunc(2, func(info promptInfo) string {
		if info.LineNumber == 0 {
			return s.UserPrompt.Render("▶ ")
		}
		return "  "
	})
	m.input.SetStyles(promptInputStyles{
		Text:        lipgloss.NewStyle().Foreground(p.Primary),
		Placeholder: lipgloss.NewStyle().Foreground(p.Muted),
		Selection:   lipgloss.NewStyle().Foreground(p.BgDeep).Background(p.Info),
		Chip:        lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
	})
}

// applyTheme swaps the active theme and live-repaints: rebuild styles, push them
// to the chat (which flushes its markdown cache), re-resolve the prompt border,
// and refresh.
func (m *Model) applyTheme(t theme.Theme) {
	m.theme = t
	m.palette = t.Palette
	m.styles = theme.NewStyles(t.Palette)
	m.ensureChatTabs()
	for _, tab := range m.chatTabs.tabs {
		tab.view.SetStyles(m.styles, m.palette)
	}
	m.applyInputStyles()
	m.promptBorderColor = m.resolvePromptColor(m.promptColorToken)
	if sp, ok := m.content.(*settingsPage); ok {
		sp.SetStyles(m.styles, m.palette)
	}
	m.refreshViewport()
}

// Init is called by Bubble Tea once at startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), m.splash.Init(), fetchConfigCmd(m.agent), fetchToolsCmd(m.agent), fetchPermissionModeCmd(m.agent), fetchOpenRuntimeStatusCmd(m.agent), fetchVisionCmd(m.agent), subscribeEventsCmd(m.agent), subscribeConnStateCmd(m.agent))
}

// permissionModeMsg carries the result of the startup GetPermissionMode RPC.
// Empty / errored fetches default to "permissive" so the chip always renders.
type permissionModeMsg struct {
	Mode string
}

func fetchPermissionModeCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		mode, err := ag.GetPermissionMode(ctx)
		if err != nil || mode == "" {
			return permissionModeMsg{Mode: "permissive"}
		}
		return permissionModeMsg{Mode: mode}
	}
}

// sessionProfileFetchedMsg carries the result of a GetSessionProfile fetch for a
// specific conversation. Used to seed the footer chip when RESUMING a
// conversation that may already be in planning mode — a brand-new conversation
// always starts unrestricted, so only the resume path needs this.
type sessionProfileFetchedMsg struct {
	convID  string
	profile string
}

// normalizeProfile collapses the unrestricted posture ("default") to "" so the
// footer chip's planning check (sessionProfile == "plan") and its "show nothing
// extra when unrestricted" behavior are plain equality tests.
func normalizeProfile(p string) string {
	if p == "default" {
		return ""
	}
	return p
}

func fetchSessionProfileCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		active, _, err := ag.GetSessionProfile(ctx, convID)
		if err != nil {
			return nil // non-fatal; the chip stays at its last value
		}
		return sessionProfileFetchedMsg{convID: convID, profile: active}
	}
}

// fetchOpenRuntimeStatusCmd asks the agent for the current local-runtime
// detection snapshot. Used on CLI startup so the chip appears immediately
// when the user is running with a partially-configured runtime (e.g. they
// edited local_runtime: llama_server in the yaml but never installed the
// binary). Push events cover subsequent state changes.
func fetchOpenRuntimeStatusCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		st, err := ag.GetOpenRuntimeStatus(ctx, "")
		if err != nil || st == nil {
			return nil
		}
		// Reuse the change-msg shape so the Update handler for it can
		// cover both initial-fetch and push-event paths uniformly. next
		// is nil — this is a one-shot, not a stream drain.
		return openRuntimeStatusChangedMsg{status: st}
	}
}

// visionCapsMsg carries the result of the startup GetProviderCapabilities RPC.
type visionCapsMsg struct{ supported bool }

// fetchVisionCmd asks the agent whether the active provider accepts image
// inputs and returns a visionCapsMsg. Errors are treated as unsupported so
// the warning fires when in doubt.
func fetchVisionCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		caps, err := ag.GetProviderCapabilities(ctx)
		if err != nil {
			return visionCapsMsg{supported: false}
		}
		return visionCapsMsg{supported: caps.SupportsVision}
	}
}

// visionNotice returns a dim warning when images are attached but the active
// model can't accept them. Interim UX until capability-aware routing lands.
func (m Model) visionNotice() string {
	if len(m.input.Attachments()) == 0 || m.supportsVision {
		return ""
	}
	return m.styles.Muted.Render("⚠ active model can't see images")
}

// toolsLoadedMsg carries the result of the startup ListTools RPC; populates
// m.toolCache so the confirm-prompt decision is local.
type toolsLoadedMsg struct {
	Tools []agentclient.ToolInfo
}

func fetchToolsCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tools, err := ag.ListTools(ctx)
		if err != nil {
			return toolsLoadedMsg{}
		}
		return toolsLoadedMsg{Tools: tools}
	}
}

// toolResultMsg is the async return value of an InvokeTool call.
type toolResultMsg struct {
	Name string
	Res  *agentclient.ToolResult
	Err  error
}

func invokeToolCmd(ag *agentclient.Client, name, argsJSON string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := ag.InvokeTool(ctx, name, argsJSON)
		return toolResultMsg{Name: name, Res: res, Err: err}
	}
}

// configLoadedMsg carries the result of the startup / post-edit GetConfig RPC.
type configLoadedMsg struct {
	OpenModel          string
	OpenRuntime        string
	CloudModel         string
	ActiveCloudProfile string
	CloudConfigured    bool
	LocusMode          string
}

// primaryModelName resolves the model that the current Locus Mode routes to
// first, for display in the banner. cloud_only / cloud_primary favor the cloud
// model (when a cloud provider is actually configured); the open (local) side
// is the fallback and the default for open_primary / open_only / unset.
func primaryModelName(openModel, cloudModel, locus string, cloudConfigured bool) string {
	cloudSide := locus == "cloud_only" || locus == "cloud_primary"
	if cloudSide && cloudConfigured && cloudModel != "" {
		return cloudModel
	}
	if openModel != "" {
		return openModel
	}
	return cloudModel
}

// fetchConfigCmd asks the agent for the current local + cloud model names so
// the header bar can render both. Called on Init and whenever the config
// editor closes so user edits flow into the header immediately.
func fetchConfigCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cfg, err := ag.GetConfig(ctx)
		if err != nil || cfg == nil {
			return configLoadedMsg{}
		}
		// Treat "cloud configured" as: the server's CloudState reports "ok" —
		// that flag reflects whether the router actually has a live cloud
		// provider registered (see server.GetConfig's cloud_state derivation).
		// The old check that summed legacy CloudProvider + CloudAPIKeySet +
		// CloudBaseURL fields broke when cloud config migrated to the
		// active-profile model, where those legacy fields can be empty even
		// while a profile with a keychain-stored key is happily routing.
		configured := cfg.CloudState == "ok"
		active := ""
		if view, err := ag.GetCloudProviders(ctx); err == nil {
			active = view.Active
		}
		return configLoadedMsg{
			OpenModel:          cfg.OpenModel,
			OpenRuntime:        cfg.OpenRuntime,
			CloudModel:         cfg.CloudModel,
			ActiveCloudProfile: active,
			CloudConfigured:    configured,
			LocusMode:          cfg.LocusMode,
		}
	}
}

// streamEndMsg signals the StreamChat channel closed (turn complete). Emitted
// by the mainAgentDriver's drain cmd on channel close. gen identifies the turn
// it belongs to — a canceled turn's late close must not run the completion
// path for the turn that replaced it.
type streamEndMsg struct{ gen int }

// ctxUsageMsg carries the result of an asynchronous GetContextUsage call.
type ctxUsageMsg struct {
	Used, Max              int
	Percent                float64
	Raw                    int
	MessageTokens          int
	SystemTokens           int
	ToolSchemaTokens       int
	OutputReserveTokens    int
	EstimatedRequestTokens int
	ContextWindowKnown     bool
	Compacting             bool
}

// fetchContextUsage produces a tea.Cmd that asks the agent for the live
// context-window meter and translates the response into a ctxUsageMsg.
func fetchContextUsage(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		u, err := ag.GetContextUsage(ctx, convID)
		if err != nil || u == nil {
			return ctxUsageMsg{}
		}
		return ctxUsageMsg{
			Used:                   u.TokensUsed,
			Max:                    u.ModelMax,
			Percent:                u.Percent,
			Raw:                    u.RawTokens,
			MessageTokens:          u.MessageTokens,
			SystemTokens:           u.SystemTokens,
			ToolSchemaTokens:       u.ToolSchemaTokens,
			OutputReserveTokens:    u.OutputReserveTokens,
			EstimatedRequestTokens: u.EstimatedRequestTokens,
			ContextWindowKnown:     u.ContextWindowKnown,
			Compacting:             u.Compacting,
		}
	}
}

// toolCallFetchedMsg carries the result of a lazy GetToolCall — the full args
// and result body for one expanded scrollback tool entry, keyed by tool_use_id.
type toolCallFetchedMsg struct {
	id     string
	detail *agentclient.ToolCallDetail
	err    error
}

// fetchToolCallCmd asks the agent for a tool call's full args + result body by
// tool_use_id (read from the persisted conversation). Backs expand-on-click of
// a folded scrollback tool entry.
func fetchToolCallCmd(ag *agentclient.Client, convID, toolUseID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d, err := ag.GetToolCall(ctx, convID, toolUseID)
		return toolCallFetchedMsg{id: toolUseID, detail: d, err: err}
	}
}

// dispatchToolFetches drains any tool bodies the chat queued for lazy load
// (from an expand toggle) and returns the fetch commands, kicking the
// animation tick so the loading spinner animates while they're in flight.
func (m *Model) dispatchToolFetches() []tea.Cmd {
	ids := m.mainChat().TakePendingToolFetches()
	if len(ids) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(ids)+1)
	for _, id := range ids {
		cmds = append(cmds, fetchToolCallCmd(m.agent, m.convID, id))
	}
	if kick := m.ensureAnimTick(); kick != nil {
		cmds = append(cmds, kick)
	}
	return cmds
}

type recapLoadedMsg struct{ recap string }

// nextPromptSuggestionMsg carries the result of a SuggestNextPrompt call.
// Empty suggestion means the server couldn't produce one (missing recap,
// dispatch engine offline, etc.) — the CLI treats "" as "no ghost text",
// never renders a banner.
type nextPromptSuggestionMsg struct{ suggestion string }

// fetchNextPromptSuggestion asks the agent's local coproc for a one-line
// "what to do next" ghost. Longer timeout than the recap poll because
// generation runs a small local-model completion; still bounded so a stuck
// coproc doesn't hang the poll cycle.
func fetchNextPromptSuggestion(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		if convID == "" {
			return nextPromptSuggestionMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		s, err := ag.SuggestNextPrompt(ctx, convID)
		if err != nil {
			return nextPromptSuggestionMsg{}
		}
		return nextPromptSuggestionMsg{suggestion: strings.TrimSpace(s)}
	}
}

// fetchRecap asks the agent for the conversation's latest living recap.
func fetchRecap(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		if convID == "" {
			return recapLoadedMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info, err := ag.GetConversation(ctx, convID)
		if err != nil {
			return recapLoadedMsg{}
		}
		return recapLoadedMsg{recap: recapDisplay(info)}
	}
}

// recapDisplay returns what the recap slot should show for a conversation:
// the real recap when present, an "unavailable" placeholder when the
// conversation has enough turns to have generated one but never has (a
// signal that the local recap model is misconfigured or offline), or "" when
// it's too early to conclude anything. The recapUnavailableMinTurns floor
// keeps a placeholder from flickering on brand-new conversations before the
// first debounced generation has had a chance to run.
func recapDisplay(info agentclient.ConversationInfo) string {
	if info.Recap != "" {
		return info.Recap
	}
	if info.TurnCount >= recapUnavailableMinTurns && info.RecapUpdatedAt.IsZero() {
		return "recap unavailable — check /config local-runtime"
	}
	return ""
}

const recapUnavailableMinTurns = 4

// progressAnimTickMsg fires every ~50ms while a streaming assistant entry is
// awaiting its first token. Triggers a View re-render so the per-char sweep
// over the status text advances.
type progressAnimTickMsg time.Time

func progressAnimTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg { return progressAnimTickMsg(t) })
}

// ensureAnimTick arms the shared animation/repaint tick loop unless one is
// already in flight. Returns nil when already armed — safe to pass straight
// into tea.Batch, which drops nil cmds.
func (m *Model) ensureAnimTick() tea.Cmd {
	if m.animTickActive {
		return nil
	}
	m.animTickActive = true
	return progressAnimTick()
}

// ctxUsageTickMsg fires after a 2-second delay to re-poll context usage during
// the background-compaction window.
type ctxUsageTickMsg struct{}

// ctxUsageTick polls the context meter on a slow cadence so the footer catches
// background compaction (which fires ~debounce seconds after a turn, off the
// request path).
func ctxUsageTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ctxUsageTickMsg{} })
}

// Update is the Bubble Tea reducer.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	start := time.Now()
	defer func() { m.logSlowUpdate(start, msg) }()

	switch msg := msg.(type) {

	case tea.ColorProfileMsg:
		// TEMPORARY INSTRUMENTATION: record each color-profile change so we can
		// diagnose the paste color-loss bug. Remove once diagnosed.
		LogColorProfileMsg(msg.Profile)
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		// Propagate the new size to any open overlay so it re-renders at
		// the correct dimensions on resize. Without this, the overlay keeps
		// drawing at its construction-time width/height and the buffer
		// fragments.
		if m.content != nil {
			m.content.SetSize(m.width, m.contentPageHeight())
		}
		// -r boot: open the history picker on the first sized frame.
		var historyCmd tea.Cmd
		if m.openHistoryOnStart && m.width > 0 {
			m.openHistoryOnStart = false
			hv, cmd := newHistoryView(m.agent, m.palette, m.styles, m.width, m.height)
			m.content = hv
			historyCmd = cmd
		}
		// -s boot / first run / wizard resume: the wizard page wins when
		// both flags are set — setup is the more urgent state.
		if m.openWizardOnStart && m.width > 0 {
			m.openWizardOnStart = false
			m.content = newWizardPage(m.agent, m.palette, m.styles, m.width, m.height)
		}
		// Force a full alt-screen redraw on resize. Without ClearScreen,
		// rows in the terminal that were occupied at the OLD size but not
		// rewritten at the NEW size show stale content.
		return m, tea.Batch(tea.ClearScreen, historyCmd)

	case tea.MouseWheelMsg:
		if m.contentPageActive() {
			if scroller, ok := m.content.(contentPageScroller); ok {
				switch msg.Mouse().Button {
				case tea.MouseWheelUp:
					scroller.ScrollBy(-promptWheelDelta)
				case tea.MouseWheelDown:
					scroller.ScrollBy(promptWheelDelta)
				}
			}
			return m, nil
		}
		mouse := msg.Mouse()
		if !m.contentPageActive() && m.taskPane.Expanded && m.taskPaneHit(mouse.X, mouse.Y) {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.scrollTaskPaneBy(-promptWheelDelta, 0)
			case tea.MouseWheelDown:
				m.scrollTaskPaneBy(promptWheelDelta, 0)
			case tea.MouseWheelLeft:
				m.scrollTaskPaneBy(0, -promptWheelDelta)
			case tea.MouseWheelRight:
				m.scrollTaskPaneBy(0, promptWheelDelta)
			}
			return m, nil
		}
		if m.pendingConfirm != nil {
			// Confirm pending: the prompt is dormant, but let the wheel scroll
			// the scrollback so the user can review context before answering.
			cmd := m.activeChat().Update(msg)
			return m, cmd
		}
		if m.activeChat().SelectionDragging() {
			return m, nil
		}
		if m.mouseInPrompt(mouse) {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.input.ScrollView(-promptWheelDelta)
			case tea.MouseWheelDown:
				m.input.ScrollView(promptWheelDelta)
			}
			return m, nil
		}
		cmd := m.activeChat().Update(msg)
		return m, cmd

	case tea.MouseClickMsg:
		if m.pendingConfirm != nil {
			// Confirm pending: the input is dormant, but fold-toggle clicks
			// on tool entries stay live (like wheel scrolling) so the user
			// can expand prior tool output to review what the call is about
			// to touch before answering y/n. All other clicks are ignored.
			mouse := msg.Mouse()
			if mouse.Button == tea.MouseLeft && !m.contentPageActive() &&
				m.activeChat().MouseToggleFold(mouse.X, mouse.Y-m.scrollbarTop) {
				cmds := m.dispatchToolFetches()
				m.refreshViewport()
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft && m.mouseInHeaderTitle(mouse.X, mouse.Y) {
			m.beginHeaderSelection(mouse.X)
			return m, nil
		}
		m.headerSelection = textSelection{}
		if m.contentPageActive() {
			if mouse.Button != tea.MouseLeft {
				return m, nil
			}
			if m.configSurface != nil && mouse.Y == m.configStripTop() {
				if tab := configTabAtX(mouse.X); tab >= 0 {
					return m, m.switchConfigTab(tab)
				}
				return m, nil
			}
			if scroller, state, ok := m.contentScrollbarAt(mouse); ok {
				m.contentScrollbarDragging = true
				scroller.ScrollTo(scrollOffsetFromClick(mouse.Y, m.contentTop(), state.Height, state.Total))
				return m, nil
			}
			m.contentScrollbarDragging = false
			if cv, ok := m.content.(*contextView); ok && mouse.Button == tea.MouseLeft {
				if cv.handleClick(mouse.X, mouse.Y-m.contentTop()) {
					return m, nil
				}
			}
			if hv, ok := m.content.(*historyView); ok && mouse.Button == tea.MouseLeft {
				if cmd, handled := hv.handleClick(mouse.X, mouse.Y-m.contentTop()); handled {
					return m, cmd
				}
			}
			return m, nil
		}
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
		if m.taskPaneToggleHit(mouse.X, mouse.Y) {
			m.toggleTaskPane()
			return m, nil
		}
		if axis, state, pos, ok := m.taskPaneScrollbarAt(mouse.X, mouse.Y); ok {
			m.taskPane.Dragging = true
			m.taskPane.Drag = axis
			m.taskPaneScrollTo(axis, scrollbarOffsetFromPosition(pos, state))
			return m, nil
		}
		if m.taskPane.Expanded && m.taskPaneHit(mouse.X, mouse.Y) {
			// The expanded pane body belongs to task interactions; do not let a
			// click through to scrollback selection behind the right-side drawer.
			return m, nil
		}
		if m.hasSubAgentTabs() && mouse.Y == m.scrollbarTop-2 {
			if id, isClose, ok := tabStripHitAtX(m.chatTabItems(), mouse.X); ok {
				if isClose {
					m.closeSubAgentTab(id)
				} else {
					m.switchChatTab(id)
				}
				m.refreshViewport()
				return m, nil
			}
		}
		if m.mouseInPrompt(mouse) {
			m.activeChat().ClearSelection()
			m.input.MouseDown(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		if url, ok := m.activeChat().LinkAt(mouse.X, mouse.Y-m.scrollbarTop); ok {
			return m, openBrowserCmd(url)
		}
		if subID, ok := m.activeChat().SubAgentTabAt(mouse.X, mouse.Y-m.scrollbarTop); ok {
			cmd := m.reopenSubAgentTabCmd(subID)
			m.refreshViewport()
			return m, cmd
		}
		// Tool-entry fold click: if the click landed on a tool entry line,
		// focus it and toggle its Folded state — mirrors keyboard
		// ToggleFocusedFold. Short-circuits before selection so a click on
		// a tool entry never starts a text-selection drag.
		if m.activeChat().MouseToggleFold(mouse.X, mouse.Y-m.scrollbarTop) {
			cmds := m.dispatchToolFetches()
			m.refreshViewport()
			return m, tea.Batch(cmds...)
		}
		// Translate screen coords to viewport-local and forward to chatView.
		m.activeChat().MouseDown(mouse.X, mouse.Y-m.scrollbarTop)
		return m, nil

	case tea.MouseMotionMsg:
		mouse := msg.Mouse()
		if m.headerSelection.Dragging {
			m.updateHeaderSelection(mouse.X)
			return m, nil
		}
		if m.contentPageActive() {
			if m.contentScrollbarDragging {
				if scroller, state, ok := m.activeContentScroller(); ok {
					scroller.ScrollTo(scrollOffsetFromClick(mouse.Y, m.contentTop(), state.Height, state.Total))
				}
			}
			return m, nil
		}
		if m.pendingConfirm != nil {
			m.activeChat().StopScrollbarDrag()
			m.activeChat().ClearSelectionDrag()
			m.input.CancelDrag()
			return m, nil
		}
		if m.taskPane.Dragging {
			axis, state, pos, ok := m.taskPaneScrollbarAt(mouse.X, mouse.Y)
			if !ok || axis != m.taskPane.Drag {
				g, geomOK := m.taskPaneGeometry()
				if !geomOK {
					return m, nil
				}
				switch scrollbarOrientation(m.taskPane.Drag) {
				case scrollbarVertical:
					state = g.verticalState(m.taskPane.ScrollY)
					pos = mouse.Y - g.bodyTop()
				case scrollbarHorizontal:
					state = g.horizontalState(m.taskPane.ScrollX)
					pos = mouse.X - g.contentLeft()
				}
			}
			m.taskPaneScrollTo(m.taskPane.Drag, scrollbarOffsetFromPosition(pos, state))
			return m, nil
		}
		if m.input.Dragging() {
			m.input.MouseDrag(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		// MouseDrag checks scrollbarDragging first (priority over text selection)
		// then falls through to the selection extend path.
		cmd := m.activeChat().MouseDrag(mouse.X, mouse.Y-m.scrollbarTop)
		return m, cmd

	case tea.MouseReleaseMsg:
		mouse := msg.Mouse()
		if m.headerSelection.Dragging {
			m.updateHeaderSelection(mouse.X)
			m.headerSelection.Dragging = false
			if m.headerSelection.empty() {
				m.headerSelection = textSelection{}
				return m, nil
			}
			if text := m.selectedHeaderText(); text != "" {
				m.selectionNotice = "copied selection"
				return m, selectionClipboardCmd(text)
			}
			return m, nil
		}
		if m.contentPageActive() {
			m.contentScrollbarDragging = false
			return m, nil
		}
		if m.taskPane.Dragging {
			m.clearTaskPaneDrag()
			return m, nil
		}
		if m.input.Dragging() {
			// Releasing a prompt drag auto-copies the selection, mirroring the
			// scrollback viewport. This is the only copy path that survives
			// terminals which reserve Cmd+C for their own copy and never
			// deliver it to the app.
			if text := m.input.MouseUp(mouse.X, mouse.Y-m.promptTop()); text != "" {
				m.selectionNotice = "copied selection"
				return m, selectionClipboardCmd(text)
			}
			return m, nil
		}
		cmd, copied := m.activeChat().MouseUp(mouse.X, mouse.Y-m.scrollbarTop)
		if copied {
			m.selectionNotice = "copied selection"
		}
		return m, cmd

	case tea.KeyboardEnhancementsMsg:
		return m, nil

	case tea.PasteMsg:
		if m.contentPageActive() {
			// A content page owns the screen. Most pages don't take pasted
			// text, but ones with a text field (e.g. the MCP add-server form)
			// do — offer the paste to the active page and only drop it if the
			// page declines. Without this, paste is silently swallowed here and
			// never reaches the form.
			if p, ok := m.content.(pasteConsumingPage); ok {
				if p.handlePaste(msg.Content) {
					return m, nil
				}
			}
			return m, nil
		}
		if m.pendingConfirm != nil {
			// Confirm pending: the prompt input is disabled. Pasted text must not
			// enter the prompt buffer or shadow y/n/c/d hotkeys.
			return m, nil
		}
		m = m.preparePromptInput()
		// A drag-dropped image arrives as a paste of its path; a copied image
		// may arrive as an empty/whitespace paste (bytes live on the clipboard).
		if strings.TrimSpace(msg.Content) == "" {
			if (&m).handleClipboardImage() {
				m.relayout()
				return m, nil
			}
		} else if (&m).handleImagePaste(msg.Content) {
			m.relayout()
			return m, nil
		}
		var cmd tea.Cmd
		prevVal := m.input.Value()
		prevLayout := m.promptLayoutSignature()
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != prevVal && m.promptLayoutSignature() != prevLayout {
			m.relayout()
		}
		return m, cmd

	case tea.KeyPressMsg:
		// Pending confirm gates keys — until the user resolves it, the
		// input and any in-flight slash commands stay dormant. Scrollback
		// navigation stays live, though, so the user can page back to review
		// what the tool is about to touch before answering y/n.
		if m.pendingConfirm != nil {
			next, cmd := m.handlePendingConfirmKey(msg)
			return next, cmd
		}
		keyStr := msg.String()
		// Compose mode ([c] "chat about this"): the confirm was dismissed and the
		// user types a redirect. Enter sends it (server records it as the tool
		// result and continues the turn); esc/ctrl+c cancels with a plain deny. Any
		// other key falls through to the normal input path so the text is typed.
		if m.composeToolUseID != "" {
			switch keyStr {
			case "enter":
				text := strings.TrimSpace(m.input.Value())
				if text == "" {
					return m, nil
				}
				id, convID, ag := m.composeToolUseID, m.convID, m.agent
				m.composeToolUseID = ""
				m.input.SetValue("")
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("↳ redirected: " + text)})
				m.refreshViewport()
				if ag != nil {
					go func() { _ = ag.DenyToolCallWithMessage(context.Background(), convID, id, text) }()
				}
				return m, nil
			case "esc", "ctrl+c":
				id, convID, ag := m.composeToolUseID, m.convID, m.agent
				m.composeToolUseID = ""
				m.input.SetValue("")
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("canceled.")})
				m.refreshViewport()
				if ag != nil {
					go func() { _ = ag.DenyToolCall(context.Background(), convID, id) }()
				}
				return m, nil
			}
		}
		if keyStr == "ctrl+c" {
			next, cmd := m.handleCtrlCKey(msg)
			return next, cmd
		}
		if keyStr == "ctrl+t" {
			m.toggleTaskPane()
			return m, nil
		}
		if m.handleTaskPaneKey(keyStr) {
			return m, nil
		}
		if m.ctrlCArmed {
			m.ctrlCArmed = false
			m.input.Placeholder = defaultInputPlaceholder
		}
		// The local-runtime install modal takes precedence over every
		// other surface — it's a floating overlay, so its keys must be
		// consumed before content pages or the input see them.
		if m.openRuntimeModal != nil {
			next, cmd := m.handleOpenRuntimeModalKey(msg)
			return next, cmd
		}
		if m.chatgptLoginModal != nil {
			return m.handleChatGPTLoginModalKey(msg)
		}
		if m.claudeLoginModal != nil {
			return m.handleClaudeLoginModalKey(msg)
		}
		// F1 opens the install modal when the chip is showing. Global so
		// it works whether the user is on chat, settings, or any content
		// page. If no unresolved local-runtime setup is queued, F1 is a
		// no-op (falls through).
		if keyStr == "f1" && m.openRuntimeStatus != nil && !m.openRuntimeStatus.Ok {
			m.openRuntimeModal = newOpenRuntimeInstallModal(*m.openRuntimeStatus)
			if modalOpensScanning(*m.openRuntimeStatus) {
				return m, fetchModalGGUFsCmd(m.agent)
			}
			return m, nil
		}
		// Active content pages own the middle region, but global keys stay
		// above this branch.
		if m.content != nil {
			if m.configSurface != nil {
				if next, cmd, handled := m.handleConfigSurfaceKey(msg); handled {
					return next, cmd
				}
			}
			if cv, ok := m.content.(*contextView); ok {
				return m.handleContextViewKey(cv, msg)
			}
			pageID := m.content.ID()
			cmd, closed := m.content.Update(msg)
			if closed {
				m.content = nil
				m.contentScrollbarDragging = false
				if pageID == contentPageSettings {
					// Refresh the header bar's model names — the editor may
					// have just changed local-model / cloud-model / cloud-base-url.
					return m, fetchConfigCmd(m.agent)
				}
				if pageID == contentPageWizard {
					// OpenWizardOnStart suppressed the splash so the wordmark
					// didn't sit above the setup steps. Reveal it on finish and
					// replay the shimmer, so completing setup lands on the CERCANO
					// welcome chrome instead of an empty body. Also refresh the
					// config so the header reflects the models the wizard just
					// wrote (mirrors the settings-close refresh above).
					m.splashShown = true
					m.splash = banner.NewAnimModel(m.palette, m.splash.Meta)
					m.bannerTickActive = true
					// Revealing the splash adds banner rows; resize the viewport
					// so the prompt + status footer still fit (every other splash
					// toggle relayouts too — omitting it pushes them off-screen).
					m.relayout()
					return m, tea.Batch(fetchConfigCmd(m.agent), m.splash.Init())
				}
			}
			return m, cmd
		}
		if isRuntimeDashboardKey(msg) {
			return m, m.openConfigSurface(configTabModels)
		}
		// Chat tab strip keyboard focus + nav: shift+tab lifts focus from the
		// prompt to the strip; while focused, tab/arrows/digits switch tabs and x
		// closes. Consumed keys never reach the prompt.
		if m.handleChatTabStripKey(keyStr) {
			m.refreshViewport()
			return m, nil
		}
		// Esc cancels an in-flight prompt execution. If there's a queued
		// follow-up (user typed while waiting), it stays queued through the
		// cancel and immediately becomes the next turn — canceling stops
		// the current work but preserves the user's already-typed next
		// intent. Double-esc still cancels the follow-up too (each esc only
		// touches one in-flight turn at a time).
		if m.streaming && key.Matches(msg, keys.Back) {
			m.cancelCurrentStream()
			if next, ok := m.mainChat().DrainNext(); ok {
				m.relayout()
				nm, cmd := m.submit(next.text, next.images)
				return nm, cmd
			}
			return m, nil
		}
		if m.activeChat().SelectionActive() {
			cmd, handled, copied := m.activeChat().HandleSelectionKey(msg)
			if copied {
				m.selectionNotice = "copied selection"
			}
			if handled {
				return m, cmd
			}
		}
		// Tool-entry navigation mode: focus is on a scrollback tool entry
		// rather than the input box. Up/down cycle (clamped at edges),
		// enter/tab toggle Folded, esc returns to input. Any other key
		// returns to input and is then handled by the normal input path.
		if m.activeChat().InToolNav() {
			switch {
			case key.Matches(msg, keys.NavUp):
				m.activeChat().NavPrev()
				m.refreshViewport()
				return m, nil
			case key.Matches(msg, keys.NavDown):
				m.activeChat().NavNext()
				m.refreshViewport()
				return m, nil
			case key.Matches(msg, keys.ToggleTool):
				m.activeChat().ToggleFocusedFold()
				cmds := m.dispatchToolFetches()
				m.refreshViewport()
				return m, tea.Batch(cmds...)
			case key.Matches(msg, keys.Back):
				m.activeChat().ExitToolNav()
				m.refreshViewport()
				return m, nil
			}
			// Any other key (typing) drops nav mode and falls through to
			// normal input handling so the character actually lands in the
			// input box.
			m = m.preparePromptInput()
			// fall through
		}
		// ctrl+v: if the clipboard holds an image, attach it; otherwise fall
		// through so the terminal's native paste mechanism handles it.
		if keyStr == "ctrl+v" && !m.contentPageActive() && m.pendingConfirm == nil {
			m = m.preparePromptInput()
			if (&m).handleClipboardImage() {
				m.relayout()
				return m, nil
			}
			// no image on clipboard → fall through to normal handling
		}
		// Esc on empty input enters tool-entry navigation mode, focusing the
		// most-recent tool entry. No-op when scrollback has no tool entries.
		if key.Matches(msg, keys.Back) && m.input.Value() == "" {
			if m.activeChat().EnterToolNav() {
				m.refreshViewport()
				return m, nil
			}
		}
		// Tab accepts the ghost-text "next prompt" suggestion when the input
		// is empty. Puts the suggestion into the input as editable text so
		// the user can tweak before submitting. Slash-command tab-completion
		// only applies when the input is non-empty and starts with "/", so
		// the two Tab modes don't collide.
		if keyStr == "tab" && m.input.Value() == "" && m.nextPromptSuggestion != "" {
			accepted := m.nextPromptSuggestion
			m.nextPromptSuggestion = ""
			m.input.Suggestion = ""
			m.input.SetValue(accepted)
			m.input.CursorEnd()
			return m, nil
		}
		// Tab completion for slash commands.
		if keyStr == "tab" {
			val := m.input.Value()
			if strings.HasPrefix(val, "/") {
				matches := m.registry.PrefixMatches(val)
				if len(matches) == 1 {
					// Complete fully + space, so the user can type args.
					m.input.SetValue("/" + matches[0] + " ")
					m.input.CursorEnd()
					return m, nil
				}
				if len(matches) > 1 {
					completed := slash.CommonPrefix(matches)
					if "/"+completed != val {
						m.input.SetValue("/" + completed)
						m.input.CursorEnd()
					}
					return m, nil
				}
			}
			// Not a slash command — fall through to default key routing.
		}
		if key.Matches(msg, keys.ScrollKeys) {
			// Route navigation keys to the scrollback viewport. Keeps the
			// textinput's normal arrow / line-edit semantics intact.
			cmd := m.activeChat().Update(msg)
			return m, cmd
		}
		unmodifiedArrow := msg.Key().Mod == 0
		// Shift+Enter composes a hard newline instead of submitting. Detect it
		// structurally (Enter code + Shift modifier) rather than by the chord
		// string: on terminals that report associated text (we enable Kitty
		// ReportAssociatedText), the event carries Text "\n" and msg.String()
		// returns "\n" instead of "shift+enter", so a string-only match misses.
		if ek := msg.Key(); ek.Code == tea.KeyEnter && ek.Mod.Contains(tea.ModShift) {
			m.input.InsertString("\n")
			m.relayout()
			return m, nil
		}
		if msg.Key().Code == tea.KeyEnter {
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				if m.streaming {
					if next, ok := m.mainChat().DrainNext(); ok {
						m.cancelCurrentStreamSilently()
						m.relayout()
						return m.submit(next.text, next.images)
					}
				}
				return m, nil
			}
			images := promptImagesToInline(m.input.Attachments())
			m.input.SetValue("")
			// The splash chrome hands off to a persistent scrollback banner:
			// dismissing it on first submit moves the wordmark to entry zero
			// instead of dropping it entirely.
			if m.splashShown {
				m.mainChat().PrependBanner(m.splash.Meta, m.splash.Started())
			}
			m.splashShown = false
			// Slash commands are local navigation / UI actions, never sent to
			// the model — bypass the mid-stream queue so /c, /m, /rename etc.
			// take effect immediately even while a turn is in flight.
			isSlash := strings.HasPrefix(text, "/")
			// Submitting a non-slash mid-stream stages the message first. Pressing
			// Enter again on the now-empty prompt promotes the staged message into
			// a steering turn by canceling the current stream and submitting it.
			if m.streaming && !isSlash {
				m.mainChat().Enqueue(text, images)
				m.relayout()
				return m, nil
			}
			// Reset the input back to one line (and reclaim any splash rows).
			m.relayout()
			return m.submit(text, images)
		}
		switch keyStr {
		case "up":
			// On an empty prompt, ↑ first unstages the most-recently-queued
			// message for editing (takes priority over history).
			if unmodifiedArrow && m.input.Value() == "" && m.input.CursorOnFirstRow() && m.unstageLastQueued() {
				return m, nil
			}
			// On the first visual row, ↑ recalls the previous submitted input
			// (shell style); otherwise it falls through to move the cursor up a
			// row. This counts soft-wrapped rows, not just hard newlines, so ↑
			// inside a long wrapped line moves the cursor rather than clobbering
			// the draft with history.
			if unmodifiedArrow && m.input.CursorOnFirstRow() && m.recallHistoryPrev() {
				return m, nil
			}
		case "down":
			// On the last visual row, ↓ steps forward through history; otherwise
			// it falls through to move the cursor down a row.
			if unmodifiedArrow && m.input.CursorOnLastRow() && m.recallHistoryNext() {
				return m, nil
			}
		}
		var cmd tea.Cmd
		prevVal := m.input.Value()
		prevLayout := m.promptLayoutSignature()
		m.input, cmd = m.input.Update(msg)
		// Recompute layout only if the input changed screen shape. Ordinary
		// same-row typing repaints the prompt from m.input.View() and must not
		// rebuild a large transcript on every keypress.
		if m.input.Value() != prevVal && m.promptLayoutSignature() != prevLayout {
			m.relayout()
		}
		return m, cmd

	case chatStatusMsg, chatAssistantMsg, chatDoneMsg, chatErrorMsg, chatConfirmMsg:
		return m.routeChatMsg(msg)

	case contextRefreshTickMsg:
		// /c auto-refresh. Stops ticking once /c is closed. Skips the reload mid-
		// edit (active proposal or a busy pane) to avoid disrupting the
		// interaction, but keeps the tick alive.
		cv, ok := m.content.(*contextView)
		if !ok {
			return m, nil
		}
		if cv.showingProposal || cv.busy() {
			return m, contextRefreshTick()
		}
		return m, tea.Batch(loadContextSnapshotCmd(cv.agent, cv.convID), contextRefreshTick())

	case contextSnapshotMsg:
		if cv, ok := m.content.(*contextView); ok {
			cv.snapshot = msg.snap
		}
		return m, nil

	case exportDoneMsg:
		if cv, ok := m.content.(*contextView); ok {
			if msg.err != nil {
				cv.notice = "export failed: " + msg.err.Error()
			} else {
				cv.notice = "exported full context → " + msg.path
			}
		}
		return m, nil

	case historyRowsLoadedMsg:
		if hv, ok := m.content.(*historyView); ok {
			hv.applyRowsLoaded(msg.rows, msg.err)
		}
		return m, nil

	case historyTurnsLoadedMsg:
		if hv, ok := m.content.(*historyView); ok {
			hv.applyTurns(msg.id, msg.turns)
		}
		return m, nil

	case runtimeDashboardActionMsg:
		if dashboard, ok := m.content.(*runtimeDashboard); ok {
			return m, dashboard.applyActionMsg(msg)
		}
		return m, nil

	case runtimeDashboardRefreshMsg:
		if dashboard, ok := m.content.(*runtimeDashboard); ok {
			return m, dashboard.refreshSnapshot()
		}
		return m, nil

	case runtimeDashboardSnapshotMsg:
		if dashboard, ok := m.content.(*runtimeDashboard); ok {
			return m, dashboard.applySnapshot(msg)
		}
		return m, nil

	case runtimeCatalogRefreshDoneMsg:
		if dashboard, ok := m.content.(*runtimeDashboard); ok {
			return m, dashboard.applyCatalogRefresh(msg)
		}
		return m, nil

	case mcpDashboardRefreshMsg:
		if dashboard, ok := m.content.(*mcpDashboard); ok {
			return m, dashboard.refreshSnapshot()
		}
		return m, nil

	case mcpDashboardSnapshotMsg:
		if dashboard, ok := m.content.(*mcpDashboard); ok {
			return m, dashboard.applySnapshot(msg)
		}
		return m, nil

	case mcpDashboardActionMsg:
		if dashboard, ok := m.content.(*mcpDashboard); ok {
			return m, dashboard.applyActionMsg(msg)
		}
		return m, nil

	case mcpDashboardClearActionMsg:
		if dashboard, ok := m.content.(*mcpDashboard); ok {
			dashboard.clearActionMessage(msg.gen)
		}
		return m, nil

	case runtimeEstimateMsg:
		if dashboard, ok := m.content.(*runtimeDashboard); ok {
			return m, dashboard.applyEstimate(msg)
		}
		return m, nil

	case settingsCommitDoneMsg:
		// The commit outcome still applies server-side even if the user has
		// left the settings page — ConfigChanged events keep the chips fresh —
		// so a missing page just drops the status repaint, never the change.
		if sp, ok := m.content.(*settingsPage); ok {
			return m, sp.applyCommitDone(msg)
		}
		return m, nil

	case settingsSpinnerTickMsg:
		if sp, ok := m.content.(*settingsPage); ok {
			return m, sp.applySpinnerTick()
		}
		return m, nil

	case chatStreamMsg:
		// Ghost events from a dead turn (canceled or superseded): never apply
		// them, but keep draining the channel so its buffered leftovers flush
		// through to the close.
		if msg.gen != m.turnGen {
			return m, msg.next
		}
		// Ignore late events from a stream we already canceled.
		if !m.streaming {
			return m, nil
		}
		// Route by event class: telemetry → footer, transcript → chatView.Apply,
		// permission → host confirm gate. turnActivity/turnTokOut are derived
		// from the event type here.
		switch ev := msg.ev.(type) {
		case chatStatusMsg:
			// RouteSelected telemetry: engine badge for the footer.
			m.turnModel = ev.model
			m.turnCloud = ev.cloud
		case reauthRequiredMsg:
			m.turnActivity = "auth"
			m.mainChat().Apply(chatProgressMsg{note: ev.note})
			if m.pendingConfirm == nil {
				m.pendingConfirm = reauthConfirm(ev)
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmRequest(m.pendingConfirm)})
			}
			m.chatDirty = true
			return m, tea.Batch(msg.next, m.ensureAnimTick())
		case chatProgressMsg:
			// Coalesced: mark the transcript dirty and let the next anim
			// tick repaint. Rebuilding per event made rebuild rate track
			// the stream's event rate — the input queue starved behind it.
			m.turnActivity = "routing"
			m.mainChat().Apply(ev)
			m.chatDirty = true
			return m, tea.Batch(msg.next, m.ensureAnimTick())
		case chatAssistantDeltaMsg:
			// Coalesced, same as chatProgressMsg: token deltas arrive far
			// faster than 20fps; the tick flushes them in batches.
			m.turnActivity = "writing"
			m.turnTokOut++ // one delta ≈ one token (approximate live count)
			m.mainChat().Apply(ev)
			m.chatDirty = true
			return m, tea.Batch(msg.next, m.ensureAnimTick())
		case toolEntryStartMsg:
			m.turnToolStarted++
			m.turnActivity = toolProgressActivity(ev.name, m.turnToolStarted, m.turnToolDone)
			m.mainChat().Apply(ev)
			// Re-arm the spinner loop if it stopped (the placeholder loop
			// dies once tokens begin streaming), and fall through to the
			// shared repaint so the new tool row appears immediately.
			m.refreshViewport()
			return m, tea.Batch(msg.next, m.ensureAnimTick())
		case toolEntryExecCompleteMsg:
			if m.turnToolDone < m.turnToolStarted {
				m.turnToolDone++
			}
			if m.turnToolStarted > 0 {
				m.turnActivity = fmt.Sprintf("completed %d/%d tools", m.turnToolDone, m.turnToolStarted)
			}
			m.mainChat().Apply(ev)
		case subAgentEventMsg:
			m.applySubAgentEvent(ev)
			m.refreshViewport()
			return m, tea.Batch(msg.next, m.ensureAnimTick())
		case taskChangeMsg:
			m.applyTaskChange(ev.kind, ev.task)
			m.refreshViewport()
			return m, msg.next
		case chatDoneMsg:
			m.applyTurnTelemetry(ev) // footer fields
			m.mainChat().Apply(ev)   // transcript finalize + notice
			m.turnToolStarted = 0
			m.turnToolDone = 0
			m.finishStaleSubAgentTabs("sub-agent stopped without a terminal event")
			// Turn done: retire finished sub-agent tabs now (ephemeral tabs)
			// instead of waiting for the next turn to start. The sweep spares
			// main, the active tab, and any still-running tab; refresh the
			// viewport since dropping the strip + its rule frees two rows.
			if m.hasSubAgentTabs() {
				m.cleanupFinishedSubAgentTabs()
				m.refreshViewport()
			}
		case permissionRequiredMsg:
			tc := &pendingToolCall{
				ToolUseID:   ev.id,
				Name:        ev.name,
				Args:        ev.argsJSON,
				Permission:  ev.tier,
				Destructive: ev.destructive,
			}
			m.pendingConfirm = toolConfirm(tc)
			m.pendingConfirm.retryPrompt = m.lastSubmittedPrompt
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmPrompt(tc)})
		case rolloverOfferedMsg:
			m.pendingConfirm = m.rolloverConfirm(ev)
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.renderRolloverPrompt(ev)})
		case chatErrorMsg:
			m.turnToolStarted = 0
			m.turnToolDone = 0
			m.finishStaleSubAgentTabs("sub-agent stopped after parent stream error")
			m.mainChat().Apply(ev)
			if m.hasSubAgentTabs() {
				m.cleanupFinishedSubAgentTabs()
			}
		default:
			// Remaining transcript events (tool stop/exec-start/exec-complete,
			// error) carry no turn-telemetry side effect.
			m.mainChat().Apply(ev)
		}
		m.refreshViewport()
		return m, msg.next

	case contextRegenProgressMsg:
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("context-regen: " + msg.line)})
		m.refreshViewport()
		// Also poll the meter: the server reports Compacting=true while the
		// rebuild holds the compaction claim, and that flag (via ctxUsageMsg)
		// is what starts the meter's compacting animation and its re-poll
		// loop. Without this fetch the meter would sit still until done.
		return m, tea.Batch(msg.next, fetchContextUsage(m.agent, m.convID))

	case contextRegenDoneMsg:
		if msg.err != "" || !msg.ok {
			text := "context-regen failed: " + msg.err
			// A severed stream does not prove the agent process died; it means the
			// foreground regen stream was interrupted (EOF/GOAWAY/unavailable/etc.).
			// Every completed pass is persisted, and /compact now uses the background
			// scheduler instead of a long foreground stream.
			if isTransportLoss(msg.err) {
				text = "context-regen stream interrupted: " + msg.err + ". " +
					"Completed passes are saved. Use /compact to continue in the background, " +
					"or re-run /context-regen only if you want a full foreground rebuild from scratch."
			}
			m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: text})
			m.refreshViewport()
			return m, fetchContextUsage(m.agent, m.convID)
		}
		doneLine := msg.line
		if doneLine == "" {
			doneLine = fmt.Sprintf("context rebuilt: ~%d → ~%d tokens", msg.pre, msg.post)
		}
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: doneLine})
		m.refreshViewport()
		// The terminal frame already carries the numbers, but the meter's
		// authoritative source is GetContextUsage — refetch so every derived
		// field (max, compacting flag) settles too. For /compact, the server
		// starts the background pass immediately, so the poll should see the real
		// compacting claim rather than a client-side fake.
		if msg.incremental && !m.ctxPolling {
			m.ctxPolling = true
			return m, tea.Batch(fetchContextUsage(m.agent, m.convID), ctxUsageTick())
		}
		return m, fetchContextUsage(m.agent, m.convID)

	case elideContextDoneMsg:
		if msg.err != "" {
			m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "elide-context failed: " + msg.err})
			m.refreshViewport()
			return m, nil
		}
		line := fmt.Sprintf("context elided: ~%d → ~%d tokens (%d tool results stubbed; in-memory — resets on agent restart)",
			msg.pre, msg.post, msg.stubbed)
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: line})
		m.refreshViewport()
		return m, fetchContextUsage(m.agent, m.convID)

	case toolCallFetchedMsg:
		// Lazy tool-body fetch returned — fill the expanded entry (or just
		// clear the spinner if the fetch failed / found nothing) and repaint.
		if t := m.mainChat().findToolEntry(msg.id); t != nil {
			t.Loading = false
			if msg.err == nil && msg.detail != nil && msg.detail.Found {
				t.FullArgs = msg.detail.ArgsJSON
				t.FullResult = msg.detail.Result
				t.StartLine = msg.detail.StartLine
			}
			m.mainChat().markTranscriptDirty()
			m.refreshViewport()
		}
		return m, nil

	case ctxUsageMsg:
		// Authoritative context-window meter from the agent; overrides
		// our locally-summed cumIn approximation.
		if msg.Used > 0 {
			m.cumIn = msg.Used
		}
		if msg.Max > 0 {
			m.modelMaxTokens = msg.Max
		}
		m.ctxRaw = msg.Raw
		m.ctxMessageTokens = msg.MessageTokens
		m.ctxSystemTokens = msg.SystemTokens
		m.ctxToolSchemaTokens = msg.ToolSchemaTokens
		m.ctxOutputReserveTokens = msg.OutputReserveTokens
		m.ctxEstimatedRequest = msg.EstimatedRequestTokens
		m.ctxWindowKnown = msg.ContextWindowKnown
		m.compacting = msg.Compacting
		// Kick the per-frame animation loop whenever compacting is reported
		// and no tick is in flight — not just on the false→true edge. The
		// edge-trigger missed real cases (compacting already true at session
		// start, a dropped kick, gaps between scheduled passes) and left the
		// meter frozen; this restarts the loop on every poll at worst, and
		// the animTickActive guard keeps the tick rate single.
		var cmd tea.Cmd
		if m.compacting && !m.animTickActive {
			m.animTickActive = true
			cmd = progressAnimTick()
		}
		return m, cmd

	case nextPromptSuggestionMsg:
		// Cache in Model and sync to the prompt input so its View renders
		// the ghost text. Empty suggestion clears any prior ghost.
		m.nextPromptSuggestion = msg.suggestion
		m.input.Suggestion = msg.suggestion
		return m, nil

	case recapLoadedMsg:
		// The recap line(s) claim rows below the viewport, so relayout() must
		// re-run whenever their count changes — otherwise the viewport stays
		// sized for the old chrome height and the extra row pushes the status
		// bar off-screen until the next keystroke forces a relayout. The recap
		// is a *living* summary: its text updates mid-session and can grow from
		// one wrapped row to two (or shrink), which changes recapH without any
		// presence toggle. Compare the rendered line count, not just presence.
		prevLines := len(m.recapLines())
		m.recap = msg.recap
		if len(m.recapLines()) != prevLines {
			m.relayout()
		}
		return m, nil

	case configLoadedMsg:
		if msg.OpenModel != "" {
			m.openModel = msg.OpenModel
			// Keep lastModel initialized for legacy telemetry/tests until a real
			// turn completes, but do not let it drive the o: header chip.
			if m.lastModel == "" {
				m.lastModel = msg.OpenModel
			}
		}
		// currentLocalRuntime is used by the install modal's OfferSwitch
		// state to decide whether to prompt "switch runtime after
		// install?" — empty is a legitimate value (defaults to ollama),
		// so we always take the fresh value from the config load.
		m.currentOpenRuntime = msg.OpenRuntime
		// Keep the c: header chip tied to concrete cloud identity, not only to
		// transient CloudState. During startup/rebuilds the server can briefly report
		// a non-ok cloud state while still returning the configured active profile and
		// model; clearing the chip in that window leaves the title bar missing c: even
		// though cloud routing is configured. If both identity fields are empty, clear
		// as before.
		if msg.ActiveCloudProfile != "" || msg.CloudModel != "" {
			m.cloudModel = msg.CloudModel
			m.activeCloudProfile = msg.ActiveCloudProfile
		} else if !msg.CloudConfigured {
			m.cloudModel = ""
			m.activeCloudProfile = ""
		}
		// Reflect the active primary profile in the splash / scrollback banner
		// (the scrollback banner is copied from m.splash.Meta at handoff, which
		// happens on first user input — well after this config load lands).
		if pm := primaryModelName(msg.OpenModel, msg.CloudModel, msg.LocusMode, msg.CloudConfigured); pm != "" {
			m.splash.Meta.Model = pm
		}
		return m, nil

	case permissionModeMsg:
		m.permissionMode = msg.Mode
		return m, nil

	case settingsColorMsg:
		m.promptBorderColor = m.resolvePromptColor(msg.token)
		m.promptColorToken = msg.token
		m.refreshViewport()
		return m, nil

	case settingsThemeMsg:
		m.applyTheme(msg.working)
		if msg.persistName != "" {
			_ = uiconfig.SaveActiveTheme(msg.persistName)
		}
		return m, nil

	case permissionModeChangedMsg:
		// Pushed by the agent (another client's /strict, or a hand-edit to
		// permissions.yaml). Update the chip and re-arm the drain loop.
		if msg.mode != "" {
			m.permissionMode = msg.mode
		}
		return m, msg.next

	case sessionProfileChangedMsg:
		// Pushed by the agent whenever a conversation's capability profile flips
		// (this client's /plan, an approved suggest_plan, plan_exit, etc.). The
		// active profile is per-conversation, so only update the footer chip when
		// the event is for THIS conversation; always re-arm the drain loop.
		if msg.convID == m.convID {
			m.sessionProfile = normalizeProfile(msg.profile)
		}
		return m, msg.next

	case sessionProfileFetchedMsg:
		// Startup/resume seed for the footer chip. Apply only if it's still the
		// active conversation (the user may have switched during the fetch).
		if msg.convID == m.convID {
			m.sessionProfile = normalizeProfile(msg.profile)
		}
		return m, nil

	case configChangedMsg:
		// Pushed by the agent after settings/profile changes. Apply the fields
		// that directly drive header chips, then ask for a fresh config snapshot so
		// derived state such as CloudState and the splash primary model stays in
		// sync with the server's provider rebuild.
		switch msg.field {
		case "cloud_model":
			m.cloudModel = msg.value
		case "active_cloud_profile":
			m.activeCloudProfile = msg.value
		case "local_model", "open_model":
			if msg.value != "" {
				m.lastModel = msg.value
			}
		case "local_runtime", "open_runtime":
			m.currentOpenRuntime = msg.value
		}
		return m, tea.Batch(msg.next, fetchConfigCmd(m.agent))

	case runtimeInstallStartedMsg:
		if m.openRuntimeModal == nil {
			// User closed the modal between Enter and stream open — cancel
			// the pending RPC and drop everything.
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.openRuntimeModal.setFailed(msg.err.Error())
			return m, nil
		}
		m.openRuntimeModal.cancel = msg.cancel
		return m, msg.next

	case runtimeInstallProgressMsg:
		if m.openRuntimeModal == nil {
			return m, nil // modal closed; discard remaining frames
		}
		m.openRuntimeModal.appendLog(msg.line)
		return m, msg.next

	case runtimeInstallDoneMsg:
		if m.openRuntimeModal == nil {
			return m, nil
		}
		m.openRuntimeModal.cancel = nil
		switch {
		case msg.err != "" && installErrorIsMissingModel(msg.err):
			// Install subprocess itself succeeded; the terminal frame
			// carries the post-install detection failure. The user needs
			// to add a GGUF or set llama_server.default_model — retrying
			// the install won't fix anything, so surface that state
			// distinctly.
			m.openRuntimeModal.setNeedsModel(msg.err)
			m.pendingRuntimeSwitch = ""
		case msg.err != "":
			m.openRuntimeModal.setFailed(msg.err)
			m.pendingRuntimeSwitch = "" // failed install — the queued switch is dropped
		case !msg.ok:
			m.openRuntimeModal.setFailed("install exited with error")
			m.pendingRuntimeSwitch = ""
		default:
			// Success. Three sub-paths:
			//   1. A runtime switch was pre-queued (user came via the
			//      settings dropdown) → dispatch it and flip to Done.
			//   2. No switch queued AND llama_server isn't the current
			//      runtime → the user came via the F1 chip and probably
			//      wants to actually use what they just installed. Ask
			//      explicitly rather than silently deciding either way.
			//   3. No switch queued AND llama_server IS already active
			//      → just flip to Done.
			if m.pendingRuntimeSwitch != "" {
				runtime := m.pendingRuntimeSwitch
				m.pendingRuntimeSwitch = ""
				m.openRuntimeModal.state = runtimeModalDone
				return m, dispatchOpenRuntimeSwitch(m.agent, runtime)
			}
			// Normalize the empty default: server treats empty as ollama.
			active := m.currentOpenRuntime
			if active == "" {
				active = "ollama"
			}
			if active != "llama_server" {
				m.openRuntimeModal.setOfferSwitch("llama_server", active)
			} else {
				m.openRuntimeModal.state = runtimeModalDone
			}
		}
		return m, nil

	case openRuntimeConfirmSwitchMsg:
		if msg.target == "" {
			return m, nil
		}
		if msg.target == msg.active {
			return m, nil
		}
		st := agentclient.OpenRuntimeStatus{Runtime: msg.target, Ok: true}
		m.openRuntimeModal = newOpenRuntimeInstallModal(st)
		m.openRuntimeModal.setOfferSwitch(msg.target, msg.active)
		m.pendingRuntimeSwitch = msg.target
		return m, nil

	case openOpenRuntimeInstallModalMsg:
		// Emitted by the settings page when the user tries to switch to
		// a runtime that isn't ready. Opens the install modal (idle for a
		// missing binary; scanning for a missing/ambiguous model) and
		// remembers the switch to dispatch on success.
		if m.openRuntimeModal == nil {
			m.openRuntimeModal = newOpenRuntimeInstallModal(msg.status)
			m.pendingRuntimeSwitch = msg.pending
			if modalOpensScanning(msg.status) {
				return m, fetchModalGGUFsCmd(m.agent)
			}
			if modalIsBundledModelMissing(msg.status) {
				if strings.TrimSpace(msg.status.DefaultModel) == "" {
					m.openRuntimeModal.setNeedsModel(msg.status.Message)
				} else {
					active := m.currentOpenRuntime
					if active == "" {
						active = "ollama"
					}
					m.openRuntimeModal.setOfferSwitch(msg.pending, active)
				}
			}
			return m, nil
		}
		m.pendingRuntimeSwitch = msg.pending
		return m, nil

	case openClaudeLoginModalMsg:
		if m.claudeLoginModal == nil {
			m.claudeLoginModal = newClaudeLoginModal(msg.profile, msg.model)
			return m, startClaudeLoginCmd(m.agent, msg.profile, msg.model, msg.setActive)
		}
		return m, nil

	case claudeLoginStartedMsg:
		if m.claudeLoginModal == nil {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.claudeLoginModal.setFailed(msg.err.Error())
			return m, nil
		}
		m.claudeLoginModal.cancel = msg.cancel
		return m, drainClaudeLoginCmd(msg.ch)

	case claudeLoginFrameMsg:
		if m.claudeLoginModal == nil {
			return m, nil
		}
		if msg.frame.Err != nil {
			m.claudeLoginModal.setFailed(msg.frame.Err.Error())
			return m, nil
		}
		if msg.frame.Done {
			if msg.frame.Ok {
				// The server owns the canonical profile name; reflect it so the
				// success message is right even when the client sent none.
				if msg.frame.ProfileName != "" {
					m.claudeLoginModal.profile = msg.frame.ProfileName
				}
				m.claudeLoginModal.setDone()
			} else {
				m.claudeLoginModal.setFailed(msg.frame.Error)
			}
			return m, nil
		}
		m.claudeLoginModal.setURL(msg.frame.AuthorizeURL)
		var claudeCmds []tea.Cmd
		if msg.ch != nil {
			claudeCmds = append(claudeCmds, drainClaudeLoginCmd(msg.ch))
		}
		// Auto-open the authorize page once, the moment we have the URL, so the
		// user lands on it without hunting for the link in the modal.
		if !m.claudeLoginModal.browserOpened && m.claudeLoginModal.authorizeURL != "" {
			m.claudeLoginModal.browserOpened = true
			claudeCmds = append(claudeCmds, openBrowserCmd(m.claudeLoginModal.authorizeURL))
		}
		return m, tea.Batch(claudeCmds...)

	case openChatGPTLoginModalMsg:
		if m.chatgptLoginModal == nil {
			m.chatgptLoginModal = newChatGPTLoginModal(msg.profile, msg.model)
			return m, startChatGPTLoginCmd(m.agent, msg.profile, msg.model, msg.setActive)
		}
		return m, nil

	case chatgptLoginStartedMsg:
		if m.chatgptLoginModal == nil {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.chatgptLoginModal.setFailed(msg.err.Error())
			return m, nil
		}
		m.chatgptLoginModal.cancel = msg.cancel
		return m, drainChatGPTLoginCmd(msg.ch)

	case chatgptLoginFrameMsg:
		if m.chatgptLoginModal == nil {
			return m, nil
		}
		if msg.frame.Err != nil {
			m.chatgptLoginModal.setFailed(msg.frame.Err.Error())
			return m, nil
		}
		if msg.frame.Done {
			if msg.frame.Ok {
				// The server owns the canonical profile name; reflect it so the
				// success message is right even when the client sent none.
				if msg.frame.ProfileName != "" {
					m.chatgptLoginModal.profile = msg.frame.ProfileName
				}
				m.chatgptLoginModal.setDone(msg.frame.AccountID)
			} else {
				m.chatgptLoginModal.setFailed(msg.frame.Error)
			}
			return m, nil
		}
		m.chatgptLoginModal.setCode(msg.frame.VerificationURL, msg.frame.UserCode)
		var cmds []tea.Cmd
		if msg.ch != nil {
			cmds = append(cmds, drainChatGPTLoginCmd(msg.ch))
		}
		// Auto-open the verification page once, the moment we have the URL, so
		// the user lands on it without hunting for the link in the modal.
		if !m.chatgptLoginModal.browserOpened && m.chatgptLoginModal.verificationURL != "" {
			m.chatgptLoginModal.browserOpened = true
			cmds = append(cmds, openBrowserCmd(m.chatgptLoginModal.verificationURL))
		}
		return m, tea.Batch(cmds...)

	case modalModelsLoadedMsg:
		// Reply to fetchModalGGUFsCmd. Routes the scanning state: fetch
		// error or zero GGUFs → NeedsModel (browse/download), one or
		// more → the picker. Ignored if the user closed the modal (or an
		// install started) while the fetch was in flight.
		if m.openRuntimeModal == nil || m.openRuntimeModal.state != runtimeModalScanningModels {
			return m, nil
		}
		if msg.err != nil || len(msg.models) == 0 {
			needsMsg := m.openRuntimeModal.status.Message
			if msg.err != nil {
				needsMsg = "model scan failed: " + msg.err.Error()
			}
			m.openRuntimeModal.setNeedsModel(needsMsg)
			return m, nil
		}
		m.openRuntimeModal.setPickModel(m.agent, m.pendingRuntimeSwitch, msg.models, msg.sysRAMBytes)
		return m, nil

	case openRuntimeDashboardMsg:
		// Emitted by the install modal's "Browse models" action — opens the
		// unified config surface on the Models tab.
		return m, m.openConfigSurface(configTabModels)

	case openRuntimeStatusChangedMsg:
		// Pushed on runtime swap or startup — the headless detection
		// outcome for the currently-selected local runtime. Cache for
		// chip rendering and re-arm the drain loop. When ok=true, drop
		// the cache so the chip disappears and any open install-success
		// modal knows to auto-dismiss.
		if msg.status != nil && msg.status.Ok {
			m.openRuntimeStatus = nil
			if m.openRuntimeModal != nil && m.openRuntimeModal.state == runtimeModalRunning {
				m.openRuntimeModal.state = runtimeModalDone
			}
		} else {
			m.openRuntimeStatus = msg.status
		}
		return m, msg.next

	case connStateChangedMsg:
		// SDK reconnect loop reporting a transport-health change. On
		// Reconnecting, if we were mid-stream, treat the current turn
		// as lost and rehydrate the input with the user's prompt so
		// they can re-send with one keystroke.
		prev := m.connState
		m.connState = msg.state
		m.connAttempt = msg.attempt
		m.connFailErrMsg = msg.errMsg
		if msg.state == agentclient.ConnStateReconnecting && prev == agentclient.ConnStateConnected {
			if m.streaming && m.pendingConfirm != nil {
				// A permission gate is the active user input surface. Do not restore
				// the submitted chat prompt into the composer while y/n/d/c is still
				// pending: that makes the prompt look focused after reconnect and can
				// leave stale text in the [c]hat redirect editor. Keep the confirm
				// gate intact so all choices still route through resolveConfirmKey.
				m.streaming = false
				m.mainChat().SetStreaming(false)
				if e := m.mainChat().lastAssistantEntry(); e != nil {
					e.Streaming = false
				}
				body := "⚠ agent disconnected while awaiting your tool decision — reconnecting; answer y/n/d/c once the agent is back."
				if msg.crashSummary != "" {
					body += "\n  cause: " + msg.crashSummary + " (full trace in ~/.config/cercano/crash.log)"
				}
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: body})
				m.refreshViewport()
			} else if m.streaming && m.lastSubmittedPrompt != "" {
				m.input.SetValue(m.lastSubmittedPrompt)
				body := "⚠ agent disconnected mid-turn — prompt restored to the input; press Enter to re-submit once the agent is back."
				if msg.crashSummary != "" {
					body += "\n  cause: " + msg.crashSummary + " (full trace in ~/.config/cercano/crash.log)"
				}
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: body})
				m.streaming = false
				m.mainChat().SetStreaming(false)
				if e := m.mainChat().lastAssistantEntry(); e != nil {
					e.Streaming = false
				}
				m.refreshViewport()
			}
		}
		if msg.state == agentclient.ConnStateConnected && prev == agentclient.ConnStateReconnecting {
			// Recovery. The server on the other end may be a freshly
			// spawned replacement, so every startup-fetched snapshot —
			// config, tools, permission mode, vision caps, runtime
			// status — could be stale. Re-fetch the lot and tell the
			// user the link is back.
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "✓ agent reconnected"})
			// A tool-permission gate that was up during the restart is now
			// stale: the paused tool call and its blocked waiter died with the
			// old process, so answering it can never resolve anything. Mark it
			// so the y/n resolver stops pretending the gate is live and instead
			// re-runs the user's request on yes.
			if m.pendingConfirm != nil && m.pendingConfirm.tool != nil {
				m.pendingConfirm.stale = true
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("↻ the pending tool decision was lost when the agent restarted — press [y] to re-run that request, [n] to drop it, or type a new message and press Enter.")})
			}
			m.refreshViewport()
			return m, tea.Batch(msg.next, fetchConfigCmd(m.agent), fetchToolsCmd(m.agent), fetchPermissionModeCmd(m.agent), fetchVisionCmd(m.agent), fetchOpenRuntimeStatusCmd(m.agent))
		}
		if msg.state == agentclient.ConnStateFailed {
			// Only reachable when the client itself is shutting down
			// mid-recovery — the SDK otherwise retries indefinitely.
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "✕ agent connection closed — reconnect abandoned. Restart cercano to recover."})
			m.refreshViewport()
		}
		return m, msg.next

	case toolsLoadedMsg:
		cache := make(map[string]agentclient.ToolInfo, len(msg.Tools))
		for _, t := range msg.Tools {
			cache[t.Name] = t
		}
		m.toolCache = cache
		return m, nil

	case visionCapsMsg:
		m.supportsVision = msg.supported
		return m, nil

	case toolResultMsg:
		var body string
		if msg.Err != nil {
			body = "tool error: " + msg.Err.Error()
		} else if msg.Res.Error != "" {
			body = "tool error: " + msg.Res.Error
		} else {
			body = slash.RenderToolResult(msg.Res)
		}
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: body})
		m.refreshViewport()
		return m, nil

	case subAgentReopenMsg:
		m.applySubAgentReopen(msg)
		m.refreshViewport()
		return m, nil

	case streamEndMsg:
		// A dead turn's channel close is inert: running the completion path
		// here would tear down the LIVE turn — including invoking
		// m.cancelStream, which now belongs to it.
		if msg.gen != m.turnGen {
			return m, nil
		}
		m.streaming = false
		m.input.Placeholder = defaultInputPlaceholder
		m.mainChat().SetStreaming(false)
		// Turn completed normally; the rehydration cache is stale.
		m.lastSubmittedPrompt = ""
		if m.cancelStream != nil {
			m.cancelStream() // release the stream context on normal completion
			m.cancelStream = nil
		}
		// Finalize the streaming entry so it stops showing the spinner.
		if e := m.mainChat().lastAssistantEntry(); e != nil {
			e.Streaming = false
		}
		// The turn is over: drop any latched compaction flag or stuck
		// in-progress tool so the animation tick can go idle instead of
		// spinning a CPU core forever when a closing ctxUsageMsg or tool
		// completion event never arrived.
		m.clearTurnAnimationState()
		m.refreshViewport()
		// Poll the agent for the authoritative context-window usage on the
		// same conversation. Result arrives as a ctxUsageMsg and overrides
		// the local cumIn approximation we incremented during streaming.
		m.ctxPollTicks = 20 // ~40s warm window covers the compaction debounce
		// Only spawn the poll ticker if one isn't already running, so rapid
		// back-to-back turns don't multiply concurrent ctxUsageTick loops.
		pollCmds := []tea.Cmd{
			fetchContextUsage(m.agent, m.convID),
			fetchRecap(m.agent, m.convID),
			fetchNextPromptSuggestion(m.agent, m.convID),
		}
		if !m.ctxPolling {
			m.ctxPolling = true
			pollCmds = append(pollCmds, ctxUsageTick())
		}
		done := tea.Batch(pollCmds...)
		// Drain the next queued message: each completed turn fires the next.
		if nextMsg, ok := m.mainChat().DrainNext(); ok {
			m.relayout()
			nm, cmd := m.submit(nextMsg.text, nextMsg.images)
			return nm, tea.Batch(cmd, done)
		}
		return m, done

	case banner.TickMsg:
		// One tick chain serves both banner homes. While the splash chrome
		// is up, forward to the AnimModel (which re-issues the next frame).
		// After dismissal the same chain keeps the scrollback banner
		// shimmering: full frame rate while its rows are on-screen, a cheap
		// visibility poll while scrolled away, and it dies once no banner
		// entry exists (e.g. --mdtest). bannerTickActive tracks liveness so
		// applyResume knows whether it must restart the loop.
		if m.splashShown {
			var cmd tea.Cmd
			m.splash, cmd = m.splash.Update(msg)
			m.bannerTickActive = cmd != nil
			return m, cmd
		}
		if m.mainChat().BannerAnimVisible() {
			m.refreshViewport()
			m.bannerTickActive = true
			return m, banner.Tick()
		}
		if m.mainChat().HasBanner() {
			m.bannerTickActive = true
			return m, banner.PollTick()
		}
		m.bannerTickActive = false
		return m, nil

	case trajectoryExportEventMsg:
		if ev, ok := m.content.(*trajectoryExportView); ok {
			return m, ev.applyExportEvent(msg)
		}
		return m, nil

	case trajectoryExportErrMsg:
		if ev, ok := m.content.(*trajectoryExportView); ok {
			ev.err = msg.err.Error()
			ev.step = exportDone
		}
		return m, nil

	case resumeRequestedMsg:
		// Fired by the history picker's OnSelect after the overlay closes.
		var resumeCmd tea.Cmd
		m, resumeCmd = m.applyResume(msg.ConversationID)
		if msg.Title != "" {
			m.sessionTitle = msg.Title
		}
		return m, resumeCmd

	case dragScrollTickMsg:
		cmd, _ := m.activeChat().DragScrollTick()
		return m, cmd

	case ctxUsageTickMsg:
		if m.convID == "" {
			return m, nil
		}
		if m.ctxPollTicks > 0 {
			m.ctxPollTicks--
		}
		// Keep polling while we're in a warm window after a turn, or actively
		// compacting; otherwise let the loop go idle until the next turn.
		if m.ctxPollTicks > 0 || m.compacting {
			return m, tea.Batch(fetchContextUsage(m.agent, m.convID), ctxUsageTick())
		}
		m.ctxPolling = false                           // loop goes idle; the next turn restarts it
		return m, fetchContextUsage(m.agent, m.convID) // one final settle, no re-tick

	case progressAnimTickMsg:
		// This tick has fired and is no longer "in flight". Set inactive
		// up-front; if any condition keeps it alive, we set true again
		// before returning the next tick — that prevents a second kick
		// (e.g. from toolEntryStartMsg) from doubling the tick rate.
		m.animTickActive = false
		frameTime := time.Time(msg)
		m.mainChat().SetAnimationTime(frameTime)
		if av := m.activeChat(); av != nil && av != m.mainChat() {
			av.SetAnimationTime(frameTime)
		}
		if cv, ok := m.content.(*contextView); ok {
			cv.chat.SetAnimationTime(frameTime)
		}
		contentRepaint := false
		animRepaint := false
		keep := false
		// Flush coalesced stream events: token deltas and progress notes set
		// chatDirty instead of rebuilding per event, so this frame carries
		// their repaint. Keep ticking while the stream is alive — the next
		// batch of deltas needs a frame too.
		if m.chatDirty {
			contentRepaint = true
		}
		if m.streaming {
			keep = true
		}
		// The following branches are animation-only: they advance a spinner,
		// color sweep, or trailing "working" line inside the transcript. Those
		// glyphs still need occasional viewport refreshes, but not the 20Hz
		// full-transcript rebuild used for actual content deltas.
		if e := m.mainChat().streamingTextEntry(); e != nil && e.Content == "" {
			animRepaint = true
			keep = true
		}
		if m.mainChat().hasInProgressTool() {
			animRepaint = true
			keep = true
		}
		if m.mainChat().hasLoadingTool() {
			animRepaint = true
			keep = true
		}
		if m.mainChat().IsBetweenPhases() {
			animRepaint = true
			keep = true
		}
		// Also keep ticking while the /c chat is busy so its animated
		// status line repaints on every frame.
		if cv, ok := m.content.(*contextView); ok && cv.busy() {
			keep = true
		}
		// Keep the trailing "working" animation alive on the visible child tab
		// (sub-agent or activity) while a phase is running but quiet — the same
		// treatment the main chat gets, so a long planning/analyze step reads as
		// alive rather than frozen.
		if av := m.activeChat(); av != nil && av != m.mainChat() {
			if av.IsBetweenPhases() || av.hasInProgressTool() {
				animRepaint = true
				keep = true
			}
		}
		if m.compacting {
			// Compaction itself only animates footer/chrome. View() redraws that
			// cheaply on every tick; it should not force a transcript rebuild.
			keep = true
		}
		if contentRepaint {
			m.refreshViewport()
			m.lastAnimViewportRefresh = time.Time{}
		} else if animRepaint {
			// Animation-only frames are overlaid by chatView.View() on the visible
			// rows. Rebuilding the viewport here would re-scan the entire transcript
			// for links and line widths on every spinner tick, which makes large
			// conversations laggy even when chatDirty=false.
			m.lastAnimViewportRefresh = frameTime
		}
		if keep {
			m.animTickActive = true
			return m, progressAnimTick()
		}
		return m, nil
	}
	return m, nil
}

// promptImagesToInline converts prompt attachments to agentclient images.
func promptImagesToInline(atts []promptImage) []agentclient.InlineImage {
	if len(atts) == 0 {
		return nil
	}
	out := make([]agentclient.InlineImage, 0, len(atts))
	for _, a := range atts {
		out = append(out, agentclient.InlineImage{
			Index:     int32(a.id),
			Data:      a.data,
			MediaType: a.mediaType,
		})
	}
	return out
}

// plural returns "s" for n != 1, "" for n == 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (m Model) submit(text string, images []agentclient.InlineImage) (tea.Model, tea.Cmd) {
	// Normalize CR/CRLF line endings to LF at the submission boundary. Bare
	// CRs from terminal paste artifacts otherwise propagate into the request,
	// the store, and the provider (see docs/bugs/2026-07-04-user-message-tear.md).
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Record for ↑/↓ history recall (skip consecutive duplicates), and reset
	// the browse position back to the live input.
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.historyIdx = len(m.inputHistory)
	m.historyStash = ""

	if strings.HasPrefix(text, "/") {
		next, cmd := m.runSlash(text)
		return next, cmd
	}
	// A genuine new turn is starting: sweep away finished sub-agent tabs the
	// user has already moved past. Deferred to turn start (not turn end) so a
	// finished tab stays readable until the user acts; the active tab is spared
	// inside the sweep so we never yank what they're viewing.
	if m.hasSubAgentTabs() {
		m.cleanupFinishedSubAgentTabs()
	}
	// Stash the prompt so we can rehydrate the input if the agent crashes
	// mid-turn. Cleared on successful streamEndMsg; consumed on
	// connStateChangedMsg(Reconnecting) if the current turn is in flight.
	m.lastSubmittedPrompt = text
	// User turn — show markers + image count suffix when images are attached.
	content := text
	if len(images) > 0 {
		content = strings.TrimSpace(content)
		content += fmt.Sprintf("  (%d image%s)", len(images), plural(len(images)))
	}
	m.streaming = true
	m.input.Placeholder = steerInputPlaceholder
	m.mainChat().SetStreaming(true)
	m.turnStart = time.Now()
	m.turnActivity = "thinking"
	m.turnTokOut = 0
	m.turnModel = ""
	m.turnCloud = false
	m.turnToolStarted = 0
	m.turnToolDone = 0
	m.mainChat().SetAnimationTime(m.turnStart)
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: content})
	// Assistant placeholder
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	m.refreshViewport()

	if m.agent == nil {
		m.streaming = false
		m.input.Placeholder = defaultInputPlaceholder
		m.mainChat().SetStreaming(false)
		m.errMsg = "agent unavailable"
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "error: agent unavailable"})
		m.refreshViewport()
		return m, nil
	}

	// Pass the effective workDir so the agent chdirs there and prepends its
	// .cercano/context.md if present (/d pins this to the Cercano repo).
	// New turn = new generation: stream events from any prior (canceled) turn
	// become identifiable ghosts.
	m.turnGen++
	driver := &mainAgentDriver{agent: m.agent, convID: m.convID, workDir: m.effectiveWorkDir()}
	cmd, cancel, err := driver.Submit(context.Background(), m.turnGen, text, images)
	if err != nil {
		m.errMsg = err.Error()
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "error: " + err.Error()})
		m.refreshViewport()
		return m, nil
	}
	m.cancelStream = cancel
	// Fire both the driver's self-re-arming drain and the progress-text
	// animator; both re-issue themselves until streaming ends. Mark the
	// anim loop as running so the tool-start kick path doesn't double-fire it.
	m.animTickActive = true
	return m, tea.Batch(cmd, progressAnimTick())
}

// effectiveWorkDir returns the work_dir to send with a turn: the /d override
// when set, else the process cwd.
func (m Model) effectiveWorkDir() string {
	if m.workDirOverride != "" {
		return m.workDirOverride
	}
	wd, _ := os.Getwd()
	return wd
}

// applyDevMode enters development mode: pin the session workDir to the repo
// and return the canned kickoff prompt for the caller to submit through the
// normal chat path (so it streams, persists, and meters like a typed turn).
func (m *Model) applyDevMode(repo string) string {
	m.workDirOverride = repo
	if m.wdRef != nil {
		m.wdRef.dir = repo
	}
	m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "dev mode: working on " + repo + "\nDebug controls enabled. Try /debug help."})
	return slash.DevKickoff(repo)
}

// recallHistoryPrev steps the prompt back to an older submitted input. Returns
// true if it consumed the key (recalled, or already at the oldest entry).
func (m *Model) recallHistoryPrev() bool {
	n := len(m.inputHistory)
	if n == 0 {
		return false
	}
	switch {
	case m.historyIdx >= n: // at the live input — stash it, jump to newest
		m.historyStash = m.input.Value()
		m.historyIdx = n - 1
	case m.historyIdx == 0: // already oldest
		return true
	default:
		m.historyIdx--
	}
	m.setInputValue(m.inputHistory[m.historyIdx])
	return true
}

// recallHistoryNext steps the prompt forward toward newer submitted inputs, then
// back to the stashed live input. Returns true if it consumed the key.
func (m *Model) recallHistoryNext() bool {
	n := len(m.inputHistory)
	if n == 0 || m.historyIdx >= n { // not browsing
		return false
	}
	m.historyIdx++
	if m.historyIdx >= n { // stepped past the newest — restore the live input
		m.setInputValue(m.historyStash)
		return true
	}
	m.setInputValue(m.inputHistory[m.historyIdx])
	return true
}

// setInputValue replaces the prompt contents, parks the cursor at the end, and
// re-fits the layout (recalled history may be multi-line).
func (m *Model) setInputValue(s string) {
	m.input.SetValue(s)
	m.input.CursorEnd()
	m.relayout()
}

// cancelCurrentStream aborts an in-flight prompt: cancel the StreamChat context
// (the gRPC stream closes), drop streaming state, finalize the placeholder, and
// append a muted "canceled" note. Any late events are ignored by the
// chatStreamMsg guard once m.streaming is false.
func (m *Model) cancelCurrentStream() {
	m.cancelCurrentStreamWithNotice(true)
}

func (m *Model) cancelCurrentStreamSilently() {
	m.cancelCurrentStreamWithNotice(false)
}

func (m *Model) cancelCurrentStreamWithNotice(showNotice bool) {
	if m.cancelStream != nil {
		m.cancelStream()
		m.cancelStream = nil
	}
	// Retire the turn's generation: its remaining in-flight events (the
	// cancel error, the channel close) are ghosts from here on, even if the
	// user submits a new prompt before they arrive.
	m.turnGen++
	m.streaming = false
	m.input.Placeholder = defaultInputPlaceholder
	m.mainChat().SetStreaming(false)
	if e := m.mainChat().lastAssistantEntry(); e != nil {
		e.Streaming = false
	}
	// Compaction is a property of the active turn. Canceling severs the
	// ctxUsageMsg poll that would otherwise deliver the closing
	// Compacting=false, so clear it here — a latched compacting flag keeps the
	// 50ms animation tick alive forever and pins a CPU core until restart.
	m.clearTurnAnimationState()
	if showNotice {
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "⊘ canceled"})
	}
	// Queued follow-ups are preserved: canceling stops the current work but
	// anything the user typed while waiting is a real intent they still want
	// executed. The Esc-key caller drains the next queued message and
	// submits it after this returns.
	m.relayout()
}

// clearTurnAnimationState resets the turn-scoped flags that keep the 50ms
// progress-animation tick alive. Any of them left latched after a turn ends —
// a compacting flag whose closing ctxUsageMsg never arrived, a tool row stuck
// in-progress because its completion event was dropped — makes the tick
// self-perpetuate and pin a CPU core until the process restarts. Every
// turn-termination path must call this, not just the happy-path done event.
func (m *Model) clearTurnAnimationState() {
	m.compacting = false
	m.mainChat().resolveStaleInProgressTools()
}

func (m Model) runSlash(line string) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(strings.TrimSpace(line), "/debug") {
		args := strings.Fields(strings.TrimSpace(line))
		if len(args) > 0 {
			args = args[1:]
		}
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: m.runDebugTaskPaneSlash(args)})
		m.refreshViewport()
		return m, nil
	}
	res, _ := m.registry.Dispatch(line)
	switch res.Kind {
	case slash.ResultQuit:
		return m, tea.Quit
	case slash.ResultClearConversation:
		m.mainChat().SetEntriesSlice(nil)
		m.convID = newConvID()
		if m.convRef != nil {
			m.convRef.id = m.convID
		}
		m.workDirOverride = ""
		if m.wdRef != nil {
			m.wdRef.dir = ""
		}
		m.sessionTitle = ""
		m.cumIn = 0
		m.cumOut = 0
		m.mainChat().ExitToolNav()
		m.refreshViewport()
	case slash.ResultOpenSettings:
		return m, m.openConfigSurface(configTabGeneral)
	case slash.ResultOpenThemeSettings:
		return m, m.openConfigSurface(configTabUI)
	case slash.ResultOpenHistoryPicker:
		hv, cmd := newHistoryView(m.agent, m.palette, m.styles, m.width, m.height)
		m.content = hv
		return m, cmd
	case slash.ResultOpenRuntimeDashboard:
		return m, m.openConfigSurface(configTabModels)
	case slash.ResultOpenMcpConfig:
		return m, m.openConfigSurface(configTabMcp)
	case slash.ResultOpenRuntimeConfig:
		return m, m.openConfigSurface(configTabRuntime)
	case slash.ResultOpenContextView:
		return m, m.openConfigSurface(configTabContext)
	case slash.ResultOpenWizard:
		m.content = newWizardPage(m.agent, m.palette, m.styles, m.width, m.height)
	case slash.ResultOpenTrajectoryExport:
		ev, _ := newTrajectoryExportView(m.agent, m.palette, m.styles, m.width, m.height, m.convID, res.Text)
		m.content = ev
	case slash.ResultRegenContext:
		if m.convID == "" {
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "no conversation yet — nothing to rebuild"})
			m.refreshViewport()
			return m, nil
		}
		return m, startContextRegenCmd(m.agent, m.convID, false)
	case slash.ResultCompactContext:
		if m.convID == "" {
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "no conversation yet — nothing to compact"})
			m.refreshViewport()
			return m, nil
		}
		return m, startContextRegenCmd(m.agent, m.convID, true)
	case slash.ResultClearCompactedContext:
		if m.convID == "" {
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "no conversation yet — nothing to clear"})
			m.refreshViewport()
			return m, nil
		}
		return m, startClearCompactedContextCmd(m.agent, m.convID)
	case slash.ResultElideContext:
		if m.convID == "" {
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "no conversation yet — nothing to elide"})
			m.refreshViewport()
			return m, nil
		}
		return m, startElideContextCmd(m.agent, m.convID)
	case slash.ResultResumeConversation:
		// /resume <id> path — slash already validated against the agent.
		var resumeCmd tea.Cmd
		m, resumeCmd = m.applyResume(res.Text)
		return m, resumeCmd
	case slash.ResultSetPromptColor:
		m.promptBorderColor = m.resolvePromptColor(res.Text)
		m.promptColorToken = res.Text
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "prompt color set"})
		m.refreshViewport()
	case slash.ResultSetSessionTitle:
		m.sessionTitle = res.Text
		// Route through AppendNotice: a title rename can land mid-stream, and a
		// plain append would splice this line into an in-progress assistant
		// message, splitting it (fenced code blocks torn in half). AppendNotice
		// inserts above the open stream so continuation tokens stay contiguous.
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "renamed to: " + res.Text})
		m.refreshViewport()
	case slash.ResultSetPermissionMode:
		// Fire-and-forget: server persistence is the source of truth, but the
		// local cache flips immediately so the status-bar chip reflects the
		// new mode on the very next frame. If the RPC fails the local UI
		// lies briefly; the next restart re-reads from the server.
		mode := res.PermissionMode
		ag := m.agent
		go func() {
			_ = ag.SetPermissionMode(context.Background(), mode)
		}()
		m.permissionMode = mode
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "Permission mode → " + mode})
		m.refreshViewport()
	case slash.ResultSetSessionProfile:
		// Switch the active capability profile (planning fence / future modes).
		// Fire-and-forget like the permission mode: takes effect on the next
		// turn (the runner reads the active profile live). On error, surface it.
		name := res.SessionProfile
		ag := m.agent
		convID := m.convID
		go func() {
			if err := ag.SetSessionProfile(context.Background(), convID, name); err != nil {
				// best-effort: the next turn simply won't be fenced
				_ = err
			}
		}()
		label := name
		if name == "default" {
			label = "off (unrestricted)"
		}
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "Mode → " + label})
		m.refreshViewport()
	case slash.ResultSubmitPrompt:
		if m.streaming {
			m.mainChat().Enqueue(res.Text, nil)
			m.relayout()
			return m, nil
		}
		return m.submit(res.Text, nil)
	case slash.ResultInvokeTool:
		if res.ToolName == debugTaskPaneToolName {
			msg, err := m.runDebugTaskPaneTool(res.ToolArgs)
			if err != nil {
				msg = "debug task pane: " + err.Error()
			}
			m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: msg})
			m.refreshViewport()
			return m, nil
		}
		// Decide locally whether to prompt: R-tier runs silently, W/X
		// queues a pending confirm.
		info := m.toolCache[res.ToolName]
		perm := info.Permission
		if perm == "" {
			// Unknown tool — let the server respond with an error so the
			// user gets a clear message rather than a silent no-op.
			return m, invokeToolCmd(m.agent, res.ToolName, res.ToolArgs)
		}
		if perm == "R" {
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("running tool:" + res.ToolName)})
			m.refreshViewport()
			return m, invokeToolCmd(m.agent, res.ToolName, res.ToolArgs)
		}
		// W or X — queue confirm.
		tc := &pendingToolCall{Name: res.ToolName, Args: res.ToolArgs, Permission: perm}
		m.pendingConfirm = toolConfirm(tc)
		prompt := m.renderConfirmPrompt(tc)
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: prompt})
		m.refreshViewport()
	case slash.ResultDevMode:
		kickoff := m.applyDevMode(res.WorkDir)
		m.refreshViewport()
		if m.streaming {
			// A stream is already in flight: enqueue the kickoff so it drains
			// at streamEndMsg, just like any typed follow-up.
			m.mainChat().Enqueue(kickoff, nil)
			m.relayout()
			return m, nil
		}
		return m.submit(kickoff, nil)
	case slash.ResultRestartAgent:
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: "↻ restarting agent — the connection will drop and reconnect momentarily…"})
		m.refreshViewport()
		return m, dispatchAgentRestart(m.agent, res.Text)
	case slash.ResultText:
		m.mainChat().AppendNotice(&Entry{Role: RoleSystem, Content: res.Text})
		m.refreshViewport()
	}
	return m, nil
}

// dispatchAgentRestart asks the agent to bounce itself, then returns so the
// CLI's reconnect loop can bring up (or re-attach to) a fresh agent. The RPC
// error is swallowed — a dropped connection mid-shutdown is the expected shape,
// and reconnect state surfaces the outcome through the normal channels.
func dispatchAgentRestart(ag *agentclient.Client, reason string) tea.Cmd {
	return func() tea.Msg {
		if reason == "" {
			reason = "user-requested restart"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ag.ShutdownAgent(ctx, reason)
		return nil
	}
}

// applyTurnTelemetry folds a done event's telemetry into the host footer
// fields. The host owns the footer; chatView.Apply owns the transcript.
func (m *Model) applyTurnTelemetry(d chatDoneMsg) {
	if d.notice != "" {
		m.cloudState = "NONE"
	} else {
		m.cloudState = "ok"
	}
	m.tokIn = d.tokIn
	m.tokOut = d.tokOut
	m.hadTurn = true
	// cumIn/cumOut here are local approximations until the agent answers
	// GetContextUsage; the RPC's authoritative total overrides cumIn on arrival.
	m.cumIn += d.tokIn
	m.cumOut += d.tokOut
	if d.model != "" {
		m.lastModel = d.model
	}
}

// relayout sets viewport / input widths and the viewport height so the
// rendered View exactly fills the terminal — status bar pinned to the bottom.
//
// Splash is rendered only when the terminal is wide enough to hold the
// fixed-width banner. Suggestion height (which depends on the current input
// value + registry help text + wrap width) is computed here so the
// viewport's allocated height matches what View will actually emit, no
// per-frame state mutation needed.
//
// View structure (lines):
//
//	header  (1)
//	─────   (1)
//	splash  (8) + blank (1)   ← only when splash visible
//	viewport (bodyH)
//	─────   (1)
//	suggestion (variable, 0..N)
//	input   (1)
//	─────   (1)
//	status  (1)
type promptLayoutSignature struct {
	inputHeight      int
	suggestionHeight int
}

func (m Model) promptLayoutSignature() promptLayoutSignature {
	sig := promptLayoutSignature{inputHeight: m.input.Height()}
	if m.mainChat().Width() > 0 && !m.contentPageActive() {
		if hint := m.renderSlashSuggestions(); hint != "" {
			sig.suggestionHeight = strings.Count(hint, "\n") + 1
		}
	}
	return sig
}

func (m *Model) relayout() {
	contentW := m.width
	if paneW := m.taskPaneWidth(); paneW > 0 {
		contentW -= paneW
	}
	if contentW < 20 {
		contentW = 20
	}
	const chromeNoInput = 5 // header + 3 dividers + status (input height added below)
	splashH := 0
	if m.splashEffective() {
		splashH = 9 // 8 banner rows + 1 blank
	}
	// Viewport's first screen row = header (1) + divider (1) + splash height,
	// plus the ephemeral chat tab row when sub-agent tabs are visible.
	m.scrollbarTop = 2 + splashH
	if m.hasSubAgentTabs() && !m.contentPageActive() {
		m.scrollbarTop += 2 // chat tab strip row + its underline rule
	}
	suggestH := 0
	if m.mainChat().Width() > 0 && !m.contentPageActive() {
		// Width may not yet match contentW on the first paint; the
		// suggestion uses m.width which we've just updated above.
		if hint := m.renderSlashSuggestions(); hint != "" {
			suggestH = strings.Count(hint, "\n") + 1
		}
	}
	// Reserve a row for the living-recap line when it's shown — View() renders it
	// below the viewport, and promptTop() already accounts for it. Without this
	// the viewport is one row too tall and shoves the status bar off-screen.
	recapH := 0
	if n := len(m.recapLines()); n > 0 {
		recapH = 1 + n // blank spacer line + the wrapped recap line(s)
	}
	queuedH := 0
	if !m.contentPageActive() {
		queuedH = len(m.queuedLines()) // wrapped rows for queued messages, rendered above the prompt
	}
	// Size the input first — DynamicHeight re-fits it to the wrapped content at
	// this width; the body claims whatever rows are left.
	m.input.SetWidth(contentW - 4)
	inputH := m.input.Height()
	bodyH := m.height - chromeNoInput - inputH - splashH - suggestH - recapH - queuedH
	if m.hasSubAgentTabs() && !m.contentPageActive() {
		bodyH -= 2 // chat tab strip row + its underline rule
	}
	if bodyH < 3 {
		bodyH = 3
	}
	m.mainChat().SetSize(contentW-2, bodyH) // reserve two right columns: a gap + the scrollbar
	for id, tab := range m.chatTabs.tabs {
		if id == mainChatTabID || tab == nil {
			continue
		}
		tab.view.SetSize(contentW-2, bodyH)
		tab.view.rebuild()
	}
	// Record the strip's shown-state so refreshViewport() can detect a drift
	// (a tab created/closed between layouts) and re-run us before painting.
	m.stripShown = m.hasSubAgentTabs() && !m.contentPageActive()
	m.refreshViewport()
}

func (m Model) contentPageActive() bool {
	return m.content != nil
}

func (m Model) contentTop() int {
	top := 2
	if m.splashEffective() {
		top += 9
	}
	if m.configSurface != nil {
		top += configStripRows
	}
	return top
}

func (m Model) activeContentScroller() (contentPageScroller, contentPageScrollState, bool) {
	if m.content == nil {
		return nil, contentPageScrollState{}, false
	}
	scroller, ok := m.content.(contentPageScroller)
	if !ok {
		return nil, contentPageScrollState{}, false
	}
	state := scroller.ScrollState()
	if state.Height < 1 || state.Total <= state.Height {
		return nil, contentPageScrollState{}, false
	}
	return scroller, state, true
}

func (m Model) contentScrollbarAt(mouse tea.Mouse) (contentPageScroller, contentPageScrollState, bool) {
	scroller, state, ok := m.activeContentScroller()
	if !ok {
		return nil, contentPageScrollState{}, false
	}
	top := m.contentTop()
	onBar := mouse.X >= m.width-1 &&
		mouse.Y >= top && mouse.Y < top+state.Height
	if !onBar {
		return nil, contentPageScrollState{}, false
	}
	return scroller, state, true
}

// handleImagePaste attaches image chips if the pasted text resolves to image
// file path(s). Returns true if it consumed the paste (caller must NOT insert
// the text literally); false means treat the paste as normal text.
func (m *Model) handleImagePaste(pasted string) bool {
	imgs, ok := classifyImagePaste(pasted)
	if !ok {
		return false
	}
	for _, img := range imgs {
		m.input.AddImage(img.data, img.mediaType, img.source)
	}
	return true
}

// handleClipboardImage attaches a chip if the OS clipboard holds an image.
// Returns true if it attached one. Rejects images exceeding maxDroppedImageBytes.
func (m *Model) handleClipboardImage() bool {
	data, mt, ok := clipboardImage()
	if !ok {
		return false
	}
	if len(data) > maxDroppedImageBytes {
		m.errMsg = fmt.Sprintf("clipboard image too large (%d MiB; limit %d MiB)", len(data)>>20, maxDroppedImageBytes>>20)
		return false
	}
	m.input.AddImage(data, mt, "")
	return true
}

// handleCtrlCKey owns the app-wide Ctrl+C contract for every content page:
// clear selected/composed prompt text first, arm quit on the first empty
// Ctrl+C, and quit only on the second consecutive empty Ctrl+C.
func (m Model) handleCtrlCKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.input.HasSelection() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	if m.input.Value() != "" {
		m.input.SetValue("")
		m.ctrlCArmed = false
		m.input.Placeholder = defaultInputPlaceholder
		return m, nil
	}
	if m.ctrlCArmed {
		return m, tea.Quit
	}
	m.ctrlCArmed = true
	m.input.Placeholder = armedInputPlaceholder
	return m, nil
}

// promptTop returns the absolute 0-based screen row at which the prompt text
// input is rendered. It derives from composeFrame — the same layout the
// renderer uses to place the real terminal cursor — so the mouse hit-test can
// never drift from the drawn position. The previous implementation re-summed
// the layout by hand (contentTop + viewport height + recap + queued + border +
// slash hint) and silently omitted the sub-agent tab-strip rows and the
// spare-row padding inserted above the prompt when content doesn't fill the
// screen; those omissions pushed the hit-test off the real input row in every
// case except a plain, splash-off, full-height frame, which is why clicking
// the prompt to select/copy appeared to do nothing while the scrollback
// viewport worked.
func (m Model) promptTop() int {
	parts, inputIdx := m.composeFrame()
	return inputCursorRow(parts, inputIdx)
}

func (m Model) mouseInPrompt(mouse tea.Mouse) bool {
	top := m.promptTop()
	return mouse.X >= 0 &&
		mouse.X < m.width &&
		mouse.Y >= top &&
		mouse.Y < top+m.input.Height()
}

// maxInputLines caps how tall the prompt grows before it scrolls internally.
const maxInputLines = 6

// splashEffective reports whether the splash banner is currently showable.
// We hide it on terminals narrower than the banner's fixed width because
// the 62-col chrome wraps catastrophically on a 40-col terminal — every
// banner row becomes 2-3 terminal rows, the whole layout fragments.
func (m Model) splashEffective() bool {
	return m.splashShown && m.width >= banner.Width
}

// refreshViewport rebuilds the viewport content from the chatView's owned
// entries at the current width. Syncs turn telemetry first so the render
// has current state, then delegates to chatView.rebuild().
func (m *Model) refreshViewport() {
	start := time.Now()
	defer func() { m.logSlowRefreshViewport(start) }()

	// If the chat tab strip appeared or vanished since the last relayout, the
	// viewport height and scrollbarTop are stale (the strip's two rows are not
	// reserved). Re-lay-out first; relayout() calls back here with stripShown in
	// sync, so this recurses at most once. Single choke point that keeps every
	// tab-mutation path (create, sweep, close) self-correcting.
	if want := m.hasSubAgentTabs() && !m.contentPageActive(); want != m.stripShown {
		m.relayout()
		return
	}
	m.chatDirty = false // any full rebuild flushes pending coalesced repaints
	m.mainChat().SetTurnStatus(turnStatus{
		activity: m.turnActivity,
		start:    m.turnStart,
		tokOut:   m.turnTokOut,
		model:    m.turnModel,
		cloud:    m.turnCloud,
	})
	m.activeChat().rebuild()
}

func (m Model) preparePromptInput() Model {
	needsRefresh := m.activeChat().InToolNav()
	if m.activeChat().SelectionActive() {
		m.activeChat().ClearSelection()
	}
	m.selectionNotice = ""
	if needsRefresh {
		m.activeChat().ExitToolNav()
		m.refreshViewport()
	}
	return m
}

const entryIndent = 2

// isHeadingBlock reports whether a prose block leads with an ATX heading marker.
func isHeadingBlock(b render.MdBlock) bool {
	return b.Kind == render.MdProse && strings.HasPrefix(strings.TrimSpace(b.Raw), "#")
}

// trimBlankEdgeLines drops leading and trailing lines that are visually empty —
// Glamour pads code blocks with a blank line top and bottom, which we don't want
// inside our rules. Lines carry ANSI escapes (a "blank" line is escape codes +
// spaces), so emptiness is judged on the ANSI-stripped text. Interior blank
// lines are preserved.
func trimBlankEdgeLines(s string) string {
	lines := strings.Split(s, "\n")
	blank := func(l string) bool { return strings.TrimSpace(ansi.Strip(l)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func paintCodeBlockBackground(s string, width int, p theme.Palette) string {
	if s == "" {
		return s
	}
	bg := theme.CodeBlockBackgroundSGR(p)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		pad := width - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		padded := line + strings.Repeat(" ", pad)
		lines[i] = bg + ansiSGRRe.ReplaceAllStringFunc(padded, func(r string) string {
			return r + bg
		}) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// codeRule renders a full-width horizontal rule delimiting a code block. With a
// language it reads `─── go ─────────…`; without one it's a plain rule. The rule
// is muted; the language label is cyan to echo the inline-code color.
func codeRule(lang string, width int, styles theme.Styles) string {
	if width < 4 {
		width = 4
	}
	if lang == "" {
		return styles.Muted.Render(strings.Repeat("─", width))
	}
	fill := width - (lipgloss.Width(lang) + 5) // "─── " (4) + lang + " " (1)
	if fill < 0 {
		fill = 0
	}
	return styles.Muted.Render("─── ") +
		styles.BufferCode.Render(lang) +
		styles.Muted.Render(" "+strings.Repeat("─", fill))
}

// closeOpenFence appends a closing code fence when the tail has an odd number of
// fence lines, so Glamour renders an in-progress code block instead of leaking
// the rest as raw text.
func closeOpenFence(s string) string {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			n++
		}
	}
	if n%2 == 1 {
		return s + "\n```"
	}
	return s
}

// animateSpinnerGlyph renders the spinner symbol with two layered motions:
//
//  1. A clockwise block-rotation through 8 half/quarter-block glyphs
//     (`▌▘▀▝▐▗▄▖`) at 100ms/frame — gives the visual of a square rolling
//     in place. Ties to the wordmark block-letter aesthetic.
//
//  2. A sine-modulated brightness pulse (lime → white → lime) at 1.5s cycle,
//     phase-locked to the same wall clock the status-text sweep uses. So the
//     spinner peaks white in roughly the same rhythm as the sweep peak
//     crosses the text, then dims back to lime base.
//
// Both motions are wall-clock-driven so phases stay smooth across status
// changes; nothing per-entry to track.
func animateSpinnerGlyph() string {
	return animateSpinnerGlyphAt(time.Now())
}

func spinnerFrameAt(t time.Time) (string, int) {
	const frames = "▌▘▀▝▐▗▄▖"
	const frameMs = 100
	runes := []rune(frames)
	idx := int(t.UnixMilli()/frameMs) % len(runes)
	return string(runes[idx]), idx
}

func animateSpinnerGlyphAt(t time.Time) string {
	return animateSpinnerGlyphAtForPalette(t, theme.Cracker())
}

func animateSpinnerGlyphAtForPalette(t time.Time, p theme.Palette) string {
	const pulseCycleMs = 1500

	// Rotation.
	glyph, _ := spinnerFrameAt(t)

	// Brightness pulse stays in the theme's spinner color family while sharing
	// the wall-clock rhythm used by the activity-text sweep.
	phase := float64(t.UnixMilli()%int64(pulseCycleMs)) / float64(pulseCycleMs)
	pulse := 0.5 + 0.5*math.Sin(phase*2*math.Pi)
	return lipgloss.NewStyle().Foreground(theme.SpinnerColorAt(p, pulse)).Render(glyph)
}

// animateLimeSweep renders `text` with a per-char color sweep — lime base, a
// bright peak (lime→white) traveling left-to-right on a 1.5s loop. Phase is
// derived from wall-clock time so the animation stays smooth regardless of
// when the status text last changed.
func animateLimeSweep(text string) string {
	return animateLimeSweepAt(text, time.Now())
}

func animateLimeSweepAt(text string, t time.Time) string {
	return animateActivitySweepAt(text, t, theme.Cracker())
}

func animateActivitySweepAt(text string, t time.Time, p theme.Palette) string {
	const (
		cycleMs = 1500 // one full sweep duration
		tail    = 4.0  // half-width of the bright band, in columns
		padCols = 4.0  // off-screen lead-in / trail-out
	)
	// Wall-clock phase, 0..1.
	phaseMs := t.UnixMilli() % int64(cycleMs)
	progress := float64(phaseMs) / float64(cycleMs)

	cols := utf8.RuneCountInString(text)
	sweepPos := -padCols + progress*(float64(cols)+2*padCols)

	var b strings.Builder
	col := 0
	for _, r := range text {
		b.WriteString(lipgloss.NewStyle().
			Foreground(theme.ActivityColorAt(p, col, sweepPos, tail)).
			Render(string(r)))
		col++
	}
	return b.String()
}

// applyResume calls the agent's ResumeConversation RPC, switches the active
// conversation id, clears the splash, and re-renders the persisted turns
// into the scrollback so the user picks up exactly where they left off.
// resolvePromptColor maps a slash-command color token into a lipgloss.Color
// the View can apply directly. Tokens take one of two shapes:
//
//   - `#RRGGBB` — literal hex, used as-is
//   - `palette:<key>` — looked up against the model's palette
//
// Falls back to the current value (silently — the slash command already
// validated; this is just the model-side dispatch).
func (m Model) resolvePromptColor(token string) color.Color {
	if strings.HasPrefix(token, "#") {
		return lipgloss.Color(token)
	}
	if strings.HasPrefix(token, "palette:") {
		switch strings.TrimPrefix(token, "palette:") {
		case "primary":
			return m.palette.Primary
		case "accent":
			return m.palette.Accent
		case "info":
			return m.palette.Info
		case "success":
			return m.palette.Success
		case "warn":
			return m.palette.Warn
		case "error":
			return m.palette.Error
		case "muted":
			return m.palette.Muted
		case "bright":
			return m.palette.Bright
		case "border":
			return m.palette.Border
		case "border_dim":
			return m.palette.BorderDim
		}
	}
	return m.promptBorderColor
}

// resumeEntries maps persisted turns to scrollback entries, dropping turns with
// no displayable prose. Lossless persistence stores tool_use (assistant) and
// tool_result (user) turns whose Content is empty — their payload lives in
// content_json, which the resume RPC does not carry. Rendering those as entries
// produces blank gaps with floating ▶ markers, so they are skipped here.
//
// When frozenThrough > 0 (compaction has happened), a RoleDivider entry is
// inserted at the freeze boundary so the user sees WHERE in the scrollback
// the model's verbatim context begins. Turns with CreatedAt.Unix() <=
// frozenThrough are frozen (in the recap); turns above this timestamp are
// live (model sees them verbatim). The count of frozen turns is included
// in the divider label.
func resumeEntries(turns []agentclient.PersistedTurn, frozenThrough int64) []*Entry {
	entries := make([]*Entry, 0, len(turns)+1)
	insertDivider := frozenThrough > 0
	dividerInserted := false
	frozenCount := 0
	if insertDivider {
		for _, t := range turns {
			if t.CreatedAt.Unix() <= frozenThrough {
				frozenCount++
			}
		}
	}
	for _, t := range turns {
		if strings.TrimSpace(t.Content) == "" {
			continue
		}
		// Insert the divider before the first live (post-freeze) turn.
		if insertDivider && !dividerInserted && t.CreatedAt.Unix() > frozenThrough {
			entries = append(entries, &Entry{
				Role:    RoleDivider,
				Content: fmt.Sprintf("⟲ %d turn(s) compacted into recap above · live context below", frozenCount),
			})
			dividerInserted = true
		}
		role := RoleSystem
		switch t.Role {
		case "user":
			role = RoleUser
		case "assistant":
			role = RoleAssistant
		}
		entries = append(entries, &Entry{Role: role, Content: t.Content})
	}
	// All turns were frozen — no live tail. The divider belongs at the end so
	// the user's next prompt lands clearly below the freeze line.
	if insertDivider && !dividerInserted {
		entries = append(entries, &Entry{
			Role:    RoleDivider,
			Content: fmt.Sprintf("⟲ %d turn(s) compacted into recap above · live context below", frozenCount),
		})
	}
	return entries
}

// resumeInputHistory extracts the prior session's submitted prompts from the
// persisted turns so ↑/↓ recall works immediately after a resume. Only user
// turns carry prompts; assistant/tool turns are skipped. The result is
// oldest-first (matching how live submits append to inputHistory) and drops
// consecutive duplicates the same way recordSubmittedInput does, so the recall
// order after a resume is identical to what it would have been had the session
// never been interrupted.
func resumeInputHistory(turns []agentclient.PersistedTurn) []string {
	var hist []string
	for _, t := range turns {
		if t.Role != "user" {
			continue
		}
		text := strings.ReplaceAll(t.Content, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if n := len(hist); n > 0 && hist[n-1] == text {
			continue
		}
		hist = append(hist, text)
	}
	return hist
}

// restoreSubAgentTabs reopens the sub-agent chat tabs for a resumed
// conversation from their persisted transcripts. Each child dispatch loop was
// saved as a hidden "subagent" conversation linked to this parent (see the
// dispatch tool's persistence path); ListSubAgents returns them in spawn order
// so a nested child is always processed after its parent, letting
// nextSubAgentTitle recompute "sub 1" / "sub 1.1"-style labels from the
// parent/child relationships. Best-effort: any RPC failure leaves the main
// resume intact and simply skips (or partially fills) tab restore.
// dismissSubAgentTab best-effort tells the server to stop reopening this
// sub-agent's tab on future resumes. Fire-and-forget: if it fails, the tab may
// reappear after a restart, and the next close/sweep re-dismisses it. Skips the
// main tab and no-ops when there's no agent client (tests).
func (m *Model) dismissSubAgentTab(id string) {
	if m.agent == nil || id == mainChatTabID || id == "" {
		return
	}
	agent := m.agent
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = agent.DismissSubAgent(ctx, id)
	}()
}

func (m *Model) restoreSubAgentTabs(ctx context.Context, conversationID string) {
	children, err := m.agent.ListSubAgents(ctx, conversationID)
	if err != nil || len(children) == 0 {
		return
	}
	// Start from a clean slate so child ordinals number from 1 and no tabs from
	// a previously-active conversation linger in the strip.
	m.dropSubAgentTabs()
	for _, child := range children {
		turns, terr := m.agent.ResumeConversation(ctx, child.ID)
		if terr != nil {
			continue
		}
		// ensureSubAgentTab mints the "sub N" / nested label from child.ParentID;
		// SetEntriesSlice then replaces the placeholder "started" banner with the
		// persisted transcript. frozenThrough is 0 here: sub-agent loops are not
		// compacted, so there is no freeze boundary to mark.
		view := m.ensureSubAgentTab(child.ID, child.ParentID, "", child.GrantedTools)
		view.SetEntriesSlice(resumeEntries(turns, 0))
		view.rebuild()
		// Sub-agent rows with only the delegated user prompt (or only synthetic
		// lifecycle text) are stale starts that never produced a readable
		// transcript. Dismiss them so a wedged dispatch tab does not reopen forever.
		if tab := m.chatTabs.tabs[child.ID]; tab != nil && !subAgentTabHasSubstantiveTranscript(tab) {
			m.closeSubAgentTab(child.ID)
			continue
		}
		// Mark done+restored so the tab renders without an activity dot and, per
		// cleanupFinishedSubAgentTabs, survives the next turn's sweep.
		if tab := m.chatTabs.tabs[child.ID]; tab != nil {
			tab.done = true
			tab.restored = true
		}
	}
}

// applyResume updates the model + the convRef shared with the slash registry,
// then rehydrates scrollback from the persisted turns. Returns a tea.Cmd that
// fetches authoritative context usage from the server — the polling loop only
// arms after a turn completes, so without this the meter sits at 0 from
// resume until the user takes their next turn. The cmd is the only way to
// drive the meter from this code path; callers MUST plumb it through.
func (m Model) applyResume(conversationID string) (Model, tea.Cmd) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	turns, err := m.agent.ResumeConversation(ctx, conversationID)
	if err != nil {
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "resume failed: " + err.Error()})
		m.refreshViewport()
		return m, nil
	}
	m.convID = conversationID
	if m.convRef != nil {
		m.convRef.id = conversationID
	}
	m.workDirOverride = ""
	if m.wdRef != nil {
		m.wdRef.dir = ""
	}
	m.mainChat().SetEntriesSlice(nil)
	// cumIn/cumOut wait for fetchContextUsage. The previous local sum here
	// summed only TokensIn, mishandled tool-call turns, and got overwritten
	// by the RPC within a roundtrip anyway — a wrong first-paint with no
	// upside.
	m.cumIn = 0
	m.cumOut = 0
	m.mainChat().ExitToolNav()
	m.splashShown = false
	// Fetch the compaction state so we can place the freeze-boundary divider
	// in the right spot inside the resumed history. Failure is non-fatal —
	// the resume still works, just without the divider.
	var frozenThrough int64
	if cs, err := m.agent.GetCompactionState(ctx, conversationID); err == nil && cs != nil {
		frozenThrough = cs.FrozenThrough
	}
	m.mainChat().SetEntriesSlice(resumeEntries(turns, frozenThrough))
	m.hydrateTaskPaneFromResumedTurns(turns)
	// Seed ↑/↓ prompt recall from the resumed session's submitted prompts so
	// the user can replay prior inputs immediately, exactly as they could
	// before the CLI restarted. historyIdx parks at the live input (past the
	// end) so the first ↑ recalls the most recent prompt.
	m.inputHistory = resumeInputHistory(turns)
	m.historyIdx = len(m.inputHistory)
	m.historyStash = ""
	m.mainChat().PrependBanner(m.splash.Meta, m.splash.Started())
	// Restore the prior session's living recap into the footer line (renderRecap),
	// or show a "recap unavailable" placeholder if the recap generator has been
	// silently failing (e.g. local runtime misconfigured). Don't push into
	// scrollback — that showed the recap twice on resume.
	if info, err := m.agent.GetConversation(ctx, conversationID); err == nil {
		m.recap = recapDisplay(info)
	}
	// Reopen the conversation's sub-agent tabs from their persisted
	// transcripts so a resumed session shows the same child chat tabs it had
	// before the CLI restarted. Best-effort; never blocks the main resume.
	m.restoreSubAgentTabs(ctx, conversationID)
	m.relayout()
	cmds := []tea.Cmd{
		fetchContextUsage(m.agent, m.convID),
		// Seed the footer mode chip: a resumed conversation may already be in
		// planning mode, and no broadcast will fire until the next flip.
		fetchSessionProfileCmd(m.agent, m.convID),
	}
	if !m.bannerTickActive {
		// The tick chain died before this resume (e.g. launching straight
		// into the history picker never shows the splash); restart it so the
		// prepended scrollback banner animates.
		m.bannerTickActive = true
		cmds = append(cmds, banner.Tick())
	}
	return m, tea.Batch(cmds...)
}

// rolloverConfirm builds the y/n gate for a rollover offer. Yes calls
// AcceptRollover and, on success, resumes into the returned fresh conversation
// (option B: switch, leaving a visible seam in scrollback); a rejected/errored
// accept leaves the session where it is with a note. No calls DeclineRollover so
// the server re-arms the offer after further growth. Both branches clear
// pendingConfirm by returning a model whose gate the caller drops.
func (m Model) rolloverConfirm(ev rolloverOfferedMsg) *confirmRequest {
	return &confirmRequest{
		onYes: func(mm Model) (Model, tea.Cmd) {
			mm.pendingConfirm = nil
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			newID, err := mm.agent.AcceptRollover(ctx, ev.convID, ev.offerID)
			if err != nil || newID == "" {
				note := "rollover could not start"
				if err != nil {
					note += ": " + err.Error()
				}
				mm.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: mm.styles.Muted.Render(note)})
				mm.refreshViewport()
				return mm, nil
			}
			// Leave a visible seam in the OLD session before switching, so the
			// boundary is legible on scroll-back.
			mm.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: mm.styles.Muted.Render("↪ rolled over to a fresh session — continuing there")})
			var cmd tea.Cmd
			mm, cmd = mm.applyResume(newID)
			return mm, cmd
		},
		onNo: func(mm Model) (Model, tea.Cmd) {
			mm.pendingConfirm = nil
			convID, offerID, ag := ev.convID, ev.offerID, mm.agent
			go func() { _ = ag.DeclineRollover(context.Background(), convID, offerID) }()
			mm.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: mm.styles.Muted.Render("staying in this session")})
			mm.refreshViewport()
			return mm, nil
		},
	}
}

// renderRolloverPrompt is the scrollback message shown while a rollover offer is
// pending: why it's offered, a short preview of the handoff that would seed the
// new session, and the y/n hints on their own line.
func (m Model) renderRolloverPrompt(ev rolloverOfferedMsg) string {
	head := m.styles.SelectionCaret.Render("▸ ")
	title := "Start a fresh session? " + ev.reason
	lines := []string{head + m.styles.AgentProse.Render(title)}
	if p := rolloverPreviewSnippet(ev.preview); p != "" {
		lines = append(lines, "  "+m.styles.Muted.Render("handoff: "+p))
	}
	hints := m.styles.Muted.Render("[") +
		m.styles.Accent.Render("y") +
		m.styles.Muted.Render("]es / [") +
		m.styles.Accent.Render("n") +
		m.styles.Muted.Render("]o")
	lines = append(lines, "  "+hints)
	return strings.Join(lines, "\n")
}

// rolloverPreviewSnippet condenses the multi-line handoff artifact to a single
// short line for the confirm prompt (the full artifact seeds the new session's
// first turn, so it isn't lost).
func rolloverPreviewSnippet(preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return ""
	}
	if i := strings.IndexByte(preview, '\n'); i >= 0 {
		preview = preview[:i]
	}
	const max = 120
	if len(preview) > max {
		preview = preview[:max-1] + "…"
	}
	return preview
}

// renderConfirmPrompt builds the confirm message shown in scrollback while
// pendingConfirm is set. The first line names the requested action, optional
func (m Model) renderConfirmRequest(c *confirmRequest) string {
	if c == nil {
		return ""
	}
	if c.tool != nil {
		return m.renderConfirmPrompt(c.tool)
	}
	head := m.styles.SelectionCaret.Render("▸ ")
	lines := []string{head + m.styles.AgentProse.Render(c.title)}
	for _, detail := range c.details {
		lines = append(lines, "  "+m.styles.AgentProse.Render(detail))
	}
	if c.hints != "" {
		lines = append(lines, "  "+c.hints)
	}
	return strings.Join(lines, "\n")
}

// human-facing intent/details follow, and key hints stay on their own final
// line so long summaries never wrap mid-hint. W-tier renders normally; X-tier
// gets a red ⚠ destructive emphasis. MCP tools get an additional [a]lways key.
func (m Model) renderConfirmPrompt(p *pendingToolCall) string {
	head := m.styles.SelectionCaret.Render("▸ ")
	if isSessionControlTool(p.Name) {
		// Session-control prompts (enter/exit planning or autonomous mode) are
		// X-tier so the gate always fires, but they destroy nothing — never
		// render the red DESTRUCTIVE emphasis. A calm marker instead.
		head = m.styles.SelectionCaret.Render("▸ ")
	} else if p.Permission == "X" {
		if isDispatchTool(p.Name) {
			head = m.styles.Error.Render("▸ ⚠ DELEGATED ")
		} else {
			head = m.styles.Error.Render("▸ ⚠ DESTRUCTIVE ")
		}
	} else if p.Destructive {
		// MCP tool that self-reports a destructive hint: surface a ⚠ marker
		// (display-only — gating is unchanged; the hint never escalates tier).
		head = m.styles.Accent.Render("▸ ⚠ ")
	}

	lines := []string{head + m.styles.AgentProse.Render(confirmPromptTitle(p))}
	if p.Name == "request_plan_approval" {
		lines = append(lines, m.renderPlanApprovalConfirmDetails(p)...)
	} else if p.Name == "suggest_autonomous" || p.Name == "request_autonomous_execution" {
		lines = append(lines, m.renderAutonomousBriefConfirmDetails(p)...)
	} else if p.Name == "request_autonomous_exit" {
		lines = append(lines, m.renderAutonomousExitConfirmDetails(p)...)
	} else {
		for _, detail := range confirmPromptDetails(p) {
			lines = append(lines, "  "+m.styles.AgentProse.Render(detail))
		}
	}
	lines = append(lines, "  "+m.confirmPromptHints(p))
	return strings.Join(lines, "\n")
}

func (m Model) confirmPromptHints(p *pendingToolCall) string {
	hints := m.styles.Muted.Render("[") +
		m.styles.Accent.Render("y") +
		m.styles.Muted.Render("]es / [") +
		m.styles.Accent.Render("n") +
		m.styles.Muted.Render("]o / [") +
		m.styles.Accent.Render("d") +
		m.styles.Muted.Render("]etails")
	if p.ToolUseID != "" && !isSessionControlTool(p.Name) {
		hints += m.styles.Muted.Render(" / [") +
			m.styles.Accent.Render("c") +
			m.styles.Muted.Render("]hat")
	}
	if strings.HasPrefix(p.Name, "mcp__") {
		hints += m.styles.Muted.Render(" / [") +
			m.styles.Accent.Render("a") +
			m.styles.Muted.Render("]lways")
	}
	return hints
}

func confirmPromptTitle(p *pendingToolCall) string {
	if title := sessionControlPromptTitle(p); title != "" {
		return title
	}
	if isDispatchTool(p.Name) {
		return displayToolName(p.Name) + " wants to run a delegated agent"
	}
	if summary := toolSpecificConfirmSummary(p); summary != "" {
		return displayToolName(p.Name) + " " + summary
	}
	summary := summarizeArgsWithoutKeys(p.Args, 120, "intent")
	if summary == "" {
		return displayToolName(p.Name)
	}
	return displayToolName(p.Name) + " " + summary
}

func toolSpecificConfirmSummary(p *pendingToolCall) string {
	obj, ok := decodeArgObject(p.Args)
	if !ok {
		return ""
	}
	switch p.Name {
	case "git_land":
		feature := oneLine(stringArg(obj, "feature"))
		if feature == "" {
			feature = "current branch"
		}
		trunk := oneLine(stringArg(obj, "trunk"))
		if trunk == "" {
			trunk = "trunk"
		}
		if boolArg(obj, "continue") {
			return "continue landing " + feature + " onto " + trunk
		}
		summary := "land " + feature + " onto " + trunk
		if strategy := oneLine(stringArg(obj, "strategy")); strategy != "" {
			summary += " (" + strategy + ")"
		}
		return summary
	}
	return ""
}

func (m Model) renderPlanApprovalConfirmDetails(p *pendingToolCall) []string {
	obj, ok := decodeArgObject(p.Args)
	if !ok {
		return nil
	}
	bodyWidth := m.confirmBlockBodyWidth()
	lines := make([]string, 0, 8)
	addTextSection := func(heading, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.Accent.Bold(true).Render(heading))
		wrapped := strings.Split(ansi.Wrap(oneLine(text), bodyWidth, ""), "\n")
		for _, line := range wrapped {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, "    "+m.styles.AgentProse.Render(line))
			}
		}
	}
	addTextSection("Plan", stringArg(obj, "summary"))
	addTextSection("Effort", stringArg(obj, "effort"))
	addTextSection("Spec", stringArg(obj, "spec_path"))
	addTextSection("Plan file", stringArg(obj, "plan_path"))
	return lines
}

func (m Model) renderAutonomousBriefConfirmDetails(p *pendingToolCall) []string {
	obj, ok := decodeArgObject(p.Args)
	if !ok {
		return nil
	}
	bodyWidth := m.confirmBlockBodyWidth()
	lines := make([]string, 0, 16)
	addTextSection := func(heading, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.Accent.Bold(true).Render(heading))
		wrapped := strings.Split(ansi.Wrap(oneLine(text), bodyWidth, ""), "\n")
		for _, line := range wrapped {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, "    "+m.styles.AgentProse.Render(line))
			}
		}
	}
	addListSection := func(heading string, vals []string) {
		vals = compactStringList(vals)
		if len(vals) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.Accent.Bold(true).Render(heading))
		for _, val := range vals {
			wrapped := strings.Split(ansi.Wrap(oneLine(val), bodyWidth-2, ""), "\n")
			for i, line := range wrapped {
				if strings.TrimSpace(line) == "" {
					continue
				}
				prefix := "    • "
				if i > 0 {
					prefix = "      "
				}
				lines = append(lines, prefix+m.styles.AgentProse.Render(line))
			}
		}
	}
	addTextSection("Plan", stringArg(obj, "summary"))
	addTextSection("Effort", stringArg(obj, "effort"))
	addTextSection("Spec", stringArg(obj, "spec_path"))
	addTextSection("Plan file", stringArg(obj, "plan_path"))
	addTextSection("Why", stringArg(obj, "reason"))
	addTextSection("Goal", stringArg(obj, "goal"))
	addListSection("Done when", stringSliceArg(obj["done_when"]))
	addListSection("Constraints", stringSliceArg(obj["constraints"]))
	addListSection("Review points", stringSliceArg(obj["review_points"]))
	return lines
}

func (m Model) renderAutonomousExitConfirmDetails(p *pendingToolCall) []string {
	obj, ok := decodeArgObject(p.Args)
	if !ok {
		return nil
	}
	bodyWidth := m.confirmBlockBodyWidth()
	lines := make([]string, 0, 6)
	addTextSection := func(heading, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.styles.Accent.Bold(true).Render(heading))
		wrapped := strings.Split(ansi.Wrap(oneLine(text), bodyWidth, ""), "\n")
		for _, line := range wrapped {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, "    "+m.styles.AgentProse.Render(line))
			}
		}
	}
	addTextSection("Summary", stringArg(obj, "summary"))
	addTextSection("Verification", stringArg(obj, "verification"))
	return lines
}

func (m Model) confirmBlockBodyWidth() int {
	bodyWidth := m.width - 6
	if bodyWidth < 40 {
		bodyWidth = 80
	}
	return bodyWidth
}

func confirmPromptDetails(p *pendingToolCall) []string {
	obj, ok := decodeArgObject(p.Args)
	if !ok {
		return nil
	}
	details := make([]string, 0, 2)
	if isSessionControlTool(p.Name) {
		// Show the model's rationale, plan summary, or autonomous brief as
		// supporting detail while keeping the title a clean question.
		if reason := oneLine(stringArg(obj, "reason")); reason != "" {
			details = append(details, "Why: "+truncateArgs(reason, 200))
		}
		if summary := oneLine(stringArg(obj, "summary")); summary != "" {
			label := "Plan: "
			if p.Name == "request_autonomous_exit" || p.Name == "complete_autonomous_review" {
				label = "Summary: "
			}
			details = append(details, label+truncateArgs(summary, 200))
		}
		if goal := oneLine(stringArg(obj, "goal")); goal != "" {
			details = append(details, "Goal: "+truncateArgs(goal, 200))
		}
		if done := summarizeStringSlice(obj["done_when"]); done != "" {
			details = append(details, "Done when: "+truncateArgs(done, 200))
		}
		if constraints := summarizeStringSlice(obj["constraints"]); constraints != "" {
			details = append(details, "Constraints: "+truncateArgs(constraints, 200))
		}
		if review := summarizeStringSlice(obj["review_points"]); review != "" {
			details = append(details, "Review points: "+truncateArgs(review, 200))
		}
		if verification := oneLine(stringArg(obj, "verification")); verification != "" {
			details = append(details, "Verification: "+truncateArgs(verification, 200))
		}
		if effort := oneLine(stringArg(obj, "effort")); effort != "" {
			details = append(details, "Effort: "+truncateArgs(effort, 80))
		}
		if spec := oneLine(stringArg(obj, "spec_path")); spec != "" {
			details = append(details, "Spec: "+truncateArgs(spec, 120))
		}
		if plan := oneLine(stringArg(obj, "plan_path")); plan != "" {
			details = append(details, "Plan file: "+truncateArgs(plan, 120))
		}
		return details
	}
	if intent := oneLine(stringArg(obj, "intent")); intent != "" {
		details = append(details, "Intent: "+truncateArgs(intent, 160))
	} else if isDispatchTool(p.Name) {
		if task := oneLine(stringArg(obj, "task")); task != "" {
			details = append(details, "Task: "+truncateArgs(task, 160))
		}
	}
	if isDispatchTool(p.Name) {
		tools := summarizeToolList(obj["tools"])
		if tools != "" {
			details = append(details, "Tools: "+truncateArgs(tools, 160))
		}
		if risk := dispatchToolRisk(tools); risk != "" {
			details = append(details, "Risk: "+risk)
		}
	}
	return details
}

func dispatchToolRisk(tools string) string {
	parts := strings.Split(tools, ",")
	for _, part := range parts {
		if strings.TrimSpace(part) == "Bash" {
			return "Bash grants shell access; approve only trusted tasks."
		}
	}
	if tools != "" {
		return "Delegated tools run as one approved unit."
	}
	return ""
}

func isDispatchTool(name string) bool {
	return name == "dispatch" || name == "workflow"
}

// isSessionControlTool reports whether a confirm prompt is a session-control
// action rather than a real tool invocation. These are X-tier (so the gate
// always fires) but destroy nothing, and deserve a plain-English question
// instead of the raw "name arg=val" dump.
func isSessionControlTool(name string) bool {
	switch name {
	case "suggest_plan", "request_plan_approval", "plan_exit", "suggest_autonomous", "request_autonomous_execution", "request_autonomous_exit", "complete_autonomous_review", "auto_exit":
		return true
	}
	return false
}

// sessionControlToolRowLabel returns the short user-facing label for a
// session-control tool when it appears in scrollback. It intentionally hides the
// raw tool name and argument dump so mode transitions read like product actions,
// not dangerous implementation calls.
func sessionControlToolRowLabel(name string) string {
	switch name {
	case "suggest_plan":
		return "Plan mode"
	case "request_plan_approval":
		return "Plan approval"
	case "plan_exit":
		return "Leave plan mode"
	case "suggest_autonomous":
		return "Autonomous run brief"
	case "request_autonomous_execution":
		return "Autonomous execution"
	case "request_autonomous_exit":
		return "Autonomous final review"
	case "complete_autonomous_review":
		return "Autonomous review complete"
	case "auto_exit":
		return "Leave autonomous mode"
	}
	return ""
}

// sessionControlPromptTitle returns the human-facing question for planning and
// autonomous session-control prompts. Supporting reason/summary/brief details
// are surfaced separately in confirmPromptDetails, so the title stays a clean
// one-liner.
func sessionControlPromptTitle(p *pendingToolCall) string {
	switch p.Name {
	case "suggest_plan":
		return "Enter plan mode to work this out before making changes?"
	case "request_plan_approval":
		return "Plan is ready — leave plan mode and start executing it?"
	case "plan_exit":
		return "Leave plan mode?"
	case "suggest_autonomous":
		return "Start autonomous mode with this run brief?"
	case "request_autonomous_execution":
		return "Plan approved. Execute it autonomously with this run brief?"
	case "request_autonomous_exit":
		return "Autonomous run complete — review completion details?"
	case "complete_autonomous_review":
		return "Final autonomous review accepted — exit autonomous mode?"
	case "auto_exit":
		return "Leave autonomous mode?"
	}
	return ""
}

func stringArg(obj map[string]any, key string) string {
	if s, ok := obj[key].(string); ok {
		return s
	}
	return ""
}

func boolArg(obj map[string]any, key string) bool {
	if b, ok := obj[key].(bool); ok {
		return b
	}
	return false
}

func summarizeStringSlice(v any) string {
	return strings.Join(stringSliceArg(v), "; ")
}

func stringSliceArg(v any) []string {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return compactStringList(parts)
}

func compactStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func summarizeToolList(v any) string {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// displayToolName converts MCP tool names (mcp__server__tool) to a readable
// slash form (mcp/server/tool). Non-MCP names are returned unchanged.
func displayToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	rest := strings.TrimPrefix(name, "mcp__")
	if i := strings.Index(rest, "__"); i >= 0 {
		return "mcp/" + rest[:i] + "/" + rest[i+2:]
	}
	return name
}

func (m Model) handlePendingConfirmKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if key.Matches(msg, keys.ScrollKeys) {
		cmd := m.activeChat().Update(msg)
		return m, cmd
	}
	// Confirm pending: the prompt input is disabled. Only explicit confirm
	// hotkeys resolve the gate; all text, paste, Enter, Esc-as-clear, and Ctrl+C
	// prompt-editing behavior is ignored here unless the confirm itself binds it.
	keyStr := msg.String()
	if next, cmd, handled := m.resolveConfirmHotkey(keyStr); handled {
		return next, cmd
	}
	return m, nil
}

func (m Model) steerPendingConfirm(text string) (Model, tea.Cmd) {
	c := m.pendingConfirm
	if c == nil {
		return m, nil
	}
	if text == "" {
		return m, nil
	}
	if c.stale && c.tool != nil {
		m.pendingConfirm = nil
		m.input.SetValue("")
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Accent.Render("↻ submitting your new message on the restarted agent…")})
		m.refreshViewport()
		next, cmd := m.submit(text, nil)
		if nm, ok := next.(Model); ok {
			return nm, cmd
		}
		return m, cmd
	}
	if c.tool != nil && isSessionControlTool(c.tool.Name) {
		m.input.SetValue("")
		m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("Session-control prompts require an explicit key: press y to approve, n to decline, or d for details.")})
		m.refreshViewport()
		return m, nil
	}
	id := ""
	if c.tool != nil {
		id = c.tool.ToolUseID
	}
	m.pendingConfirm = nil
	convID, ag := m.convID, m.agent
	m.input.SetValue("")
	m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("↳ steer: " + text)})
	m.refreshViewport()
	if id != "" && ag != nil {
		go func() { _ = ag.DenyToolCallWithMessage(context.Background(), convID, id, text) }()
	}
	return m, nil
}

func (m Model) confirmToolUseID() string {
	if m.pendingConfirm != nil && m.pendingConfirm.tool != nil {
		return m.pendingConfirm.tool.ToolUseID
	}
	return ""
}

// resolveConfirmKey processes a keystroke while a confirm is pending.
// y/Y → onYes, n/N/esc/ctrl+c → onNo, extras keys → their handler,
// anything else → ignored (confirm stays pending).
func (m Model) resolveConfirmKey(key string) (Model, tea.Cmd) {
	next, cmd, _ := m.resolveConfirmHotkey(key)
	return next, cmd
}

func (m Model) resolveConfirmHotkey(key string) (Model, tea.Cmd, bool) {
	c := m.pendingConfirm
	if c == nil {
		return m, nil, false
	}
	// Stale tool gate: the server-side turn died (agent restart) so the
	// Allow/Deny RPCs would resolve nothing. Don't call onYes/onNo — they'd
	// print "approved — running…" and silently orphan the call. Instead, yes
	// re-runs the original request as a fresh turn; no/esc drops it honestly.
	if c.stale && c.tool != nil {
		switch key {
		case "y", "Y":
			m.pendingConfirm = nil
			prompt := c.retryPrompt
			if prompt == "" {
				prompt = m.lastSubmittedPrompt
			}
			if prompt == "" {
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("nothing to re-run — the original request wasn't captured.")})
				m.refreshViewport()
				return m, nil, true
			}
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Accent.Render("↻ re-running your request on the restarted agent…")})
			m.refreshViewport()
			next, cmd := m.submit(prompt, nil)
			if nm, ok := next.(Model); ok {
				return nm, cmd, true
			}
			return m, cmd, true
		case "n", "N", "esc", "ctrl+c":
			m.pendingConfirm = nil
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("dropped the lost tool decision.")})
			m.refreshViewport()
			return m, nil, true
		case "d", "D":
			// Details are still local (just show the args); leave the gate up.
			if fn, ok := c.extras[key]; ok {
				next, cmd := fn(m)
				return next, cmd, true
			}
			return m, nil, true
		default:
			// Confirm pending disables the prompt input even for stale gates; the
			// user must answer with the advertised hotkeys.
			return m, nil, true
		}
	}
	switch key {
	case "y", "Y":
		next, cmd := c.onYes(m)
		return next, cmd, true
	case "n", "N", "esc", "ctrl+c":
		next, cmd := c.onNo(m)
		return next, cmd, true
	default:
		if fn, ok := c.extras[key]; ok {
			next, cmd := fn(m)
			return next, cmd, true
		}
		return m, nil, false
	}
}

func reauthConfirm(req reauthRequiredMsg) *confirmRequest {
	profile := req.profile
	if profile == "" {
		profile = "claude"
	}
	note := req.note
	if note == "" {
		note = "Claude sign-in expired."
	}
	var detailsEntry *Entry
	toggleDetails := func(m Model) (Model, tea.Cmd) {
		if detailsEntry != nil {
			if m.mainChat().RemoveEntry(detailsEntry) {
				detailsEntry = nil
				m.refreshViewport()
				return m, nil
			}
			detailsEntry = nil
		}
		detailsEntry = &Entry{Role: RoleSystem, Content: "auth details:\n" + note}
		m.mainChat().AppendEntry(detailsEntry)
		m.refreshViewport()
		return m, nil
	}
	return &confirmRequest{
		title: "Claude sign-in expired",
		details: []string{
			"Cercano could not refresh the Claude subscription token.",
			"Re-authenticate now, or dismiss and keep using the backup profile for this turn.",
		},
		hints: "[" + "y" + "]es re-auth / [" + "n" + "]o dismiss / [" + "d" + "]etails",
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			if m.agent == nil {
				m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Error.Render("Claude sign-in unavailable — no agent connection.")})
				m.refreshViewport()
				return m, nil
			}
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Accent.Render("opening Claude sign-in…")})
			m.refreshViewport()
			m.claudeLoginModal = newClaudeLoginModal(profile, "")
			return m, startClaudeLoginCmd(m.agent, profile, "", true)
		},
		onNo: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("Claude re-auth dismissed.")})
			m.refreshViewport()
			return m, nil
		},
		extras: map[string]func(Model) (Model, tea.Cmd){
			"d": toggleDetails,
			"D": toggleDetails,
		},
	}
}

// toolConfirm builds the confirmRequest for a tool-permission decision,
// preserving the prior behavior: y approves (Allow RPC for stream-origin call,
// else local invoke), n denies (Deny RPC for stream-origin), d/D reveals args.
// MCP tools (mcp__*) additionally expose an [a]lways key that persists the
// allow server-side so future calls run silently.
func toolConfirm(tc *pendingToolCall) *confirmRequest {
	var detailsEntry *Entry
	toggleDetails := func(m Model) (Model, tea.Cmd) {
		if detailsEntry != nil {
			if m.mainChat().RemoveEntry(detailsEntry) {
				detailsEntry = nil
				m.refreshViewport()
				return m, nil
			}
			detailsEntry = nil
		}
		detailsEntry = &Entry{Role: RoleSystem,
			Content: "details:\n```json\n" + tc.Args + "\n```"}
		m.mainChat().AppendEntry(detailsEntry)
		m.refreshViewport()
		return m, nil
	}
	cr := &confirmRequest{
		tool: tc,
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Accent.Render("✓ approved — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				// Stream-event origin: unblock the server-side tool loop.
				ag, id, convID := m.agent, tc.ToolUseID, m.convID
				if ag != nil {
					go func() { _ = ag.AllowToolCall(context.Background(), convID, id) }()
				}
				return m, nil
			}
			// Local /tool origin: fire the invoke directly.
			return m, invokeToolCmd(m.agent, tc.Name, tc.Args)
		},
		onNo: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("canceled.")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id, convID := m.agent, tc.ToolUseID, m.convID
				if ag != nil {
					go func() { _ = ag.DenyToolCall(context.Background(), convID, id) }()
				}
			}
			return m, nil
		},
		extras: map[string]func(Model) (Model, tea.Cmd){
			"d": toggleDetails,
			"D": toggleDetails,
		},
	}
	// [c]hat about this: dismiss the confirm and drop into the compose sub-state.
	// Only for stream-origin ordinary tools — a local /tool invoke has no server
	// tool loop to redirect, and session-control prompts are explicit y/n/d gates
	// because redirecting them would deny the control transition while feeding the
	// user's text back to the model.
	if tc.ToolUseID != "" && !isSessionControlTool(tc.Name) {
		enterCompose := func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.composeToolUseID = tc.ToolUseID
			m.input.SetValue("")
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem,
				Content: m.styles.Muted.Render("↳ chat about this — type your redirect and press enter (esc cancels)")})
			m.refreshViewport()
			return m, nil
		}
		cr.extras["c"] = enterCompose
		cr.extras["C"] = enterCompose
	}
	// MCP tools are confirm-by-default; offer always-allow, which persists a
	// silent allowlist rule server-side so future calls bypass the prompt.
	if strings.HasPrefix(tc.Name, "mcp__") {
		cr.extras["a"] = func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.mainChat().AppendEntry(&Entry{Role: RoleSystem,
				Content: m.styles.Accent.Render("✓ always-allowed — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id, convID := m.agent, tc.ToolUseID, m.convID
				if ag != nil {
					go func() { _ = ag.AllowToolCallPersist(context.Background(), convID, id, true) }()
				}
			}
			return m, nil
		}
	}
	return cr
}

// writeExport writes the export JSON to <dir>/cercano-context-<conv8>.json and
// returns the absolute path.
func writeExport(dir, convID, jsonBody string) (string, error) {
	id := convID
	if len(id) > 8 {
		id = id[:8]
	}
	path := filepath.Join(dir, "cercano-context-"+id+".json")
	if err := os.WriteFile(path, []byte(jsonBody), 0o644); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs, nil
	}
	return path, nil
}

type exportDoneMsg struct {
	path string
	err  error
}

func exportContextCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		body, err := ag.ExportContext(ctx, convID)
		if err != nil {
			return exportDoneMsg{err: err}
		}
		dir, _ := os.Getwd()
		path, err := writeExport(dir, convID, body)
		return exportDoneMsg{path: path, err: err}
	}
}

// handleContextViewKey owns the keyboard while the /c context viewer is the
// active page: typing edits the main prompt bar, enter submits an edit
// instruction to the pane, scroll keys move the turn list, and esc on an empty
// bar closes the page.
func (m Model) handleContextViewKey(cv *contextView, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			return m, nil
		}
		// Closing /c — drop any queued pane messages.
		cv.chat.ClearQueue()
		m.content = nil
		m.contentScrollbarDragging = false
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			if cv.focusedTurn >= 0 && cv.focusedTurn < len(cv.snapshot.Turns) {
				cv.toggleExpand(cv.snapshot.Turns[cv.focusedTurn].ID)
			}
			return m, nil
		}
		m.input.SetValue("")
		return m.submitContextEdit(cv, text)
	case "tab":
		cv.focusNextExpandable(+1)
		return m, nil
	case "shift+tab":
		cv.focusNextExpandable(-1)
		return m, nil
	case "up":
		// With an empty prompt: first pop the last queued message back for
		// editing (mirrors d808952 in the main chat); otherwise move the section
		// focus backward (right/left arrows expand/collapse — see below). With a
		// non-empty prompt, fall through so the textarea owns cursor movement.
		if m.input.Value() == "" {
			if turn, ok := cv.chat.UnstageLast(); ok {
				m.input.SetValue(turn.text)
				for _, img := range turn.images {
					m.input.RegisterImage(int(img.Index), img.Data, img.MediaType, "")
				}
				return m, nil
			}
			cv.focusNextExpandable(-1)
			return m, nil
		}
	case "down":
		// Empty prompt: move the section focus forward (does NOT expand).
		// Non-empty prompt: fall through to the textarea.
		if m.input.Value() == "" {
			cv.focusNextExpandable(+1)
			return m, nil
		}
	case "right":
		// Empty prompt: expand the focused section. Non-empty: textarea cursor.
		if m.input.Value() == "" {
			cv.setFocusedExpanded(true)
			return m, nil
		}
	case "left":
		// Empty prompt: collapse the focused section. Non-empty: textarea cursor.
		if m.input.Value() == "" {
			cv.setFocusedExpanded(false)
			return m, nil
		}
	case "pgup", "ctrl+b":
		cv.ScrollBy(-dashboardContentHeight(cv.height))
		return m, nil
	case "pgdown", "ctrl+f":
		cv.ScrollBy(dashboardContentHeight(cv.height))
		return m, nil
	case "ctrl+u":
		cv.ScrollBy(-maxInt(1, dashboardContentHeight(cv.height)/2))
		return m, nil
	case "ctrl+d":
		cv.ScrollBy(maxInt(1, dashboardContentHeight(cv.height)/2))
		return m, nil
	case "ctrl+r":
		// Manual refresh. Must NOT be a bare "r" — in /c the prompt bar is the
		// input, so a bare letter hotkey would swallow that letter while typing.
		cv.snapshot = loadContextSnapshot(cv.agent, cv.convID)
		return m, nil
	case "ctrl+o":
		// Toggle between the sent (compacted) view and the full original. Reset
		// focus (the visible turn range changes) and clear any export notice.
		cv.showOriginal = !cv.showOriginal
		cv.focusedTurn = -1
		cv.notice = ""
		cv.ScrollTo(0)
		return m, nil
	case "ctrl+e":
		return m, exportContextCmd(cv.agent, cv.convID)
	}
	// Everything else edits the prompt bar.
	m = m.preparePromptInput()
	var cmd tea.Cmd
	prev := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != prev {
		m.relayout()
	}
	return m, cmd
}

// submitContextEdit submits a /c edit instruction: enqueue while busy, else
// append the user entry + an open streaming placeholder, set the working status,
// and fire the driver. Mirrors the main page's submit path (sendChatMessage).
func (m Model) submitContextEdit(cv *contextView, input string) (Model, tea.Cmd) {
	if cv.busy() {
		cv.chat.Enqueue(input, nil)
		return m, nil
	}
	cv.busyFlag = true
	cv.chat.AppendEntry(&Entry{Role: RoleUser, Content: input})
	cv.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	cv.chat.SetTurnStatus(turnStatus{activity: "working…", start: time.Now()})
	cv.chat.rebuild()
	return m, tea.Batch(cv.driver.Submit(context.Background(), input), progressAnimTick())
}

// routeChatMsg routes a chat event to the active *contextView. On chatConfirmMsg
// it fills-and-closes the open streaming placeholder with the rationale (so no
// orphaned working… entry remains) and raises the shared confirm gate. On done
// and error it clears the explicit busy flag. All other events flow through
// chatView.Apply, with the queue auto-draining when a turn ends.
func (m Model) routeChatMsg(msg tea.Msg) (Model, tea.Cmd) {
	cv, ok := m.content.(*contextView)
	if !ok {
		return m, nil
	}
	if rm, ok := msg.(reauthRequiredMsg); ok {
		cv.chat.Apply(chatProgressMsg{note: rm.note})
		if m.pendingConfirm == nil {
			m.pendingConfirm = reauthConfirm(rm)
			cv.chat.AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmRequest(m.pendingConfirm)})
		}
		cv.chat.rebuild()
		return m, progressAnimTick()
	}
	if cm, isConfirm := msg.(chatConfirmMsg); isConfirm {
		// Fill the open streaming placeholder with the rationale rather than
		// appending after it — prevents a frozen working… orphan mid-transcript.
		// Fall back to a fresh append if no placeholder is open.
		if !cv.chat.FillOpenAssistant(cm.assistant) {
			cv.chat.Apply(chatAssistantMsg{text: cm.assistant})
		}
		cv.chat.rebuild()
		onYes, onNo := cm.onYes, cm.onNo
		m.pendingConfirm = &confirmRequest{
			onYes: func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, onYes },
			onNo: func(m Model) (Model, tea.Cmd) {
				m.pendingConfirm = nil
				cv.busyFlag = false
				cv.chat.rebuild()
				return m, onNo
			},
		}
		return m, progressAnimTick()
	}
	switch msg.(type) {
	case chatDoneMsg, chatErrorMsg:
		cv.busyFlag = false
	}
	cv.chat.Apply(msg)
	cv.chat.rebuild()
	if !cv.busy() {
		if next, ok := cv.chat.DrainNext(); ok {
			return m.submitContextEdit(cv, next.text)
		}
		return m, nil
	}
	return m, progressAnimTick()
}

// summarizeArgs renders tool arguments for the confirm prompt one-liner. JSON
// objects become key=value summaries so approval prompts explain the requested
// action instead of showing raw JSON. The [d]etails action still exposes the full
// original args when the user wants exact details.
func summarizeArgs(s string, max int) string {
	return summarizeArgsWithoutKeys(s, max)
}

func summarizeArgsWithoutKeys(s string, max int, omit ...string) string {
	var decoded any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return truncateArgs(s, max)
	}
	if dec.More() {
		return truncateArgs(s, max)
	}

	if obj, ok := decoded.(map[string]any); ok && len(obj) > 0 {
		for _, key := range omit {
			delete(obj, key)
		}
		if len(obj) == 0 {
			return ""
		}
		keys := orderedArgKeys(obj)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+"="+summarizeArgValue(obj[key]))
		}
		return truncateArgs(strings.Join(parts, " "), max)
	}

	return truncateArgs(summarizeArgValue(decoded), max)
}

func decodeArgObject(s string) (map[string]any, bool) {
	var decoded any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	obj, ok := decoded.(map[string]any)
	return obj, ok
}

func orderedArgKeys(obj map[string]any) []string {
	preferred := []string{
		"conversation_id",
		"task",
		"cmd",
		"cwd",
		"path",
		"file_path",
		"pattern",
		"query",
		"url",
		"tools",
	}
	seen := make(map[string]bool, len(obj))
	keys := make([]string, 0, len(obj))
	for _, key := range preferred {
		if _, ok := obj[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(obj)-len(keys))
	for key := range obj {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func summarizeArgValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return oneLine(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, summarizeArgValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateArgs keeps the confirm prompt summary to one line.
func truncateArgs(s string, max int) string {
	s = oneLine(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// abbreviateModel produces a short display name for the header strip:
//
//   - claude-opus-4-7    → opus 4.7
//   - claude-sonnet-4-6  → sonnet 4.6
//   - claude-haiku-4-5   → haiku 4.5
//   - qwen3-coder:latest → qwen3-coder  (strip the latest tag)
//   - llama_server:catalog:glm-4.5-air-q4_k_m → glm-4.5-air-q4_k_m
//   - /models/foo/bar.gguf → bar.gguf
//   - gemini-1.5-pro     → gemini 1.5 pro (collapse the dashes that aren't
//     part of the version)
//   - anything-else      → returned verbatim
//
// Aim is signal at a glance; if the abbreviation hides a distinction the
// user can always /config to see full names.
func abbreviateModel(name string) string {
	if name == "" {
		return "—"
	}
	// Strip transport/runtime identity before abbreviating. Header chips are a
	// glanceable user-facing surface; the namespace is useful for routing and
	// config but noisy in the title bar.
	if strings.Contains(name, "/") {
		parts := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
	}
	for _, prefix := range []string{"llama_server:catalog:", "mistralrs:catalog:"} {
		name = strings.TrimPrefix(name, prefix)
	}
	if i := strings.Index(name, ":catalog:"); i >= 0 {
		name = name[i+len(":catalog:"):]
	}
	// Strip Ollama ":latest" suffix.
	name = strings.TrimSuffix(name, ":latest")
	// Anthropic family — collapse "claude-<family>-<major>-<minor>".
	if strings.HasPrefix(name, "claude-") {
		rest := strings.TrimPrefix(name, "claude-")
		// Split into "<family>-<major>-<minor>[-suffix]".
		parts := strings.Split(rest, "-")
		if len(parts) >= 3 {
			// e.g. ["opus","4","7"] → "opus 4.7"
			return parts[0] + " " + parts[1] + "." + parts[2]
		}
		return rest
	}
	// Google Gemini — turn "gemini-1.5-pro" → "gemini 1.5 pro".
	if strings.HasPrefix(name, "gemini-") {
		return strings.ReplaceAll(name, "-", " ")
	}
	return name
}

// normalizeProgress canonicalises the agent's progress messages for display:
// lowercases the first word, strips trailing "...", and trims whitespace.
// Avoids touching the agent's emission semantics (shared with other clients)
// while still giving the CLI a consistent visual cadence.
func normalizeProgress(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".")
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Lowercase the leading word only (e.g. "Classifying Intent" → "classifying intent").
	// Spare any all-caps tokens past the first space.
	first := []rune(s)
	if first[0] >= 'A' && first[0] <= 'Z' {
		first[0] = first[0] + ('a' - 'A')
	}
	// Lowercase second word if it's a single capital-leading word.
	out := string(first)
	if i := strings.Index(out, " "); i > 0 && i+1 < len(out) {
		r := []rune(out)
		if r[i+1] >= 'A' && r[i+1] <= 'Z' {
			r[i+1] = r[i+1] + ('a' - 'A')
		}
		out = string(r)
	}
	return out
}

// indentBlock prefixes every line of s with pad.
func indentBlock(pad, s string) string {
	if s == "" {
		return pad
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// View renders the full screen at the current width/height. The viewport
// height is computed dynamically here so it absorbs whatever rows the
// suggestion/help line ends up taking — wrapped help text grows downward,
// the viewport shrinks to keep the status bar pinned to terminal bottom.
// composeFrame builds the full ordered slice of screen rows (before overlay
// compositing) and reports inputIdx — the index into parts at which the prompt
// text input begins. It is the single source of truth for the frame layout:
// both View() (for rendering + real-cursor placement) and the mouse hit-test
// (promptTop/mouseInPrompt) derive from it, so "where the input is drawn" and
// "where a click lands on the input" can never diverge. It must stay pure: it
// only reads m and returns freshly built slices.
//
// inputCursorRow(parts, inputIdx) converts inputIdx to an absolute 0-based
// screen row, accounting for embedded newlines and the spare-row padding
// inserted above the prompt when the content does not fill the terminal.
func (m Model) composeFrame() (parts []string, inputIdx int) {
	parts = append(parts, m.renderHeader())
	parts = append(parts, m.styles.BorderDim.Render(strings.Repeat("─", m.width)))
	if m.splashEffective() {
		parts = append(parts, m.splash.View())
		parts = append(parts, "")
	}

	switch {
	case m.content != nil:
		if m.configSurface != nil {
			parts = append(parts, renderConfigTabStrip(m.width, m.configSurface.active, m.configSurface.focused, m.styles))
		}
		parts = append(parts, m.content.View())
	default:
		if m.hasSubAgentTabs() {
			parts = append(parts, m.renderChatTabStrip())
			// A top-border glyph under the strip so the tabs read as real tabs.
			// ▔ (upper one-eighth block) sits at the TOP of its cell so the line
			// hugs the tabs directly above, unlike ─ which centers it in the row.
			// Its row is reserved in scrollbarTop/bodyH below.
			parts = append(parts, m.styles.BorderDim.Render(strings.Repeat("▔", m.width)))
		}
		parts = append(parts, m.renderViewportWithScrollbar())
		if m.recap != "" {
			parts = append(parts, m.renderRecap())
		}
	}

	promptLine := lipgloss.NewStyle().Foreground(m.promptBorderColor).Render(strings.Repeat("─", m.width))
	promptParts := []string{}
	// Queued messages float just above the prompt border on the main chat page.
	// Content pages such as /c should show only their own page body and the prompt;
	// queued chat turns remain pending but do not leak into the page chrome.
	if !m.contentPageActive() {
		if q := m.renderQueued(); q != "" {
			promptParts = append(promptParts, q)
		}
	}
	// The prompt frame (border rules + text input) belongs only on the chat
	// surface. Content pages such as the setup wizard render their own body and
	// keep just the status line beneath it, so gate the frame out for them.
	inputIdx = len(parts) + len(promptParts)
	if !m.contentPageActive() {
		promptParts = append(promptParts, promptLine)
		if hint := m.renderSlashSuggestions(); hint != "" {
			promptParts = append(promptParts, hint)
		}
		inputIdx = len(parts) + len(promptParts)
		promptParts = append(promptParts, m.input.View())
		promptParts = append(promptParts, promptLine)
		if notice := m.visionNotice(); notice != "" {
			promptParts = append(promptParts, notice)
		}
	}
	promptParts = append(promptParts, m.renderStatus())

	spareRows := m.height - countLines(parts) - countLines(promptParts)
	for range spareRows {
		parts = append(parts, "")
		inputIdx++
	}
	parts = append(parts, promptParts...)
	return parts, inputIdx
}

func (m Model) View() tea.View {
	start := time.Now()
	defer func() { m.logSlowView(start) }()

	if m.width == 0 || m.height == 0 {
		v := tea.NewView("")
		v.AltScreen = true
		v.BackgroundColor = m.palette.BgDeep // paint our own bg, not the terminal's
		return v                             // first paint before WindowSizeMsg
	}

	parts, inputIdx := m.composeFrame()

	// Alt-screen safety: pad to exactly m.height lines so resize-to-smaller
	// frames clear the trailing rows the previous larger frame occupied.
	out := strings.Join(parts, "\n")
	rendered := countLines(parts)
	if rendered < m.height {
		out += strings.Repeat("\n", m.height-rendered)
	}
	// Composite floating modals on top of the base frame. Currently only
	// the local-runtime install modal — future overlays (about box, etc.)
	// splice here in z-order.
	if m.openRuntimeModal != nil {
		box := m.openRuntimeModal.View(m.styles, m.palette, m.width, m.height)
		boxW, _ := m.openRuntimeModal.modalDim(m.width, m.height)
		x := (m.width - boxW) / 2
		// Center on the rendered box's line count: the pick-state panel
		// sizes its own height (modalDim returns 0 there), and for the
		// install box countLines equals its fixed height.
		y := (m.height - countLines([]string{box})) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		out = composeOverlay(out, box, x, y)
	}
	if m.claudeLoginModal != nil {
		boxW, boxH := m.claudeLoginModal.modalDim(m.width, m.height)
		x := (m.width - boxW) / 2
		y := (m.height - boxH) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		box := m.claudeLoginModal.View(m.styles, m.palette, m.width, m.height)
		out = composeOverlay(out, box, x, y)
	}
	if m.chatgptLoginModal != nil {
		boxW, boxH := m.chatgptLoginModal.modalDim(m.width, m.height)
		x := (m.width - boxW) / 2
		y := (m.height - boxH) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		box := m.chatgptLoginModal.View(m.styles, m.palette, m.width, m.height)
		out = composeOverlay(out, box, x, y)
	}
	v := tea.NewView(out)
	v.AltScreen = true
	v.BackgroundColor = m.palette.BgDeep // paint our own bg so themes aren't at the mercy of the terminal's
	// Request richer Kitty-protocol key reporting so terminals that support it
	// can deliver Command/Super-modified keys such as Cmd+C to the app-native
	// selection layer. Terminals that reserve Cmd+C for their own copy action
	// simply won't emit a keypress, so ctrl+c/c/enter remain copy fallbacks.
	v.KeyboardEnhancements = tea.KeyboardEnhancements{
		ReportEventTypes:           true,
		ReportAllKeysAsEscapeCodes: true,
		ReportAlternateKeys:        true,
		ReportAssociatedText:       true,
	}
	v.MouseMode = tea.MouseModeCellMotion
	// Drive the real terminal cursor to the input caret position. Only
	// when the chat input owns focus (no overlay, no pending confirm).
	if !m.contentPageActive() && m.pendingConfirm == nil {
		if c := m.input.Cursor(); c != nil {
			c.Y += inputCursorRow(parts, inputIdx)
			v.Cursor = c
		}
	} else {
		v.Cursor = nil // overlays manage their own (virtual) cursor
	}
	return v
}

// countLines totals the visible row count of a slice of pre-joined strings,
// accounting for embedded newlines (e.g. multi-line splash, wrapped help).
func countLines(rows []string) int {
	n := 0
	for _, r := range rows {
		n += strings.Count(r, "\n") + 1
	}
	return n
}

// renderSlashSuggestions renders the line above the input. Two states:
//
//   - Help mode: once the user has typed a recognised command (with or
//     without trailing args), show that command's Help text — gives them
//     the usage signature mid-typing, before they hit Enter.
//
//   - Completion mode: prefix matches against the registry, displayed as a
//     two-tone strip (typed prefix in bright amber, completion tail in cyan)
//     with a `tab to complete` hint.
//
// Returns the empty string when the input isn't a slash command.
func (m Model) renderSlashSuggestions() string {
	val := m.input.Value()
	if !strings.HasPrefix(val, "/") || len(val) < 2 {
		return ""
	}

	// Help mode: the first whitespace-delimited token, if it's a registered
	// command (or alias), overrides completion with the usage signature.
	firstWord := strings.TrimPrefix(val, "/")
	if i := strings.IndexAny(firstWord, " \t"); i >= 0 {
		firstWord = firstWord[:i]
	}
	if cmd, ok := m.registry.Lookup(firstWord); ok && cmd.Help != "" {
		// Wrap help text to terminal width with hanging indent so the
		// continuation lines align under the help text rather than the
		// command name prefix.
		head := "  " + m.styles.Accent.Render("/"+cmd.Name) +
			m.styles.BorderDim.Render("   ·   ")
		prefixW := lipgloss.Width(head)
		wrapW := m.width - prefixW
		if wrapW < 20 {
			wrapW = 20
		}
		wrappedHelp := lipgloss.NewStyle().Width(wrapW).Render(cmd.Help)
		lines := strings.Split(wrappedHelp, "\n")
		for i := range lines {
			styled := m.styles.Muted.Render(lines[i])
			if i == 0 {
				lines[i] = head + styled
			} else {
				lines[i] = strings.Repeat(" ", prefixW) + styled
			}
		}
		return strings.Join(lines, "\n")
	}

	matches := m.registry.PrefixMatches(val)
	if len(matches) == 0 {
		return ""
	}
	typed := strings.TrimPrefix(val, "/")
	var pieces []string
	for _, n := range matches {
		// Color the typed prefix in bright amber so the user sees "this is
		// what you typed"; the completion tail in info-cyan so it reads as
		// "here's what would be added."
		var b strings.Builder
		b.WriteString(m.styles.Bright.Render("/" + n[:len(typed)]))
		if len(n) > len(typed) {
			b.WriteString(m.styles.Info.Render(n[len(typed):]))
		}
		pieces = append(pieces, b.String())
	}
	hint := "  " + strings.Join(pieces, "  ") +
		m.styles.BorderDim.Render("   ·   ") + m.styles.Muted.Render("tab to complete")
	return hint
}

// unstageLastQueued pops the most-recently-queued message back into the prompt
// for editing and drops it from the queue. Returns false when the queue is
// empty so callers can fall through to history recall.
// Images are re-registered via RegisterImage so the existing "[image N]"
// markers in the restored text resolve without inserting duplicate markers.
func (m *Model) unstageLastQueued() bool {
	last, ok := m.mainChat().UnstageLast()
	if !ok {
		return false
	}
	m.input.SetValue(last.text)
	for _, img := range last.images {
		m.input.RegisterImage(int(img.Index), img.Data, img.MediaType, "")
	}
	m.relayout()
	return true
}

// queuedLines builds the rendered rows for the messages queued while a response
// streams — a navy-fill strip just above the prompt, starting at the content
// margin and spanning to the right edge. Each message word-wraps to the strip
// width with a hanging indent so continuation rows align past the "⊕ " marker.
// The marker shows in muted lime; the text in bright amber on the navy fill, so
// the rows read as upcoming user prompts (matching the same palette slot
// designated for echoed user-prompt rows in the scrollback). The layout height
// calc counts these lines, so wrapping and reserved rows stay in sync. Nil when
// nothing is queued.
func (m Model) queuedLines() []string {
	queued := m.mainChat().Queued()
	if len(queued) == 0 {
		return nil
	}
	leftPad := strings.Repeat(" ", entryIndent)
	avail := m.width - entryIndent - 2 // marker "⊕ " takes 2 cells
	if avail < 1 {
		avail = 1
	}
	// Continuation rows hang under the text, aligned past the "⊕ " marker.
	hang := strings.Repeat(" ", 2)
	hint := "↪ [enter] steer"
	hintStyle := lipgloss.NewStyle().Foreground(m.palette.Dim).Background(m.palette.BufferUserBg)
	var lines []string
	for _, q := range queued {
		wrapped := strings.Split(ansi.Wrap(q, avail, ""), "\n")
		for i, w := range wrapped {
			isLast := i == len(wrapped)-1
			marker := m.styles.BufferUserMarker.Render("⊕ ")
			if i > 0 {
				marker = m.styles.BufferUserText.Render(hang)
			}
			text := w
			if isLast {
				lineW := lipgloss.Width(w)
				hintW := lipgloss.Width(hint)
				if lineW+1+hintW <= avail {
					gap := avail - lineW - hintW
					lines = append(lines, leftPad+marker+
						m.styles.BufferUserText.Render(text+strings.Repeat(" ", gap))+
						hintStyle.Render(hint))
					continue
				}
			}
			fill := avail - lipgloss.Width(text)
			if fill < 0 {
				fill = 0
			}
			lines = append(lines, leftPad+marker+
				m.styles.BufferUserText.Render(text+strings.Repeat(" ", fill)))
			if isLast {
				trimmedHint := ansi.Truncate(hint, avail, "…")
				gap := max(0, avail-2-lipgloss.Width(trimmedHint))
				lines = append(lines, leftPad+m.styles.BufferUserText.Render(hang+strings.Repeat(" ", gap))+hintStyle.Render(trimmedHint))
			}
		}
	}
	return lines
}

// renderQueued draws the messages queued while a response streams as a
// navy-fill strip just above the prompt. See queuedLines for the per-row
// layout; long messages wrap (with a hanging indent) rather than truncate.
// Empty when nothing is queued.
func (m Model) renderQueued() string {
	return strings.Join(m.queuedLines(), "\n")
}

// recapLabelText is the leading label on the recap line; its width also sets the
// hanging indent for wrapped continuation lines.
const recapLabelText = "recap "

// recapLines builds the rendered recap line(s): the "recap " label on the first
// line, the living work summary word-wrapped to the terminal width with a
// hanging indent so continuation lines align under the text. Returns nil when
// there's no recap or no room. renderRecap joins these; the layout height calc
// counts them, so wrapping and reserved rows stay in sync.
func (m Model) recapLines() []string {
	if m.recap == "" {
		return nil
	}
	pad := strings.Repeat(" ", entryIndent)
	// Label in lime (upright) to read as a label; the recap text in bright amber
	// italic so the two are visually distinct.
	labelStyle := lipgloss.NewStyle().Foreground(m.palette.Accent)
	textStyle := lipgloss.NewStyle().Foreground(m.palette.Bright).Italic(true)
	avail := m.width - entryIndent - lipgloss.Width(recapLabelText)
	if avail < 8 {
		return nil
	}
	hang := strings.Repeat(" ", lipgloss.Width(recapLabelText))
	wrapped := strings.Split(ansi.Wrap(m.recap, avail, ""), "\n")
	lines := make([]string, 0, len(wrapped))
	for i, w := range wrapped {
		if i == 0 {
			lines = append(lines, pad+labelStyle.Render(recapLabelText)+textStyle.Render(w))
		} else {
			lines = append(lines, pad+hang+textStyle.Render(w))
		}
	}
	return lines
}

// renderRecap draws the living work summary at the bottom of the chat area,
// dimmed and wrapped to terminal width. Only rendered in the default (no-overlay)
// view.
func (m Model) renderRecap() string {
	lines := m.recapLines()
	if len(lines) == 0 {
		return ""
	}
	// Blank line above for breathing room, then the wrapped recap.
	return "\n" + strings.Join(lines, "\n")
}

// renderViewportWithScrollbar renders the chat viewport with a one-column
// vertical scrollbar on its right edge. Delegates to chatView.View.
func (m Model) renderViewportWithScrollbar() string {
	return m.renderViewportWithTaskPane()
}

func headerTextWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func headerDisplayWidth(s string) int {
	return ansi.StringWidth(ansi.Strip(s))
}

func (m Model) renderHeaderRight(maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	cloudChip := m.cloudModel
	if cloudChip == "" {
		cloudChip = m.activeCloudProfile
	}
	cloudLabel := ""
	if cloudChip != "" {
		cloudLabel = abbreviateModel(cloudChip)
	}
	openChip := m.openModel
	openChipStyle := m.styles.Accent
	if m.openRuntimeStatus != nil && m.openRuntimeStatus.Downloading {
		// The top-bar o: chip is the primary local-runtime state surface. If the
		// configured open model is actively downloading, say so here; do not show
		// the last served model (which may be a cloud profile such as
		// openai-responses).
		openChip = "downloading"
		openChipStyle = m.styles.BorderDim
	}
	if openChip == "" {
		openChip = m.lastModel
	}
	openLabel := abbreviateModel(openChip)

	build := func() string {
		pieces := []string{}
		if cloudLabel != "" {
			pieces = append(pieces,
				m.styles.Info.Render("c:"),
				m.styles.Accent.Render(cloudLabel),
				m.styles.BorderDim.Render(" │ "),
			)
		}
		pieces = append(pieces,
			m.styles.Info.Render("o:"),
			openChipStyle.Render(openLabel),
		)
		return lipgloss.JoinHorizontal(lipgloss.Left, pieces...)
	}

	right := build()
	for headerDisplayWidth(right) > maxWidth {
		if lipgloss.Width(openLabel) > 1 {
			openLabel = ansi.Truncate(openLabel, lipgloss.Width(openLabel)-1, "…")
		} else if lipgloss.Width(cloudLabel) > 1 {
			cloudLabel = ansi.Truncate(cloudLabel, lipgloss.Width(cloudLabel)-1, "…")
		} else {
			break
		}
		right = build()
	}
	if headerDisplayWidth(right) > maxWidth {
		return ansi.Truncate(right, maxWidth, "…")
	}
	return right
}

func (m Model) headerTitleRange() (int, int, bool) {
	if m.sessionTitle == "" || m.width <= 0 {
		return 0, 0, false
	}
	leftW := headerTextWidth("▓▓ CERCANO v0.1.0")
	titlePlain := "░▒▓ " + m.sessionTitle + " ▓▒░"
	titleW := headerTextWidth(titlePlain)
	titleStart := (m.width - titleW) / 2
	gapBefore := titleStart - leftW
	if gapBefore < 2 {
		gapBefore = 2
	}
	start := leftW + gapBefore + headerTextWidth("░▒▓ ")
	end := start + headerTextWidth(m.sessionTitle)
	return start, end, start < end
}

func (m Model) mouseInHeaderTitle(x, y int) bool {
	start, end, ok := m.headerTitleRange()
	return ok && y == 0 && x >= start && x <= end
}

func (m *Model) beginHeaderSelection(x int) {
	start, end, ok := m.headerTitleRange()
	if !ok {
		return
	}
	pt := selectionPoint{Line: 0, Col: clampInt(x, start, end)}
	m.headerSelection = textSelection{Active: true, Dragging: true, Anchor: pt, Cursor: pt}
}

func (m *Model) updateHeaderSelection(x int) {
	if !m.headerSelection.Active {
		return
	}
	start, end, ok := m.headerTitleRange()
	if !ok {
		m.headerSelection = textSelection{}
		return
	}
	m.headerSelection.Cursor = selectionPoint{Line: 0, Col: clampInt(x, start, end)}
}

func (m Model) selectedHeaderText() string {
	if !m.headerSelection.hasRange() {
		return ""
	}
	plain := ansi.Strip(m.renderHeader())
	start, end := m.headerSelection.ordered()
	return ansi.Cut(plain, start.Col, end.Col)
}

func (m Model) renderHeader() string {
	// Three regions:
	//   left   — brand + version, anchored at column 0
	//   center — session title (centered across full width when present)
	//   right  — cloud + local model strip, anchored at the right edge
	//
	// With the title centered, the left and right slots stay symmetric so
	// the bar reads cleanly even on wide terminals.
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		m.styles.Primary.Render("▓▓ CERCANO"),
		m.styles.Muted.Render(" v0.1.0"),
	)

	leftW := headerTextWidth("▓▓ CERCANO v0.1.0")

	// No title — flush left and right to the edges with a single gap.
	if m.sessionTitle == "" {
		right := m.renderHeaderRight(m.width - leftW - 1)
		rightW := headerDisplayWidth(right)
		gap := m.width - leftW - rightW
		if gap < 1 {
			gap = 1
		}
		return m.renderHeaderSelection(left + strings.Repeat(" ", gap) + right)
	}

	titlePlain := "░▒▓ " + m.sessionTitle + " ▓▒░"
	title := m.styles.Info.Render("░▒▓ ") +
		m.styles.Info.Render(m.sessionTitle) +
		m.styles.Info.Render(" ▓▒░")
	titleW := headerTextWidth(titlePlain)

	// Center the title across the full bar.
	titleStart := (m.width - titleW) / 2
	gapBefore := titleStart - leftW
	if gapBefore < 2 {
		gapBefore = 2 // collision guard with the brand
	}
	titleEnd := leftW + gapBefore + titleW
	right := m.renderHeaderRight(m.width - titleEnd - 2)
	rightW := headerDisplayWidth(right)
	gapAfter := m.width - rightW - titleEnd
	if gapAfter < 2 {
		gapAfter = 2 // collision guard with the model strip
	}
	return m.renderHeaderSelection(left +
		strings.Repeat(" ", gapBefore) +
		title +
		strings.Repeat(" ", gapAfter) +
		right)
}

func (m Model) renderHeaderSelection(line string) string {
	start, end, ok := m.headerSelection.lineRange(0, m.width)
	if !ok {
		return line
	}
	return highlightRange(line, start, end, theme.SelectionBackgroundSGR(m.palette))
}

func (m Model) renderStatus() string {
	// The footer stays put during a turn — the live turn status renders inline on
	// the assistant placeholder, not here.
	help := m.styles.Muted.Render("/help for cmds")
	if m.activeChat().SelectionHasRange() {
		if m.selectionNotice != "" {
			help = m.styles.Success.Render(m.selectionNotice) +
				m.styles.Muted.Render("  ") +
				m.styles.Accent.Render("esc") +
				m.styles.Muted.Render(" clear")
		} else {
			help = m.styles.Info.Render("selection: ") +
				m.styles.Accent.Render("c") +
				m.styles.Muted.Render(" copy ") +
				m.styles.Accent.Render("esc") +
				m.styles.Muted.Render(" clear")
		}
	} else if m.selectionNotice != "" {
		help = m.styles.Success.Render(m.selectionNotice)
	}
	cloudPart := ""
	switch m.cloudState {
	case "NONE":
		cloudPart = m.statusDivider() + m.styles.Muted.Render("cloud:") + m.styles.Error.Render(" NONE")
	case "ok":
		cloudPart = m.statusDivider() + m.styles.Muted.Render("cloud:") + m.styles.Success.Render(" ok")
	}
	// Show the token counter only once a turn has completed — no "0↑/0↓" on a
	// fresh session — and label it "last turn" since it's the prior turn's total.
	turnPart := ""
	if m.hadTurn {
		turnPart = m.statusDivider() +
			m.styles.Muted.Render(fmt.Sprintf("last turn %d↑/%d↓", m.tokIn, m.tokOut))
	}
	parts := []string{
		m.renderContextMeter(),
		turnPart,
		cloudPart,
		m.renderConnStateChip(),
		m.renderPermissionModeChip(),
		m.renderDevChip(),
		m.statusDivider(),
		help,
	}
	return lipgloss.NewStyle().Width(m.width).Render(strings.Join(parts, ""))
}

// renderConnStateChip surfaces gRPC transport health. Amber "reconnecting
// (N/3)…" while the SDK's reconnect loop is retrying; red "agent
// unreachable" when the loop has given up. Empty (silent) when the
// connection is healthy so the status bar isn't cluttered by the common
// case.
func (m Model) renderConnStateChip() string {
	switch m.connState {
	case agentclient.ConnStateReconnecting:
		label := fmt.Sprintf("⚠ agent reconnecting (%d/%d)…", m.connAttempt, agentclient.FastReconnectAttempts)
		if m.connAttempt > agentclient.FastReconnectAttempts {
			// Past the fast burst: the SDK is in its indefinite slow
			// lane, so a bounded "(N/3)" would be a lie.
			label = fmt.Sprintf("⚠ agent down — retrying every 10s (attempt %d)…", m.connAttempt)
		}
		return m.statusDivider() + m.styles.Primary.Render(label)
	case agentclient.ConnStateFailed:
		return m.statusDivider() + m.styles.Error.Render("✕ agent unreachable")
	default:
		return ""
	}
}

// renderPermissionModeChip renders the session-mode chip for the status bar.
// It carries two orthogonal axes, pipe-separated when both are present:
//
//		mode: autonomous | bypass
//
//	  - The capability profile ("planning" or "autonomous") shows only when a
//	    non-default profile is active for this conversation. It is colored with
//	    the calm accent — the profile is a posture, not a danger level.
//	  - The permission mode is colored by how safe it is: strict → green (most
//	    gated), permissive → amber, bypass → red (least gated / unsafe).
//
// The profile is rendered first because it is the more consequential state to
// notice. Returns "" only when neither axis is known yet (the startup fetch
// hasn't landed and no profile is active) so the bar doesn't show a misleading
// default.
func (m Model) renderPermissionModeChip() string {
	profileLabel := ""
	switch m.sessionProfile {
	case "plan":
		profileLabel = "planning"
	case "autonomous":
		profileLabel = "autonomous"
	}
	if m.permissionMode == "" && profileLabel == "" {
		return ""
	}

	var val string
	if profileLabel != "" {
		val = m.styles.Accent.Render(profileLabel)
	}
	if m.permissionMode != "" {
		var permStyle lipgloss.Style
		switch m.permissionMode {
		case "strict":
			permStyle = m.styles.Success
		case "bypass":
			permStyle = m.styles.Error
		default: // permissive (or anything unexpected) → amber
			permStyle = m.styles.Primary
		}
		perm := permStyle.Render(m.permissionMode)
		if profileLabel != "" {
			val += m.styles.Muted.Render(" | ") + perm
		} else {
			val = perm
		}
	}

	return m.statusDivider() +
		m.styles.Muted.Render("mode:") +
		" " + val
}

func (m Model) statusDivider() string {
	return m.styles.Muted.Render(" · ")
}

// renderDevChip shows a lime DEV marker while the /d workDir override is
// active, so it stays visible that tools are pointed at the Cercano repo.
func (m Model) renderDevChip() string {
	if m.workDirOverride == "" {
		return ""
	}
	return m.statusDivider() + m.styles.Accent.Render("DEV")
}

func (m Model) renderContextMeter() string {
	// cumIn now carries the agent-reported cumulative used (set by
	// ctxUsageMsg) rather than per-turn input. Fall back to cumIn+cumOut
	// before the first usage RPC has returned.
	used := m.ctxEstimatedRequest
	if used == 0 {
		used = m.cumIn
	}
	if used == 0 {
		used = m.cumIn + m.cumOut
	}
	max := m.modelMaxTokens
	if max <= 0 {
		max = 1
	}
	pct := float64(used) / float64(max)
	if pct > 1 {
		pct = 1
	}
	const cells = 20
	fillN := int(pct * float64(cells))
	// During a compaction pass the "compacting…" label is overlaid on the
	// bar itself. Cells outside the label render normally — lime→white
	// shimmer █ for fill, dim ░ for empty. Cells under the label render their
	// letter with per-cell contrast: dark glyph on the shimmer color when
	// over the filled portion, shimmer color as the letter's foreground when
	// over the empty portion — so the sweep animates across both halves of
	// the label and the bar's fill ratio still reads through.
	var bar string
	if m.compacting {
		bar = m.renderCompactingMeterBar(cells, fillN)
	} else {
		bar = m.styles.MeterFill.Render(strings.Repeat("█", fillN)) +
			m.styles.MeterEmpty.Render(strings.Repeat("░", cells-fillN))
	}
	pctStyle := m.styles.Muted
	switch {
	case pct >= 0.9:
		pctStyle = m.styles.Error
	case pct >= 0.7:
		pctStyle = m.styles.Warn
	}
	// The savings badge shows whenever ctxRaw > used. During a compaction
	// pass `used` drops live, so the percentage grows in real time alongside
	// the overlaid "compacting…" label on the bar above.
	badge := ""
	messageUsed := m.ctxMessageTokens
	if messageUsed == 0 {
		messageUsed = m.cumIn
	}
	if m.ctxRaw > messageUsed && messageUsed > 0 {
		saved := int(100 * (1 - float64(messageUsed)/float64(m.ctxRaw)))
		badge = m.statusDivider() + m.styles.Muted.Render(fmt.Sprintf("▣ %d%%↓", saved))
	}
	if m.ctxEstimatedRequest > 0 {
		badge += m.statusDivider() + m.styles.Muted.Render(fmt.Sprintf("msg %s", formatTokens(messageUsed)))
	}
	if m.modelMaxTokens > 0 && !m.ctxWindowKnown {
		badge += m.statusDivider() + m.styles.Muted.Render("window est")
	}
	return strings.Join([]string{
		m.styles.Muted.Render("ctx "),
		bar,
		m.styles.Muted.Render(" "),
		m.styles.Accent.Render(formatTokens(used)),
		m.styles.BorderDim.Render("/"),
		m.styles.Muted.Render(formatTokens(max)),
		m.styles.Muted.Render(" "),
		pctStyle.Render(fmt.Sprintf("%d%%", int(pct*100))),
		badge,
	}, "")
}

// renderCompactingMeterBar paints the 20-cell context meter with the
// "compacting…" label overlaid on the bar. Cells outside the label render the
// usual shimmer █ (fill) / dim ░ (empty). Cells UNDER the label render the
// label letter colored as if it were the bar at that position: the shimmer
// fill color for letters over the filled portion, the dim empty color for
// letters over the empty portion. The fill boundary reads through the label
// as a brightness transition across the letters — e.g., `co` bright vs.
// `mpacting…` dim — so the user still sees how full the bar is. Both halves
// animate via the same wall-clock lime→white sweep used elsewhere.
func (m Model) renderCompactingMeterBar(cells, fillN int) string {
	const (
		cycleMs = 1500
		tail    = 4.0
		padCols = 4.0
	)
	label := []rune("compacting…")
	labelLen := len(label)
	start := (cells - labelLen) / 2
	if start < 0 {
		start = 0
	}
	end := start + labelLen
	if end > cells {
		end = cells
	}
	phaseMs := time.Now().UnixMilli() % int64(cycleMs)
	progress := float64(phaseMs) / float64(cycleMs)
	sweepPos := -padCols + progress*(float64(cells)+2*padCols)

	var b strings.Builder
	for col := 0; col < cells; col++ {
		inLabel := col >= start && col < end
		onFill := col < fillN
		switch {
		case inLabel && onFill:
			// The bar must stay visible under the label: the filled cell keeps
			// its (animated) fill color as the BACKGROUND and the letter is
			// knocked out against it. Light themes need dark ink here; using the
			// pale terminal background as the glyph color washed the label out.
			fill := theme.ActivityColorAt(m.palette, col, sweepPos, tail)
			fg := theme.MeterLabelForeground(m.palette, fill, true)
			b.WriteString(lipgloss.NewStyle().
				Foreground(fg).
				Background(fill).
				Render(string(label[col-start])))
		case inLabel && !onFill:
			// Empty-side letters are an overlay on the checker. On light themes,
			// flip the glyph to the page color over a darkened checker so the
			// label stays readable instead of becoming amber-on-brown mush.
			emptyBg := theme.Fade(m.palette.Dim, 0.45)
			fg := theme.MeterLabelForeground(m.palette, emptyBg, false)
			b.WriteString(lipgloss.NewStyle().
				Foreground(fg).
				Background(emptyBg).
				Render(string(label[col-start])))
		case !inLabel && onFill:
			// Background-paint (space glyph), not a █ foreground glyph: the
			// label cells are background-painted, and a font's full-block
			// glyph doesn't flood the whole cell the way a background color
			// does — mixing the two makes the label cells look taller than
			// the rest of the bar.
			b.WriteString(lipgloss.NewStyle().
				Background(theme.ActivityColorAt(m.palette, col, sweepPos, tail)).
				Render(" "))
		default: // !inLabel && !onFill
			b.WriteString(m.styles.MeterEmpty.Render("░"))
		}
	}
	return b.String()
}

// formatTokens renders 21400 → "21.4k", 1247 → "1.2k", 412 → "412".
func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// QuitAfter exposes a tea.Cmd for the main package to fire if it wants to wrap
// startup with a confirmation. Currently unused; reserved for future.
func QuitAfter() tea.Cmd { return tea.Quit }
