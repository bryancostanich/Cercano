// Package ui hosts the Bubble Tea root model for cercano-cli.
package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
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
	// Status is the current pre-stream progress note (e.g. "classifying
	// intent", "selecting provider", "generating response"). Set by
	// progress messages while Content is empty; shown in place of the
	// "thinking…" placeholder. Cleared as soon as tokens start arriving.
	Status string

	// Tool, when non-nil, makes this entry a tool-call line — Role/Content
	// are ignored and renderToolEntry produces the visible row. expand /
	// collapse via tab-focus is a follow-up; V1 renders folded.
	Tool *ToolEntry
}

// Model is the Bubble Tea root model.
type Model struct {
	width, height int

	// scrollbarTop is the absolute screen row of the viewport's first line,
	// used to hit-test scrollbar mouse events. Set in relayout().
	scrollbarTop int

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

	splashShown bool // hide after first user input
	splash      banner.AnimModel
	chat        chatView
	selectionNotice    string
	input              promptInput

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

	tokIn, tokOut  int
	cumIn, cumOut  int
	ctxRaw         int
	compacting     bool
	ctxPollTicks   int
	ctxPolling     bool // a ctxUsageTick loop is currently running (avoid double-scheduling)
	animTickActive bool // a progressAnimTick loop is currently running (avoid double-scheduling)
	lastLatencyMs  int
	modelMaxTokens int
	lastModel      string // local model name (from config)
	cloudModel     string // cloud model name (from config); empty when no cloud configured
	cloudState     string // "" = unknown, "NONE" = absent, "ok" = real cloud configured
	ctrlCArmed     bool   // first ctrl-c on empty input arms quit; any other key disarms
	errMsg         string

	// Live turn telemetry, surfaced by renderStatus while a turn streams. Reset
	// when a turn begins; the engine fields fill in on the RouteSelected event.
	turnStart    time.Time // wall clock when streaming began (for elapsed)
	turnActivity string    // current verb: thinking → routing → running <tool> → writing
	turnTokOut   int       // output tokens seen so far this turn (approximate, live)
	turnModel    string    // engine handling the turn (from RouteSelected)
	turnCloud    bool      // true when the turn routed to a cloud engine
	hadTurn      bool      // a turn has completed; gate the idle token counter

	content contentPage

	recap string // living one-line work summary; shown in the chat footer

	// convRef shares the current convID with the slash registry by reference,
	// so /rename always targets whatever conversation the model currently has
	// active (including after /resume).
	convRef *struct{ id string }

	openHistoryOnStart bool // -r flag → open the history picker after first WindowSizeMsg

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

	// toolCache is the registry of available tools, fetched at startup so
	// the CLI can decide locally (no extra RPC) whether to prompt before
	// invoking a tool. Keyed by tool name.
	toolCache map[string]agentclient.ToolInfo

	// pendingConfirm carries a pending confirmation gate waiting on a y/n/esc
	// (and optional extra) keypress. While non-nil, all key events route to
	// the confirm resolver instead of the input or scrollback.
	pendingConfirm *confirmRequest

	// permissionMode caches the agent's current session permission mode
	// ("strict" | "permissive" | "bypass") so the status bar can render a
	// colored chip without an RPC round-trip every frame. Updated by the
	// startup fetch (permissionModeMsg) and by the /strict /permissive
	// /bypass /mode slash handlers.
	permissionMode string

	// supportsVision is true when the active provider can accept image
	// inputs. Fetched once at startup via fetchVisionCmd. Used by
	// visionNotice to show a dim warning when images are attached but the
	// model can't see them. Interim until capability-aware routing lands.
	supportsVision bool

	// meridianStatus is the latest snapshot of the local Meridian proxy
	// pushed by the agent over SubscribeEvents. nil until the first event
	// (or initial GetCloudProfiles populates it). The status bar reads this
	// to decide whether to render a "Sign in to Claude" chip or other
	// setup hints.
	meridianStatus *agentclient.MeridianStatus

	// localRuntimeStatus is the most recent LocalRuntimeStatusChanged event
	// from the agent — populated on a config-driven runtime swap when the
	// server's headless detection can't find the binary or a model. nil
	// (or ok=true) means the chip stays hidden and the install modal is
	// not offered.
	localRuntimeStatus *agentclient.LocalRuntimeStatus

	// localRuntimeModal is the open install-modal state (nil = closed). It
	// walks a small state machine — idle → running → done|failed — driven
	// by InstallLocalRuntime stream events.
	localRuntimeModal *localRuntimeInstallModal

	// pendingRuntimeSwitch, when non-empty, is a runtime id ("llama_server")
	// whose UpdateConfig(local-runtime=...) call is queued to fire once the
	// install-modal reports success. Set by openLocalRuntimeInstallModalMsg
	// (emitted by the settings page when the user picks a runtime that
	// isn't ready), cleared by every modal-close path (dispatched on
	// success, dropped on cancel/failed).
	pendingRuntimeSwitch string
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
	onYes  func(Model) (Model, tea.Cmd)
	onNo   func(Model) (Model, tea.Cmd)
	extras map[string]func(Model) (Model, tea.Cmd)
}

const defaultInputPlaceholder = "type a message, /help for commands"
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
	slash.RegisterContext(reg)
	slash.RegisterTools(reg, ag)
	slash.RegisterMcp(reg, ag)
	slash.RegisterPermissions(reg, ag)
	// currentConv is captured by reference so it always returns the active
	// conversation id even after /resume swaps it.
	convRef := &struct{ id string }{}
	slash.RegisterHistory(reg, ag, func() string { return convRef.id })
	slash.RegisterRuntime(reg)
	slash.RegisterLocus(reg, ag)
	slash.RegisterContextView(reg)
	slash.RegisterSettings(reg)
	slash.RegisterTheme(reg)

	splash := banner.NewAnimModel(p, banner.Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		Model:   "qwen3-coder",
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
		chat:               newChatView(s, p, root, home, 80, 10),
		agent:              ag,
		convID:             initialConvID,
		convRef:            convRef,
		registry:           reg,
		splashShown:        !openHistoryOnStart,
		splash:             splash,
		input:              ti,
		lastModel:          "qwen3-coder",
		modelMaxTokens:     128_000, // placeholder until the agent serves real ctx limits
		openHistoryOnStart: openHistoryOnStart,
		promptBorderColor:  p.Accent,
		promptColorToken:   "palette:accent",
	}
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
	m.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: doc})
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
	m.chat.SetStyles(m.styles, m.palette)
	m.applyInputStyles()
	m.promptBorderColor = m.resolvePromptColor(m.promptColorToken)
	if sp, ok := m.content.(*settingsPage); ok {
		sp.SetStyles(m.styles, m.palette)
	}
	m.refreshViewport()
}

// Init is called by Bubble Tea once at startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), m.splash.Init(), fetchConfigCmd(m.agent), fetchToolsCmd(m.agent), fetchPermissionModeCmd(m.agent), fetchLocalRuntimeStatusCmd(m.agent), fetchVisionCmd(m.agent), subscribeEventsCmd(m.agent))
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

// fetchLocalRuntimeStatusCmd asks the agent for the current local-runtime
// detection snapshot. Used on CLI startup so the chip appears immediately
// when the user is running with a partially-configured runtime (e.g. they
// edited local_runtime: llama_server in the yaml but never installed the
// binary). Push events cover subsequent state changes.
func fetchLocalRuntimeStatusCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		st, err := ag.GetLocalRuntimeStatus(ctx, "")
		if err != nil || st == nil {
			return nil
		}
		// Reuse the change-msg shape so the Update handler for it can
		// cover both initial-fetch and push-event paths uniformly. next
		// is nil — this is a one-shot, not a stream drain.
		return localRuntimeStatusChangedMsg{status: st}
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
	LocalModel      string
	CloudModel      string
	CloudConfigured bool
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
		// Treat "cloud configured" as: provider AND (api-key OR base-url) are
		// set. Otherwise we'd show a cloud model name that the agent will
		// never actually route to.
		configured := cfg.CloudProvider != "" && (cfg.CloudAPIKeySet || cfg.CloudBaseURL != "")
		return configLoadedMsg{
			LocalModel:      cfg.LocalModel,
			CloudModel:      cfg.CloudModel,
			CloudConfigured: configured,
		}
	}
}

// streamEndMsg signals the StreamChat channel closed (turn complete). Emitted
// by the mainAgentDriver's drain cmd on channel close.
type streamEndMsg struct{}

// ctxUsageMsg carries the result of an asynchronous GetContextUsage call.
type ctxUsageMsg struct {
	Used, Max  int
	Percent    float64
	Raw        int
	Compacting bool
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
				Used: u.TokensUsed, Max: u.ModelMax, Percent: u.Percent,
				Raw: u.RawTokens, Compacting: u.Compacting,
			}
	}
}

type recapLoadedMsg struct{ recap string }

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
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		// Propagate the new size to any open overlay so it re-renders at
		// the correct dimensions on resize. Without this, the overlay keeps
		// drawing at its construction-time width/height and the buffer
		// fragments.
		if m.content != nil {
			m.content.SetSize(m.width, m.height)
		}
		// -r boot: open the history picker on the first sized frame.
		if m.openHistoryOnStart && m.width > 0 {
			m.openHistoryOnStart = false
			hv, _ := newHistoryView(m.agent, m.palette, m.styles, m.width, m.height)
			m.content = hv
		}
		// Force a full alt-screen redraw on resize. Without ClearScreen,
		// rows in the terminal that were occupied at the OLD size but not
		// rewritten at the NEW size show stale content.
		return m, tea.ClearScreen

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
		if m.pendingConfirm != nil {
			return m, nil
		}
		if m.chat.SelectionDragging() {
			return m, nil
		}
		mouse := msg.Mouse()
		if m.mouseInPrompt(mouse) {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.input.ScrollView(-promptWheelDelta)
			case tea.MouseWheelDown:
				m.input.ScrollView(promptWheelDelta)
			}
			return m, nil
		}
		cmd := m.chat.Update(msg)
		return m, cmd

	case tea.MouseClickMsg:
		if m.pendingConfirm != nil {
			return m, nil
		}
		mouse := msg.Mouse()
		if m.contentPageActive() {
			if mouse.Button != tea.MouseLeft {
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
		if m.mouseInPrompt(mouse) {
			m.chat.ClearSelection()
			m.input.MouseDown(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		// Translate screen coords to viewport-local and forward to chatView.
		m.chat.MouseDown(mouse.X, mouse.Y-m.scrollbarTop)
		return m, nil

	case tea.MouseMotionMsg:
		if m.contentPageActive() {
			mouse := msg.Mouse()
			if m.contentScrollbarDragging {
				if scroller, state, ok := m.activeContentScroller(); ok {
					scroller.ScrollTo(scrollOffsetFromClick(mouse.Y, m.contentTop(), state.Height, state.Total))
				}
			}
			return m, nil
		}
		if m.pendingConfirm != nil {
			m.chat.StopScrollbarDrag()
			m.chat.ClearSelectionDrag()
			m.input.CancelDrag()
			return m, nil
		}
		mouse := msg.Mouse()
		if m.input.Dragging() {
			m.input.MouseDrag(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		// MouseDrag checks scrollbarDragging first (priority over text selection)
		// then falls through to the selection extend path.
		cmd := m.chat.MouseDrag(mouse.X, mouse.Y-m.scrollbarTop)
		return m, cmd

	case tea.MouseReleaseMsg:
		if m.contentPageActive() {
			m.contentScrollbarDragging = false
			return m, nil
		}
		mouse := msg.Mouse()
		if m.input.Dragging() {
			m.input.MouseUp(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		cmd, copied := m.chat.MouseUp(mouse.X, mouse.Y-m.scrollbarTop)
		if copied {
			m.selectionNotice = "copied selection"
		}
		return m, cmd

	case tea.KeyboardEnhancementsMsg:
		return m, nil

	case tea.PasteMsg:
		if m.contentPageActive() || m.pendingConfirm != nil {
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
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != prevVal {
			m.relayout()
		}
		return m, cmd

	case tea.KeyPressMsg:
		// Pending confirm gates ALL keys — until the user resolves it, the
		// input, scrollback, and any in-flight slash commands stay dormant.
		if m.pendingConfirm != nil {
			next, cmd := m.resolveConfirmKey(msg.String())
			return next, cmd
		}
		keyStr := msg.String()
		if keyStr == "ctrl+c" {
			next, cmd := m.handleCtrlCKey(msg)
			return next, cmd
		}
		if m.ctrlCArmed {
			m.ctrlCArmed = false
			m.input.Placeholder = defaultInputPlaceholder
		}
		// The local-runtime install modal takes precedence over every
		// other surface — it's a floating overlay, so its keys must be
		// consumed before content pages or the input see them.
		if m.localRuntimeModal != nil {
			next, cmd := m.handleLocalRuntimeModalKey(msg)
			return next, cmd
		}
		// F1 opens the install modal when the chip is showing. Global so
		// it works whether the user is on chat, settings, or any content
		// page. If no unresolved local-runtime setup is queued, F1 is a
		// no-op (falls through).
		if keyStr == "f1" && m.localRuntimeStatus != nil && !m.localRuntimeStatus.Ok {
			m.localRuntimeModal = newLocalRuntimeInstallModal(*m.localRuntimeStatus)
			return m, nil
		}
		// Active content pages own the middle region, but global keys stay
		// above this branch.
		if m.content != nil {
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
			}
			return m, cmd
		}
		if isRuntimeDashboardKey(msg) {
			dashboard, cmd := newRuntimeDashboard(m.agent, m.palette, m.styles, m.width, m.height)
			m.content = dashboard
			if dashboard.hasActiveDownloads() {
				cmd = tea.Batch(cmd, runtimeDashboardRefreshTick())
			}
			return m, cmd
		}
		// Esc cancels an in-flight prompt execution.
		if m.streaming && key.Matches(msg, keys.Back) {
			m.cancelCurrentStream()
			return m, nil
		}
		if m.chat.SelectionActive() {
			cmd, handled, copied := m.chat.HandleSelectionKey(msg)
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
		if m.chat.InToolNav() {
			switch {
			case key.Matches(msg, keys.NavUp):
				m.chat.NavPrev()
				m.refreshViewport()
				return m, nil
			case key.Matches(msg, keys.NavDown):
				m.chat.NavNext()
				m.refreshViewport()
				return m, nil
			case key.Matches(msg, keys.ToggleTool):
				m.chat.ToggleFocusedFold()
				m.refreshViewport()
				return m, nil
			case key.Matches(msg, keys.Back):
				m.chat.ExitToolNav()
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
			if m.chat.EnterToolNav() {
				m.refreshViewport()
				return m, nil
			}
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
			cmd := m.chat.Update(msg)
			return m, cmd
		}
		unmodifiedArrow := msg.Key().Mod == 0
		switch keyStr {
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			images := promptImagesToInline(m.input.Attachments())
			m.input.SetValue("")
			m.splashShown = false
			// Slash commands are local navigation / UI actions, never sent to
			// the model — bypass the mid-stream queue so /c, /m, /rename etc.
			// take effect immediately even while a turn is in flight.
			isSlash := strings.HasPrefix(text, "/")
			// Submitting a non-slash mid-stream queues the message instead of
			// starting a second turn; it sends when the current stream completes.
			if m.streaming && !isSlash {
				m.chat.Enqueue(text, images)
				m.relayout()
				return m, nil
			}
			// Reset the input back to one line (and reclaim any splash rows).
			m.relayout()
			return m.submit(text, images)
		case "shift+enter":
			// Insert a hard newline for multi-line composing; relayout so the
			// input grows by a row (up to the cap).
			m.input.InsertString("\n")
			m.relayout()
			return m, nil
		case "up":
			// On an empty prompt, ↑ first unstages the most-recently-queued
			// message for editing (takes priority over history).
			if unmodifiedArrow && m.input.Value() == "" && m.input.Line() == 0 && m.unstageLastQueued() {
				return m, nil
			}
			// On the first line, ↑ recalls the previous submitted input (shell
			// style); otherwise it falls through to move the cursor up.
			if unmodifiedArrow && m.input.Line() == 0 && m.recallHistoryPrev() {
				return m, nil
			}
		case "down":
			// On the last line, ↓ steps forward through history; otherwise it
			// falls through to move the cursor down.
			if unmodifiedArrow && m.input.Line() == m.input.LineCount()-1 && m.recallHistoryNext() {
				return m, nil
			}
		}
		var cmd tea.Cmd
		prevVal := m.input.Value()
		m.input, cmd = m.input.Update(msg)
		// Recompute layout if the input changed shape (suggestion line
		// height depends on the input value). Cheap when nothing changed.
		if m.input.Value() != prevVal {
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

	case chatStreamMsg:
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
		case chatProgressMsg:
			m.turnActivity = "routing"
			m.chat.Apply(ev)
		case chatAssistantDeltaMsg:
			m.turnActivity = "writing"
			m.turnTokOut++ // one delta ≈ one token (approximate live count)
			m.chat.Apply(ev)
		case toolEntryStartMsg:
			m.turnActivity = "running " + ev.name
			m.chat.Apply(ev)
			// Kick the spinner animation loop if it isn't already running
			// — the placeholder loop may have stopped once tokens began
			// streaming, leaving the in-progress tool line without ticks.
			if !m.animTickActive {
				m.animTickActive = true
				return m, tea.Batch(msg.next, progressAnimTick())
			}
		case chatDoneMsg:
			m.applyTurnTelemetry(ev) // footer fields
			m.chat.Apply(ev)         // transcript finalize + notice
		case permissionRequiredMsg:
			tc := &pendingToolCall{
				ToolUseID:   ev.id,
				Name:        ev.name,
				Args:        ev.argsJSON,
				Permission:  ev.tier,
				Destructive: ev.destructive,
			}
			m.pendingConfirm = toolConfirm(tc)
			m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: m.renderConfirmPrompt(tc)})
		default:
			// Remaining transcript events (tool stop/exec-start/exec-complete,
			// error) carry no turn-telemetry side effect.
			m.chat.Apply(ev)
		}
		m.refreshViewport()
		return m, msg.next

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
		wasCompacting := m.compacting
		m.compacting = msg.Compacting
		// Kick the per-frame animation loop when a pass starts.
		var cmd tea.Cmd
		if m.compacting && !wasCompacting {
			cmd = progressAnimTick()
		}
		return m, cmd

	case recapLoadedMsg:
		// When the recap's presence toggles, the recap line claims (or frees) a
		// row below the viewport. relayout() must re-run so the viewport resizes
		// and the status bar stays pinned — otherwise the new line pushes the
		// footer off-screen until the next resize.
		had := m.recap != ""
		m.recap = msg.recap
		if had != (m.recap != "") {
			m.relayout()
		}
		return m, nil

	case configLoadedMsg:
		if msg.LocalModel != "" {
			m.lastModel = msg.LocalModel
		}
		if msg.CloudConfigured {
			m.cloudModel = msg.CloudModel
		} else {
			m.cloudModel = ""
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

	case meridianStatusChangedMsg:
		// Pushed by the agent on every Meridian proxy state transition.
		// Cache for the status bar and re-arm the drain loop.
		m.meridianStatus = msg.status
		return m, msg.next

	case runtimeInstallStartedMsg:
		if m.localRuntimeModal == nil {
			// User closed the modal between Enter and stream open — cancel
			// the pending RPC and drop everything.
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.localRuntimeModal.setFailed(msg.err.Error())
			return m, nil
		}
		m.localRuntimeModal.cancel = msg.cancel
		return m, msg.next

	case runtimeInstallProgressMsg:
		if m.localRuntimeModal == nil {
			return m, nil // modal closed; discard remaining frames
		}
		m.localRuntimeModal.appendLog(msg.line)
		return m, msg.next

	case runtimeInstallDoneMsg:
		if m.localRuntimeModal == nil {
			return m, nil
		}
		m.localRuntimeModal.cancel = nil
		switch {
		case msg.err != "":
			m.localRuntimeModal.setFailed(msg.err)
			m.pendingRuntimeSwitch = "" // failed install — the queued switch is dropped
		case !msg.ok:
			m.localRuntimeModal.setFailed("install exited with error")
			m.pendingRuntimeSwitch = ""
		default:
			// Success — wait for LocalRuntimeStatusChanged{ok:true} to
			// confirm the runtime is actually usable, then flip to done.
			// If the event doesn't arrive within a reasonable window we
			// still show the completion to unblock the user.
			m.localRuntimeModal.state = runtimeModalDone
			// If the settings gate queued a runtime switch, dispatch it
			// now — the modal's install succeeded so the runtime is
			// ready to use.
			if m.pendingRuntimeSwitch != "" {
				runtime := m.pendingRuntimeSwitch
				m.pendingRuntimeSwitch = ""
				return m, dispatchLocalRuntimeSwitch(m.agent, runtime)
			}
		}
		return m, nil

	case openLocalRuntimeInstallModalMsg:
		// Emitted by the settings page when the user tries to switch to
		// a runtime that isn't ready. Opens the install modal in its
		// idle state and remembers the switch to dispatch on success.
		if m.localRuntimeModal == nil {
			m.localRuntimeModal = newLocalRuntimeInstallModal(msg.status)
		}
		m.pendingRuntimeSwitch = msg.pending
		return m, nil

	case localRuntimeStatusChangedMsg:
		// Pushed on runtime swap or startup — the headless detection
		// outcome for the currently-selected local runtime. Cache for
		// chip rendering and re-arm the drain loop. When ok=true, drop
		// the cache so the chip disappears and any open install-success
		// modal knows to auto-dismiss.
		if msg.status != nil && msg.status.Ok {
			m.localRuntimeStatus = nil
			if m.localRuntimeModal != nil && m.localRuntimeModal.state == runtimeModalRunning {
				m.localRuntimeModal.state = runtimeModalDone
			}
		} else {
			m.localRuntimeStatus = msg.status
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
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: body})
		m.refreshViewport()
		return m, nil

	case streamEndMsg:
		m.streaming = false
		m.chat.SetStreaming(false)
		if m.cancelStream != nil {
			m.cancelStream() // release the stream context on normal completion
			m.cancelStream = nil
		}
		// Finalize the streaming entry so it stops showing the spinner.
		if e := m.chat.lastAssistantEntry(); e != nil {
			e.Streaming = false
		}
		m.refreshViewport()
		// Poll the agent for the authoritative context-window usage on the
		// same conversation. Result arrives as a ctxUsageMsg and overrides
		// the local cumIn approximation we incremented during streaming.
		m.ctxPollTicks = 20 // ~40s warm window covers the compaction debounce
		// Only spawn the poll ticker if one isn't already running, so rapid
		// back-to-back turns don't multiply concurrent ctxUsageTick loops.
		pollCmds := []tea.Cmd{fetchContextUsage(m.agent, m.convID), fetchRecap(m.agent, m.convID)}
		if !m.ctxPolling {
			m.ctxPolling = true
			pollCmds = append(pollCmds, ctxUsageTick())
		}
		done := tea.Batch(pollCmds...)
		// Drain the next queued message: each completed turn fires the next.
		if nextMsg, ok := m.chat.DrainNext(); ok {
			m.relayout()
			nm, cmd := m.submit(nextMsg.text, nextMsg.images)
			return nm, tea.Batch(cmd, done)
		}
		return m, done

	case banner.TickMsg:
		// Gate forwarding on splashShown — when the splash is dismissed,
		// returning nil Cmd lets the animation's tick chain die out
		// naturally without needing the AnimModel itself to track state.
		if !m.splashShown {
			return m, nil
		}
		var cmd tea.Cmd
		m.splash, cmd = m.splash.Update(msg)
		return m, cmd

	case resumeRequestedMsg:
		// Fired by the history picker's OnSelect after the overlay closes.
		var resumeCmd tea.Cmd
		m, resumeCmd = m.applyResume(msg.ConversationID)
		if msg.Title != "" {
			m.sessionTitle = msg.Title
		}
		return m, resumeCmd

	case dragScrollTickMsg:
		cmd, _ := m.chat.DragScrollTick()
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
		m.ctxPolling = false                          // loop goes idle; the next turn restarts it
		return m, fetchContextUsage(m.agent, m.convID) // one final settle, no re-tick

	case progressAnimTickMsg:
		// This tick has fired and is no longer "in flight". Set inactive
		// up-front; if any condition keeps it alive, we set true again
		// before returning the next tick — that prevents a second kick
		// (e.g. from toolEntryStartMsg) from doubling the tick rate.
		m.animTickActive = false
		keep := false
		// Keep ticking while there's an assistant entry awaiting its first
		// token — that's when the animated status line is visible. Each tick
		// must call refreshViewport so the per-frame color sweep is pushed
		// into the viewport's content cache; without this, View renders the
		// last-set content and the animation appears frozen.
		if e := m.chat.streamingTextEntry(); e != nil && e.Content == "" {
			m.refreshViewport()
			keep = true
		}
		// Tool spinners on in-progress entries need the same per-frame push;
		// without this branch the placeholder tick stops once the assistant
		// streams a token, then the active tool line shows a frozen glyph.
		if m.chat.hasInProgressTool() {
			m.refreshViewport()
			keep = true
		}
		// Between phases of a multi-step turn (tools done, waiting for the
		// model's next action), animate the trailing "still working" line —
		// without this the indicator would freeze on the first frame after
		// tools complete.
		if m.chat.IsBetweenPhases() {
			m.refreshViewport()
			keep = true
		}
		// Also keep ticking while the /c chat is busy so its animated
		// status line repaints on every frame.
		if cv, ok := m.content.(*contextView); ok && cv.busy() {
			keep = true
		}
		if m.compacting {
			keep = true
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
	// User turn — show markers + image count suffix when images are attached.
	content := text
	if len(images) > 0 {
		content = strings.TrimSpace(content)
		content += fmt.Sprintf("  (%d image%s)", len(images), plural(len(images)))
	}
	m.chat.AppendEntry(&Entry{Role: RoleUser, Content: content})
	// Assistant placeholder
	m.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	m.refreshViewport()

	// Pass cwd so the agent prepends .cercano/context.md if present.
	wd, _ := os.Getwd()
	driver := &mainAgentDriver{agent: m.agent, convID: m.convID, workDir: wd}
	cmd, cancel, err := driver.Submit(context.Background(), text, images)
	if err != nil {
		m.errMsg = err.Error()
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "error: " + err.Error()})
		m.refreshViewport()
		return m, nil
	}
	m.cancelStream = cancel
	m.streaming = true
	m.chat.SetStreaming(true)
	m.turnStart = time.Now()
	m.turnActivity = "thinking"
	m.turnTokOut = 0
	m.turnModel = ""
	m.turnCloud = false
	// Fire both the driver's self-re-arming drain and the progress-text
	// animator; both re-issue themselves until streaming ends. Mark the
	// anim loop as running so the tool-start kick path doesn't double-fire it.
	m.animTickActive = true
	return m, tea.Batch(cmd, progressAnimTick())
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
	if m.cancelStream != nil {
		m.cancelStream()
		m.cancelStream = nil
	}
	m.streaming = false
	m.chat.SetStreaming(false)
	if e := m.chat.lastAssistantEntry(); e != nil {
		e.Streaming = false
	}
	m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "⊘ canceled"})
	// Esc aborts the train of thought — drop any queued follow-ups too.
	m.chat.ClearQueue()
	m.relayout()
}

func (m Model) runSlash(line string) (tea.Model, tea.Cmd) {
	res, _ := m.registry.Dispatch(line)
	switch res.Kind {
	case slash.ResultQuit:
		return m, tea.Quit
	case slash.ResultClearConversation:
		m.chat.SetEntriesSlice(nil)
		m.convID = newConvID()
		if m.convRef != nil {
			m.convRef.id = m.convID
		}
		m.sessionTitle = ""
		m.cumIn = 0
		m.cumOut = 0
		m.chat.ExitToolNav()
		m.refreshViewport()
	case slash.ResultOpenSettings:
		sp, cmd := newSettingsPage(m.agent, m.palette, m.styles, m.promptColorToken, m.width, m.height, m.themes, m.theme)
		m.content = sp
		return m, cmd
	case slash.ResultOpenHistoryPicker:
		hv, _ := newHistoryView(m.agent, m.palette, m.styles, m.width, m.height)
		m.content = hv
	case slash.ResultOpenRuntimeDashboard:
		dashboard, _ := newRuntimeDashboard(m.agent, m.palette, m.styles, m.width, m.height)
		m.content = dashboard
	case slash.ResultOpenContextView:
		cv, cmd := newContextView(m.agent, m.palette, m.styles, m.convID, m.width, m.height)
		m.content = cv
		return m, tea.Batch(cmd, contextRefreshTick())
	case slash.ResultResumeConversation:
		// /resume <id> path — slash already validated against the agent.
		var resumeCmd tea.Cmd
		m, resumeCmd = m.applyResume(res.Text)
		return m, resumeCmd
	case slash.ResultSetPromptColor:
		m.promptBorderColor = m.resolvePromptColor(res.Text)
		m.promptColorToken = res.Text
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "prompt color set"})
		m.refreshViewport()
	case slash.ResultSetSessionTitle:
		m.sessionTitle = res.Text
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "renamed to: " + res.Text})
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
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "Permission mode → " + mode})
		m.refreshViewport()
	case slash.ResultInvokeTool:
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
			m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("running tool:" + res.ToolName)})
			m.refreshViewport()
			return m, invokeToolCmd(m.agent, res.ToolName, res.ToolArgs)
		}
		// W or X — queue confirm.
		tc := &pendingToolCall{Name: res.ToolName, Args: res.ToolArgs, Permission: perm}
		m.pendingConfirm = toolConfirm(tc)
		prompt := m.renderConfirmPrompt(tc)
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: prompt})
		m.refreshViewport()
	case slash.ResultText:
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: res.Text})
		m.refreshViewport()
	}
	return m, nil
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
func (m *Model) relayout() {
	contentW := m.width
	if contentW < 20 {
		contentW = 20
	}
	const chromeNoInput = 5 // header + 3 dividers + status (input height added below)
	splashH := 0
	if m.splashEffective() {
		splashH = 9 // 8 banner rows + 1 blank
	}
	// Viewport's first screen row = header (1) + divider (1) + splash height.
	m.scrollbarTop = 2 + splashH
	suggestH := 0
	if m.chat.Width() > 0 && !m.contentPageActive() {
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
	queuedH := len(m.chat.Queued()) // one row per queued message, rendered above the prompt
	// Size the input first — DynamicHeight re-fits it to the wrapped content at
	// this width; the body claims whatever rows are left.
	m.input.SetWidth(contentW - 4)
	inputH := m.input.Height()
	bodyH := m.height - chromeNoInput - inputH - splashH - suggestH - recapH - queuedH
	if bodyH < 3 {
		bodyH = 3
	}
	m.chat.SetSize(contentW-2, bodyH) // reserve two right columns: a gap + the scrollbar
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

func (m Model) promptTop() int {
	top := m.contentTop()
	top += m.chat.Height()
	if m.recap != "" {
		top += 2 // blank spacer line + the recap line
	}
	top += len(m.chat.Queued()) // queued messages render above the prompt border
	top++                // prompt border above the input
	if hint := m.renderSlashSuggestions(); hint != "" && !m.contentPageActive() {
		top += strings.Count(hint, "\n") + 1
	}
	return top
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
	m.chat.SetTurnStatus(turnStatus{
		activity: m.turnActivity,
		start:    m.turnStart,
		tokOut:   m.turnTokOut,
		model:    m.turnModel,
		cloud:    m.turnCloud,
	})
	m.chat.rebuild()
}

func (m Model) preparePromptInput() Model {
	needsRefresh := m.chat.InToolNav()
	if m.chat.SelectionActive() {
		m.chat.ClearSelection()
	}
	m.selectionNotice = ""
	if needsRefresh {
		m.chat.ExitToolNav()
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
//     (`▌▘▀▝▐▗▄▖`) at 80ms/frame — gives the visual of a square rolling
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
	const frames = "▌▘▀▝▐▗▄▖"
	const frameMs = 80
	const pulseCycleMs = 1500

	nowMs := time.Now().UnixMilli()

	// Rotation.
	runes := []rune(frames)
	glyph := string(runes[int(nowMs/frameMs)%len(runes)])

	// Brightness pulse — stays in the orange/amber family throughout:
	// primary amber base lerps to bright amber peak (palette colors), so
	// the rolling block reads orange at every phase of the cycle.
	phase := float64(nowMs%int64(pulseCycleMs)) / float64(pulseCycleMs)
	pulse := 0.5 + 0.5*math.Sin(phase*2*math.Pi)
	base := [3]uint8{0xEA, 0x82, 0x12} // primary amber
	peak := [3]uint8{0xFF, 0xB8, 0x4D} // bright amber
	c := [3]uint8{
		uint8(float64(base[0]) + (float64(peak[0])-float64(base[0]))*pulse),
		uint8(float64(base[1]) + (float64(peak[1])-float64(base[1]))*pulse),
		uint8(float64(base[2]) + (float64(peak[2])-float64(base[2]))*pulse),
	}
	hex := []byte("#000000")
	const digits = "0123456789ABCDEF"
	hex[1] = digits[c[0]>>4]
	hex[2] = digits[c[0]&0xF]
	hex[3] = digits[c[1]>>4]
	hex[4] = digits[c[1]&0xF]
	hex[5] = digits[c[2]>>4]
	hex[6] = digits[c[2]&0xF]
	return lipgloss.NewStyle().Foreground(lipgloss.Color(string(hex))).Render(glyph)
}

// animateLimeSweep renders `text` with a per-char color sweep — lime base, a
// bright peak (lime→white) traveling left-to-right on a 1.5s loop. Phase is
// derived from wall-clock time so the animation stays smooth regardless of
// when the status text last changed.
func animateLimeSweep(text string) string {
	const (
		cycleMs = 1500 // one full sweep duration
		tail    = 4.0  // half-width of the bright band, in columns
		padCols = 4.0  // off-screen lead-in / trail-out
	)
	// Walk-clock phase, 0..1.
	phaseMs := time.Now().UnixMilli() % int64(cycleMs)
	progress := float64(phaseMs) / float64(cycleMs)

	cols := utf8.RuneCountInString(text)
	sweepPos := -padCols + progress*(float64(cols)+2*padCols)

	var b strings.Builder
	col := 0
	for _, r := range text {
		b.WriteString(lipgloss.NewStyle().
			Foreground(progressColorAt(col, sweepPos, tail)).
			Render(string(r)))
		col++
	}
	return b.String()
}

// progressColorAt returns the rendered color for one column at a given sweep
// position. Lime base; the inside `tail` columns lerp toward white.
func progressColorAt(col int, sweepPos float64, tail float64) color.Color {
	dist := float64(col) - sweepPos
	if dist < 0 {
		dist = -dist
	}
	if dist >= tail {
		return lipgloss.Color("#BDF000") // lime base
	}
	k := 1.0 - dist/tail               // 0 at edge, 1 at peak
	base := [3]uint8{0xBD, 0xF0, 0x00} // lime
	peak := [3]uint8{0xFF, 0xFF, 0xFF} // white peak
	c := [3]uint8{
		uint8(float64(base[0]) + (float64(peak[0])-float64(base[0]))*k),
		uint8(float64(base[1]) + (float64(peak[1])-float64(base[1]))*k),
		uint8(float64(base[2]) + (float64(peak[2])-float64(base[2]))*k),
	}
	hex := []byte("#000000")
	const digits = "0123456789ABCDEF"
	hex[1] = digits[c[0]>>4]
	hex[2] = digits[c[0]&0xF]
	hex[3] = digits[c[1]>>4]
	hex[4] = digits[c[1]&0xF]
	hex[5] = digits[c[2]>>4]
	hex[6] = digits[c[2]&0xF]
	return lipgloss.Color(string(hex))
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

// applyResume updates the model + the convRef shared with the slash registry,
// then rehydrates scrollback from the persisted turns. Returns a tea.Cmd that
// fetches authoritative context usage from the server — the polling loop only
// arms after a turn completes, so without this the meter sits at 0 from
// resume until the user takes their next turn. The cmd is the only way to
// drive the meter from this code path; callers MUST plumb it through.
func (m Model) applyResume(conversationID string) (Model, tea.Cmd) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	turns, err := m.agent.ResumeConversation(ctx, conversationID)
	if err != nil {
		m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "resume failed: " + err.Error()})
		m.refreshViewport()
		return m, nil
	}
	m.convID = conversationID
	if m.convRef != nil {
		m.convRef.id = conversationID
	}
	m.chat.SetEntriesSlice(nil)
	// cumIn/cumOut wait for fetchContextUsage. The previous local sum here
	// summed only TokensIn, mishandled tool-call turns, and got overwritten
	// by the RPC within a roundtrip anyway — a wrong first-paint with no
	// upside.
	m.cumIn = 0
	m.cumOut = 0
	m.chat.ExitToolNav()
	m.splashShown = false
	// Fetch the compaction state so we can place the freeze-boundary divider
	// in the right spot inside the resumed history. Failure is non-fatal —
	// the resume still works, just without the divider.
	var frozenThrough int64
	if cs, err := m.agent.GetCompactionState(ctx, conversationID); err == nil && cs != nil {
		frozenThrough = cs.FrozenThrough
	}
	m.chat.SetEntriesSlice(resumeEntries(turns, frozenThrough))
	// Restore the prior session's living recap into the footer line (renderRecap),
	// or show a "recap unavailable" placeholder if the recap generator has been
	// silently failing (e.g. local runtime misconfigured). Don't push into
	// scrollback — that showed the recap twice on resume.
	if info, err := m.agent.GetConversation(ctx, conversationID); err == nil {
		m.recap = recapDisplay(info)
	}
	m.relayout()
	return m, fetchContextUsage(m.agent, m.convID)
}

// renderConfirmPrompt builds the single-line confirm message shown in
// scrollback while pendingConfirm is set. W-tier renders normally; X-tier
// gets a red ⚠ destructive emphasis. MCP tools get an additional [a]lways key.
func (m Model) renderConfirmPrompt(p *pendingToolCall) string {
	head := m.styles.Accent.Render("▸ ")
	if p.Permission == "X" {
		head = m.styles.Error.Render("▸ ⚠ DESTRUCTIVE ")
	} else if p.Destructive {
		// MCP tool that self-reports a destructive hint: surface a ⚠ marker
		// (display-only — gating is unchanged; the hint never escalates tier).
		head = m.styles.Accent.Render("▸ ⚠ ")
	}
	summary := displayToolName(p.Name) + " " + truncateArgs(p.Args, 80)
	out := head +
		m.styles.AgentProse.Render(summary) +
		m.styles.BorderDim.Render("   ·   ") +
		m.styles.Muted.Render("[") +
		m.styles.Accent.Render("y") +
		m.styles.Muted.Render("]es / [") +
		m.styles.Accent.Render("n") +
		m.styles.Muted.Render("]o / [") +
		m.styles.Accent.Render("d") +
		m.styles.Muted.Render("]iff")
	if strings.HasPrefix(p.Name, "mcp__") {
		out += m.styles.Muted.Render(" / [") +
			m.styles.Accent.Render("a") +
			m.styles.Muted.Render("]lways")
	}
	return out
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

// resolveConfirmKey processes a keystroke while a confirm is pending.
// y/Y → onYes, n/N/esc/ctrl+c → onNo, extras keys → their handler,
// anything else → ignored (confirm stays pending).
func (m Model) resolveConfirmKey(key string) (Model, tea.Cmd) {
	c := m.pendingConfirm
	if c == nil {
		return m, nil
	}
	switch key {
	case "y", "Y":
		return c.onYes(m)
	case "n", "N", "esc", "ctrl+c":
		return c.onNo(m)
	default:
		if fn, ok := c.extras[key]; ok {
			return fn(m)
		}
		return m, nil
	}
}

// toolConfirm builds the confirmRequest for a tool-permission decision,
// preserving the prior behavior: y approves (Allow RPC for stream-origin call,
// else local invoke), n denies (Deny RPC for stream-origin), d/D reveals args.
// MCP tools (mcp__*) additionally expose an [a]lways key that persists the
// allow server-side so future calls run silently.
func toolConfirm(tc *pendingToolCall) *confirmRequest {
	cr := &confirmRequest{
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Accent.Render("✓ approved — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				// Stream-event origin: unblock the server-side tool loop.
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.AllowToolCall(context.Background(), id) }()
				}
				return m, nil
			}
			// Local /tool origin: fire the invoke directly.
			return m, invokeToolCmd(m.agent, tc.Name, tc.Args)
		},
		onNo: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: m.styles.Muted.Render("canceled.")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.DenyToolCall(context.Background(), id) }()
				}
			}
			return m, nil
		},
		extras: map[string]func(Model) (Model, tea.Cmd){
			"d": func(m Model) (Model, tea.Cmd) {
				m.chat.AppendEntry(&Entry{Role: RoleSystem,
					Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
			"D": func(m Model) (Model, tea.Cmd) {
				m.chat.AppendEntry(&Entry{Role: RoleSystem,
					Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
		},
	}
	// MCP tools are confirm-by-default; offer always-allow, which persists a
	// silent allowlist rule server-side so future calls bypass the prompt.
	if strings.HasPrefix(tc.Name, "mcp__") {
		cr.extras["a"] = func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.chat.AppendEntry(&Entry{Role: RoleSystem,
				Content: m.styles.Accent.Render("✓ always-allowed — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.AllowToolCallPersist(context.Background(), id, true) }()
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

// truncateArgs renders the JSON args compactly for the confirm prompt one-liner.
func truncateArgs(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
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
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("")
		v.AltScreen = true
		return v // first paint before WindowSizeMsg
	}

	var parts []string
	parts = append(parts, m.renderHeader())
	parts = append(parts, m.styles.BorderDim.Render(strings.Repeat("─", m.width)))
	if m.splashEffective() {
		parts = append(parts, m.splash.View())
		parts = append(parts, "")
	}

	switch {
	case m.content != nil:
		parts = append(parts, m.content.View())
	default:
		parts = append(parts, m.renderViewportWithScrollbar())
		if m.recap != "" {
			parts = append(parts, m.renderRecap())
		}
	}

	promptLine := lipgloss.NewStyle().Foreground(m.promptBorderColor).Render(strings.Repeat("─", m.width))
	promptParts := []string{}
	// Queued messages float just above the prompt border.
	if q := m.renderQueued(); q != "" {
		promptParts = append(promptParts, q)
	}
	promptParts = append(promptParts, promptLine)
	if hint := m.renderSlashSuggestions(); hint != "" && !m.contentPageActive() {
		promptParts = append(promptParts, hint)
	}
	inputIdx := len(parts) + len(promptParts)
	promptParts = append(promptParts, m.input.View())
	promptParts = append(promptParts, promptLine)
	if notice := m.visionNotice(); notice != "" {
		promptParts = append(promptParts, notice)
	}
	promptParts = append(promptParts, m.renderStatus())

	spareRows := m.height - countLines(parts) - countLines(promptParts)
	for range spareRows {
		parts = append(parts, "")
		inputIdx++
	}
	parts = append(parts, promptParts...)

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
	if m.localRuntimeModal != nil {
		boxW, boxH := m.localRuntimeModal.modalDim(m.width, m.height)
		x := (m.width - boxW) / 2
		y := (m.height - boxH) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		box := m.localRuntimeModal.View(m.styles, m.palette, m.width, m.height)
		out = composeOverlay(out, box, x, y)
	}
	v := tea.NewView(out)
	v.AltScreen = true
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
	last, ok := m.chat.UnstageLast()
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

// renderQueued draws the messages queued while a response streams as a
// navy-fill strip just above the prompt — one per line, starting at the
// content margin and spanning to the right edge. The "⊕" marker shows in
// muted lime; the text in bright amber on the navy fill, so the queued
// lines read as upcoming user prompts (matching the same palette slot
// designated for echoed user-prompt rows in the scrollback). Empty when
// nothing is queued.
func (m Model) renderQueued() string {
	queued := m.chat.Queued()
	if len(queued) == 0 {
		return ""
	}
	leftPad := strings.Repeat(" ", entryIndent)
	avail := m.width - entryIndent - 2 // marker "⊕ " takes 2 cells
	if avail < 1 {
		avail = 1
	}
	lines := make([]string, len(queued))
	for i, q := range queued {
		text := q
		if lipgloss.Width(text) > avail {
			r := []rune(text)
			if len(r) > avail-1 {
				text = string(r[:avail-1]) + "…"
			}
		}
		fill := avail - lipgloss.Width(text)
		if fill < 0 {
			fill = 0
		}
		lines[i] = leftPad +
			m.styles.BufferUserMarker.Render("⊕ ") +
			m.styles.BufferUserText.Render(text+strings.Repeat(" ", fill))
	}
	return strings.Join(lines, "\n")
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
	return m.chat.View()
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

	rightPieces := []string{}
	if m.cloudModel != "" {
		rightPieces = append(rightPieces,
			m.styles.Info.Render("c:"),
			m.styles.Accent.Render(abbreviateModel(m.cloudModel)),
			m.styles.BorderDim.Render(" │ "),
		)
	}
	rightPieces = append(rightPieces,
		m.styles.Info.Render("l:"),
		m.styles.Accent.Render(abbreviateModel(m.lastModel)),
	)
	right := lipgloss.JoinHorizontal(lipgloss.Left, rightPieces...)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	// No title — flush left and right to the edges with a single gap.
	if m.sessionTitle == "" {
		gap := m.width - leftW - rightW
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + right
	}

	title := m.styles.Info.Render("░▒▓ ") +
		m.styles.Info.Render(m.sessionTitle) +
		m.styles.Info.Render(" ▓▒░")
	titleW := lipgloss.Width(title)

	// Center the title across the full bar.
	titleStart := (m.width - titleW) / 2
	gapBefore := titleStart - leftW
	if gapBefore < 2 {
		gapBefore = 2 // collision guard with the brand
	}
	titleEnd := leftW + gapBefore + titleW
	gapAfter := m.width - rightW - titleEnd
	if gapAfter < 2 {
		gapAfter = 2 // collision guard with the model strip
	}
	return left +
		strings.Repeat(" ", gapBefore) +
		title +
		strings.Repeat(" ", gapAfter) +
		right
}

func (m Model) renderStatus() string {
	// The footer stays put during a turn — the live turn status renders inline on
	// the assistant placeholder, not here.
	help := m.styles.Muted.Render("/help for cmds")
	if m.chat.SelectionHasRange() {
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
		cloudPart = m.styles.BorderDim.Render("  ·  ") + m.styles.Muted.Render("cloud:") + m.styles.Error.Render(" NONE")
	case "ok":
		cloudPart = m.styles.BorderDim.Render("  ·  ") + m.styles.Muted.Render("cloud:") + m.styles.Success.Render(" ok")
	}
	// Show the token counter only once a turn has completed — no "0↑/0↓" on a
	// fresh session — and label it "last turn" since it's the prior turn's total.
	turnPart := ""
	if m.hadTurn {
		turnPart = m.styles.BorderDim.Render("  ·  ") +
			m.styles.Muted.Render(fmt.Sprintf("last turn %d↑/%d↓", m.tokIn, m.tokOut))
	}
	parts := []string{
		m.renderContextMeter(),
		turnPart,
		cloudPart,
		m.renderMeridianChip(),
		m.renderLocalRuntimeChip(),
		m.renderPermissionModeChip(),
		m.styles.BorderDim.Render("  ·  "),
		help,
	}
	return lipgloss.NewStyle().Width(m.width).Render(strings.Join(parts, ""))
}

// renderMeridianChip surfaces local Meridian proxy state in the status bar
// when the user needs to know or act:
//   - needs_auth       → amber "⚠ /login"  (run claude login)
//   - prereqs_missing  → red   "⚠ meridian: setup"
//   - failed           → red   "✕ meridian: failed"
//   - starting         → muted "meridian: starting…"
//
// All other states (disabled, ready, external) return "" — Meridian is
// either not in use or working silently, and the bar should stay quiet.
func (m Model) renderMeridianChip() string {
	if m.meridianStatus == nil {
		return ""
	}
	var label string
	var valStyle lipgloss.Style
	switch m.meridianStatus.State {
	case "needs_auth":
		label = "⚠ Sign in to Claude (run: claude login)"
		valStyle = m.styles.Primary
	case "prereqs_missing":
		label = "⚠ Meridian setup needed"
		valStyle = m.styles.Error
	case "failed":
		label = "✕ Meridian failed"
		valStyle = m.styles.Error
	case "starting":
		label = "Meridian: starting…"
		valStyle = m.styles.Muted
	default:
		return ""
	}
	return m.styles.BorderDim.Render("  ·  ") + valStyle.Render(label)
}

// renderLocalRuntimeChip surfaces local-runtime detection state — currently
// only used when the user has switched local_runtime to llama_server (via
// config file edit or /config) and the agent's headless detection couldn't
// find the binary or a GGUF model. In that case we show an amber chip
// telling the user to press F1 to open the install modal. When status is
// nil or ok, the chip is hidden.
func (m Model) renderLocalRuntimeChip() string {
	if m.localRuntimeStatus == nil || m.localRuntimeStatus.Ok {
		return ""
	}
	var label string
	switch m.localRuntimeStatus.Missing {
	case "binary":
		label = "⚠ llama-server not installed (F1)"
	case "model":
		label = "⚠ no GGUF model found (F1)"
	default:
		label = "⚠ local runtime: setup (F1)"
	}
	return m.styles.BorderDim.Render("  ·  ") + m.styles.Primary.Render(label)
}

// renderPermissionModeChip renders the session-mode chip for the status bar:
// strict → red (Error), permissive → amber (Primary), bypass → lime (Accent).
// Returns the empty string when the mode isn't known yet (the startup fetch
// hasn't landed) so the bar doesn't show a misleading default.
func (m Model) renderPermissionModeChip() string {
	if m.permissionMode == "" {
		return ""
	}
	var valStyle lipgloss.Style
	switch m.permissionMode {
	case "strict":
		valStyle = m.styles.Error
	case "bypass":
		valStyle = m.styles.Accent
	default: // permissive (or anything unexpected) → amber
		valStyle = m.styles.Primary
	}
	return m.styles.BorderDim.Render("  ·  ") +
		m.styles.Muted.Render("mode:") +
		valStyle.Render(" "+m.permissionMode)
}

func (m Model) renderContextMeter() string {
	// cumIn now carries the agent-reported cumulative used (set by
	// ctxUsageMsg) rather than per-turn input. Fall back to cumIn+cumOut
	// before the first usage RPC has returned.
	used := m.cumIn
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
	if m.ctxRaw > used && used > 0 {
		saved := int(100 * (1 - float64(used)/float64(m.ctxRaw)))
		badge = m.styles.Muted.Render(fmt.Sprintf("  ·  ▣ %d%%↓", saved))
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
			// Letter inherits the bar's bright shimmer color — feels like the
			// bar's fill is showing through the letter shape.
			b.WriteString(lipgloss.NewStyle().
				Foreground(progressColorAt(col, sweepPos, tail)).
				Render(string(label[col-start])))
		case inLabel && !onFill:
			// Letter inherits the bar's dim empty color — same idea, but
			// for the un-filled side.
			b.WriteString(m.styles.MeterEmpty.Render(string(label[col-start])))
		case !inLabel && onFill:
			b.WriteString(lipgloss.NewStyle().
				Foreground(progressColorAt(col, sweepPos, tail)).
				Render("█"))
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
