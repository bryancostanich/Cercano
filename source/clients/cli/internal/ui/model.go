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
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/banner"
	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/slash"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// Role tags a scrollback entry's origin so the renderer can style it.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
	RoleSystem // /help output, errors, progress notes
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

	// scrollbarDragging is true while the user holds the mouse on the
	// scrollbar; motion events then scrub the viewport scroll position.
	scrollbarDragging bool

	// contentScrollbarDragging is the same gesture, but for a reusable content
	// page scrollbar rather than the main chat scrollback.
	contentScrollbarDragging bool

	// dragMouse is the last pointer position during a text-selection drag.
	// dragScrolling is true while the edge auto-scroll tick loop is running, so
	// holding the pointer past the top/bottom edge keeps scrolling without
	// needing mouse motion. See dragScrollTickMsg.
	dragMouse     tea.Mouse
	dragScrolling bool

	// root and home are resolved once at construction; used to humanize tool-call
	// path arguments (relative to the project root, ~-abbreviated under home).
	root string
	home string

	palette theme.Palette
	styles  theme.Styles

	// md renders assistant markdown prose. Holds per-width Glamour renderers
	// and a render cache for committed blocks.
	md *render.Markdown

	agent  *agentclient.Client
	convID string

	registry *slash.Registry

	splashShown bool // hide after first user input
	splash      banner.AnimModel
	entries     []*Entry
	viewport    viewport.Model
	// viewportPlainLines mirrors the rendered viewport content with ANSI
	// styling stripped. It lets mouse selection copy clean text while the
	// viewport itself keeps the styled display string.
	viewportPlainLines []string
	selection          textSelection
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

	streamCh  <-chan agentclient.StreamMsg
	streaming bool
	// cancelStream cancels the context of the in-flight StreamChat so Esc can
	// abort a running prompt. Nil when nothing is streaming.
	cancelStream context.CancelFunc

	tokIn, tokOut  int
	cumIn, cumOut  int
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

	// queued holds messages submitted while a response was streaming, FIFO.
	// They render just above the prompt, drain (front) as each stream completes,
	// and the most-recent (back) can be popped back into the prompt with ↑.
	queued []string

	// convRef shares the current convID with the slash registry by reference,
	// so /rename always targets whatever conversation the model currently has
	// active (including after /resume).
	convRef *struct{ id string }

	openHistoryOnStart bool // -r flag → open the history picker after first WindowSizeMsg

	// promptBorderColor is the color of the lines immediately above and
	// below the input row. Defaults to the palette's accent (lime). /color
	// sets it at runtime.
	promptBorderColor color.Color

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

	// focusedToolIdx points into m.entries at the tool entry the user is
	// currently navigating; -1 means the input box owns focus (default). Esc
	// on empty input enters nav mode at the most recent tool entry; up/down
	// cycle, enter/tab toggle Folded, esc returns to input.
	focusedToolIdx int
}

// pendingToolCall is a queued tool invocation awaiting user confirmation.
type pendingToolCall struct {
	// ToolUseID identifies the paused server-side tool call. Set when the
	// confirm prompt is raised by a PermissionRequired streaming event;
	// empty for legacy local-/tool invocations that route directly through
	// InvokeTool. The y/n resolver uses it to RPC Allow/DenyToolCall back
	// to the agent so the server-side tool loop can unblock.
	ToolUseID  string
	Name       string
	Args       string
	Permission string // "R" | "W" | "X" — R never reaches here, but kept for symmetry
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
	p := theme.Cracker()
	s := theme.NewStyles(p)

	ti := newPromptInput()
	ti.Placeholder = defaultInputPlaceholder
	// Grow/shrink to fit wrapped content, from one line up to the cap; beyond
	// the cap the prompt scrolls internally.
	ti.MinHeight = 1
	ti.MaxHeight = maxInputLines
	// Lime "▶ " on the first line; a 2-space hang indent on wrapped/extra lines
	// so continuation text aligns under the first line's content.
	ti.SetPromptFunc(2, func(info promptInfo) string {
		if info.LineNumber == 0 {
			return s.UserPrompt.Render("▶ ")
		}
		return "  "
	})
	ti.SetStyles(promptInputStyles{
		Text:        lipgloss.NewStyle().Foreground(p.Primary),
		Placeholder: lipgloss.NewStyle().Foreground(p.Muted),
		Selection: lipgloss.NewStyle().
			Foreground(p.BgDeep).
			Background(p.Info),
	})
	ti.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))

	reg := slash.New()
	slash.RegisterBasics(reg)
	slash.RegisterConfig(reg, ag)
	slash.RegisterColor(reg)
	slash.RegisterContext(reg)
	slash.RegisterTools(reg, ag)
	slash.RegisterPermissions(reg, ag)
	// currentConv is captured by reference so it always returns the active
	// conversation id even after /resume swaps it.
	convRef := &struct{ id string }{}
	slash.RegisterHistory(reg, ag, func() string { return convRef.id })
	slash.RegisterRuntime(reg)
	slash.RegisterContextView(reg)

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

	return Model{
		root:               root,
		home:               home,
		palette:            p,
		styles:             s,
		md:                 render.NewMarkdown(theme.CrackerMarkdownStyle()),
		agent:              ag,
		convID:             initialConvID,
		convRef:            convRef,
		registry:           reg,
		splashShown:        !openHistoryOnStart,
		splash:             splash,
		viewport:           vp,
		input:              ti,
		lastModel:          "qwen3-coder",
		modelMaxTokens:     128_000, // placeholder until the agent serves real ctx limits
		openHistoryOnStart: openHistoryOnStart,
		promptBorderColor:  p.Accent,
		focusedToolIdx:     -1,
	}
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
	m.entries = append(m.entries, &Entry{Role: RoleAssistant, Content: doc})
	m.splashShown = false
	return m
}

// Init is called by Bubble Tea once at startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), m.splash.Init(), fetchConfigCmd(m.agent), fetchToolsCmd(m.agent), fetchPermissionModeCmd(m.agent))
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

// streamTickMsg signals "drain one message from the active stream channel."
type streamTickMsg struct{ msg agentclient.StreamMsg }
type streamEndMsg struct{}

// ctxUsageMsg carries the result of an asynchronous GetContextUsage call.
type ctxUsageMsg struct {
	Used, Max int
	Percent   float64
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
		return ctxUsageMsg{Used: u.TokensUsed, Max: u.ModelMax, Percent: u.Percent}
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
		return recapLoadedMsg{recap: info.Recap}
	}
}

// progressAnimTickMsg fires every ~50ms while a streaming assistant entry is
// awaiting its first token. Triggers a View re-render so the per-char sweep
// over the status text advances.
type progressAnimTickMsg time.Time

func progressAnimTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg { return progressAnimTickMsg(t) })
}

// dragScrollTickMsg drives continuous auto-scroll while a selection drag is held
// past the viewport's top/bottom edge (no mouse motion required).
type dragScrollTickMsg struct{}

func dragScrollTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return dragScrollTickMsg{} })
}

// atScrollEdge reports whether the last drag pointer position sits past the top
// or bottom edge of the viewport — the condition for edge auto-scroll.
func (m Model) atScrollEdge() bool {
	row := m.dragMouse.Y - m.scrollbarTop
	return row < 0 || row >= m.viewport.Height()
}

func waitForStream(ch <-chan agentclient.StreamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamEndMsg{}
		}
		return streamTickMsg{msg: msg}
	}
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
			hp, _ := newHistoryPicker(m.agent, m.palette, m.styles, m.width, m.height, m.convID)
			m.content = hp
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
		if m.selection.Dragging {
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
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
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
			return m, nil
		}
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
		if m.mouseInPrompt(mouse) {
			m.clearSelection()
			m.input.MouseDown(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		height := m.viewport.Height()
		// The bar occupies the last column (width-1). Accept the rightmost column
		// and anything past it: terminals report a click in the final column as
		// X=width-1 or, in some cases, X=width (one past) — an exact == match made
		// the 1-column bar unreliable to grab. Viewport text is X < width-1, so
		// this never steals a text click.
		onBar := mouse.X >= m.width-1 &&
			mouse.Y >= m.scrollbarTop && mouse.Y < m.scrollbarTop+height
		if onBar {
			// Grabbing the scrollbar is a scroll gesture, not a selection;
			// cancel any in-progress selection drag so it can't hijack motion.
			m.selection.Dragging = false
			m.scrollbarDragging = true
			off := scrollOffsetFromClick(mouse.Y, m.scrollbarTop, height, m.viewport.TotalLineCount())
			m.viewport.SetYOffset(off)
			return m, nil
		}
		if m.mouseInViewportText(mouse) {
			m.beginSelection(mouse)
			return m, nil
		}
		m.clearSelection()
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
			m.scrollbarDragging = false
			m.selection.Dragging = false
			m.input.CancelDrag()
			return m, nil
		}
		mouse := msg.Mouse()
		if m.input.Dragging() {
			m.input.MouseDrag(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		// An active scrollbar drag is unambiguous and takes priority over text
		// selection — otherwise a left-over selection.Dragging would swallow the
		// motion and the bar wouldn't scroll.
		if m.scrollbarDragging {
			height := m.viewport.Height()
			off := scrollOffsetFromClick(mouse.Y, m.scrollbarTop, height, m.viewport.TotalLineCount())
			m.viewport.SetYOffset(off)
			return m, nil
		}
		if m.selection.Dragging {
			m.dragMouse = mouse
			m.updateSelection(mouse, true)
			// Held past an edge → start the auto-scroll tick so it keeps
			// scrolling even when the pointer stops moving.
			if m.atScrollEdge() && !m.dragScrolling {
				m.dragScrolling = true
				return m, dragScrollTick()
			}
			return m, nil
		}
		return m, nil

	case tea.MouseReleaseMsg:
		if m.contentPageActive() {
			m.contentScrollbarDragging = false
			return m, nil
		}
		mouse := msg.Mouse()
		m.dragScrolling = false
		if m.input.Dragging() {
			m.input.MouseUp(mouse.X, mouse.Y-m.promptTop())
			return m, nil
		}
		if m.selection.Dragging {
			m.updateSelection(mouse, true)
			m.selection.Dragging = false
			if m.selection.empty() {
				m.clearSelection()
			} else if text := m.selectedText(); text != "" {
				m.selectionNotice = "copied selection"
				m.scrollbarDragging = false
				return m, selectionClipboardCmd(text)
			}
		}
		m.scrollbarDragging = false
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		return m, nil

	case tea.PasteMsg:
		if m.contentPageActive() || m.pendingConfirm != nil {
			return m, nil
		}
		m = m.preparePromptInput()
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
				if pageID == contentPageConfig {
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
		if m.selection.Active {
			next, cmd, handled := m.handleSelectionKey(msg)
			if handled {
				return next, cmd
			}
			m = next
		}
		// Tool-entry navigation mode: focus is on a scrollback tool entry
		// rather than the input box. Up/down cycle (clamped at edges),
		// enter/tab toggle Folded, esc returns to input. Any other key
		// returns to input and is then handled by the normal input path.
		if m.focusedToolIdx >= 0 {
			switch {
			case key.Matches(msg, keys.NavUp):
				indices := m.toolEntryIndices()
				for i, idx := range indices {
					if idx == m.focusedToolIdx {
						if i > 0 {
							m.focusedToolIdx = indices[i-1]
							m.refreshViewport()
						}
						break
					}
				}
				return m, nil
			case key.Matches(msg, keys.NavDown):
				indices := m.toolEntryIndices()
				for i, idx := range indices {
					if idx == m.focusedToolIdx {
						if i < len(indices)-1 {
							m.focusedToolIdx = indices[i+1]
							m.refreshViewport()
						}
						break
					}
				}
				return m, nil
			case key.Matches(msg, keys.ToggleTool):
				if m.focusedToolIdx < len(m.entries) && m.entries[m.focusedToolIdx].Tool != nil {
					m.entries[m.focusedToolIdx].Tool.Folded = !m.entries[m.focusedToolIdx].Tool.Folded
					m.refreshViewport()
				}
				return m, nil
			case key.Matches(msg, keys.Back):
				m.focusedToolIdx = -1
				m.refreshViewport()
				return m, nil
			}
			// Any other key (typing) drops nav mode and falls through to
			// normal input handling so the character actually lands in the
			// input box.
			m = m.preparePromptInput()
			// fall through
		}
		// Esc on empty input enters tool-entry navigation mode, focusing the
		// most-recent tool entry. No-op when scrollback has no tool entries.
		if key.Matches(msg, keys.Back) && m.input.Value() == "" {
			indices := m.toolEntryIndices()
			if len(indices) > 0 {
				m.focusedToolIdx = indices[len(indices)-1]
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
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		unmodifiedArrow := msg.Key().Mod == 0
		switch keyStr {
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			m.splashShown = false
			// Submitting mid-stream queues the message instead of starting a
			// second turn; it sends when the current stream completes.
			if m.streaming {
				m.queued = append(m.queued, text)
				m.relayout()
				return m, nil
			}
			// Reset the input back to one line (and reclaim any splash rows).
			m.relayout()
			return m.submit(text)
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
		if cv.showingProposal || (cv.pane != nil && cv.pane.Busy()) {
			return m, contextRefreshTick()
		}
		return m, tea.Batch(loadContextSnapshotCmd(cv.agent, cv.convID), contextRefreshTick())

	case contextSnapshotMsg:
		if cv, ok := m.content.(*contextView); ok {
			cv.snapshot = msg.snap
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

	case streamTickMsg:
		// Ignore late messages from a stream we already canceled.
		if !m.streaming {
			return m, nil
		}
		return m.applyStreamMsg(msg.msg)

	case ctxUsageMsg:
		// Authoritative context-window meter from the agent; overrides
		// our locally-summed cumIn approximation.
		if msg.Used > 0 {
			m.cumIn = msg.Used
		}
		if msg.Max > 0 {
			m.modelMaxTokens = msg.Max
		}
		return m, nil

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

	case toolsLoadedMsg:
		cache := make(map[string]agentclient.ToolInfo, len(msg.Tools))
		for _, t := range msg.Tools {
			cache[t.Name] = t
		}
		m.toolCache = cache
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
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: body})
		m.refreshViewport()
		return m, nil

	case streamEndMsg:
		m.streaming = false
		if m.cancelStream != nil {
			m.cancelStream() // release the stream context on normal completion
			m.cancelStream = nil
		}
		// Finalize the streaming entry so it stops showing the spinner.
		if e := m.lastAssistantEntry(); e != nil {
			e.Streaming = false
		}
		m.refreshViewport()
		// Poll the agent for the authoritative context-window usage on the
		// same conversation. Result arrives as a ctxUsageMsg and overrides
		// the local cumIn approximation we incremented during streaming.
		done := tea.Batch(fetchContextUsage(m.agent, m.convID), fetchRecap(m.agent, m.convID))
		// Drain the next queued message: each completed turn fires the next.
		if len(m.queued) > 0 {
			nextMsg := m.queued[0]
			m.queued = m.queued[1:]
			m.relayout()
			nm, cmd := m.submit(nextMsg)
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
		m = m.applyResume(msg.ConversationID)
		if msg.Title != "" {
			m.sessionTitle = msg.Title
		}
		return m, nil

	case dragScrollTickMsg:
		// Continuous edge auto-scroll: while the drag is still held past an
		// edge, scroll one line and extend the selection, then reschedule.
		if !m.selection.Dragging || !m.atScrollEdge() {
			m.dragScrolling = false
			return m, nil
		}
		m.updateSelection(m.dragMouse, true)
		return m, dragScrollTick()

	case progressAnimTickMsg:
		// Keep ticking while there's an assistant entry awaiting its first
		// token — that's when the animated status line is visible. Each tick
		// must call refreshViewport so the per-frame color sweep is pushed
		// into the viewport's content cache; without this, View renders the
		// last-set content and the animation appears frozen.
		if e := m.streamingTextEntry(); e != nil && e.Content == "" {
			m.refreshViewport()
			return m, progressAnimTick()
		}
		// Also keep ticking while the /c chatPane is busy so its animated
		// status line repaints on every frame.
		if cv, ok := m.content.(*contextView); ok && cv.pane != nil && cv.pane.Busy() {
			return m, progressAnimTick()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) submit(text string) (tea.Model, tea.Cmd) {
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
	// User turn
	m.entries = append(m.entries, &Entry{Role: RoleUser, Content: text})
	// Assistant placeholder
	m.entries = append(m.entries, &Entry{Role: RoleAssistant, Content: "", Streaming: true})
	m.refreshViewport()

	// Pass cwd so the agent prepends .cercano/context.md if present.
	wd, _ := os.Getwd()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.agent.StreamChat(ctx, m.convID, text, wd)
	if err != nil {
		cancel()
		m.errMsg = err.Error()
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "error: " + err.Error()})
		m.refreshViewport()
		return m, nil
	}
	m.cancelStream = cancel
	m.streamCh = ch
	m.streaming = true
	// Reset live turn telemetry; the engine fields fill in on RouteSelected.
	m.turnStart = time.Now()
	m.turnActivity = "thinking"
	m.turnTokOut = 0
	m.turnModel = ""
	m.turnCloud = false
	// Fire both the stream drainer and the progress-text animator; both
	// re-issue themselves until streaming ends.
	return m, tea.Batch(waitForStream(ch), progressAnimTick())
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
// append a muted "canceled" note. Any late messages are ignored by the
// streamTickMsg guard once m.streaming is false.
func (m *Model) cancelCurrentStream() {
	if m.cancelStream != nil {
		m.cancelStream()
		m.cancelStream = nil
	}
	m.streaming = false
	m.streamCh = nil
	if e := m.lastAssistantEntry(); e != nil {
		e.Streaming = false
	}
	m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "⊘ canceled"})
	// Esc aborts the train of thought — drop any queued follow-ups too.
	m.queued = nil
	m.relayout()
}

func (m Model) runSlash(line string) (tea.Model, tea.Cmd) {
	res, _ := m.registry.Dispatch(line)
	switch res.Kind {
	case slash.ResultQuit:
		return m, tea.Quit
	case slash.ResultClearConversation:
		m.entries = nil
		m.convID = newConvID()
		if m.convRef != nil {
			m.convRef.id = m.convID
		}
		m.sessionTitle = ""
		m.cumIn = 0
		m.cumOut = 0
		m.focusedToolIdx = -1
		m.refreshViewport()
	case slash.ResultOpenConfigEditor:
		ed, _ := newConfigEditor(m.agent, m.palette, m.styles, m.width, m.height)
		m.content = ed
	case slash.ResultOpenHistoryPicker:
		hp, _ := newHistoryPicker(m.agent, m.palette, m.styles, m.width, m.height, m.convID)
		m.content = hp
	case slash.ResultOpenRuntimeDashboard:
		dashboard, _ := newRuntimeDashboard(m.agent, m.palette, m.styles, m.width, m.height)
		m.content = dashboard
	case slash.ResultOpenContextView:
		cv, cmd := newContextView(m.agent, m.palette, m.styles, m.convID, m.width, m.height)
		m.content = cv
		return m, tea.Batch(cmd, contextRefreshTick())
	case slash.ResultResumeConversation:
		// /resume <id> path — slash already validated against the agent.
		m = m.applyResume(res.Text)
	case slash.ResultSetPromptColor:
		m.promptBorderColor = m.resolvePromptColor(res.Text)
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "prompt color set"})
		m.refreshViewport()
	case slash.ResultSetSessionTitle:
		m.sessionTitle = res.Text
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "renamed to: " + res.Text})
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
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "Permission mode → " + mode})
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
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Muted.Render("running tool:" + res.ToolName)})
			m.refreshViewport()
			return m, invokeToolCmd(m.agent, res.ToolName, res.ToolArgs)
		}
		// W or X — queue confirm.
		tc := &pendingToolCall{Name: res.ToolName, Args: res.ToolArgs, Permission: perm}
		m.pendingConfirm = toolConfirm(tc)
		prompt := m.renderConfirmPrompt(tc)
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: prompt})
		m.refreshViewport()
	case slash.ResultText:
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: res.Text})
		m.refreshViewport()
	}
	return m, nil
}

func (m Model) applyStreamMsg(sm agentclient.StreamMsg) (tea.Model, tea.Cmd) {
	switch sm.Type {
	case agentclient.TypeToken:
		// Append to the open text entry, or start a fresh one if the previous
		// segment was closed by a tool call — so post-tool prose lands BELOW the
		// tools in scrollback rather than in the pre-tool placeholder.
		e := m.streamingTextEntry()
		if e == nil {
			e = &Entry{Role: RoleAssistant, Streaming: true}
			m.entries = append(m.entries, e)
		}
		e.Content += sm.Token
		// Once real tokens arrive, the pre-stream progress note is
		// no longer relevant; clear so the renderer drops it.
		e.Status = ""
		m.turnActivity = "writing"
		m.turnTokOut++ // one delta ≈ one token (approximate live count)
	case agentclient.TypeRouteSelected:
		// Engine chosen for this turn — fills the footer's local/cloud badge.
		m.turnModel = sm.RouteModel
		m.turnCloud = sm.RouteCloud
	case agentclient.TypeProgress:
		m.turnActivity = "routing"
		// Collapse progress messages onto the open (empty) assistant entry's
		// Status field — one line that mutates as the agent advances through
		// phases. Falls back to a normal system entry if there's no open
		// streaming assistant to attach to.
		note := normalizeProgress(sm.Note)
		if e := m.streamingTextEntry(); e != nil && e.Content == "" {
			e.Status = note
		} else {
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: note})
		}
	case agentclient.TypeDone:
		e := m.streamingTextEntry()
		if e == nil && sm.Final != "" {
			// Tools ran but no post-tool tokens streamed; surface the final
			// answer as a fresh entry below them.
			e = &Entry{Role: RoleAssistant}
			m.entries = append(m.entries, e)
		}
		if e != nil {
			// If we never received any tokens, fall back to the full final response.
			if e.Content == "" {
				e.Content = sm.Final
			}
			e.Streaming = false
		}
		// Surface non-fatal notices (e.g. "cloud not configured — answered
		// locally") as a system entry above the assistant content. Sticks
		// the cloud state to NONE so the status bar shows it.
		if sm.Notice != "" {
			m.entries = append(m.entries[:len(m.entries)-1], &Entry{Role: RoleSystem, Content: "⚠ " + sm.Notice}, m.entries[len(m.entries)-1])
			m.cloudState = "NONE"
		} else {
			m.cloudState = "ok"
		}
		m.tokIn = sm.TokIn
		m.tokOut = sm.TokOut
		m.hadTurn = true
		// cumIn/cumOut here are local approximations until the agent
		// answers GetContextUsage below; the RPC's authoritative cumulative
		// total overrides cumIn (Used) on arrival.
		m.cumIn += sm.TokIn
		m.cumOut += sm.TokOut
		if sm.Model != "" {
			m.lastModel = sm.Model
		}
	case agentclient.TypeError:
		if e := m.streamingTextEntry(); e != nil {
			e.Streaming = false
		}
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "stream error: " + sm.Err.Error()})
	case agentclient.TypeToolUseStart:
		// Model just emitted a tool_use block. Close the open assistant text
		// entry first: drop it if it's only the empty "thinking" placeholder,
		// otherwise stop its streaming indicator. Then drop a folded in-progress
		// line so the user sees what's being invoked. Args summary fills in on
		// TypeToolUseStop; result fills in on TypeToolExecComplete.
		m.turnActivity = "running " + sm.ToolName
		if e := m.streamingTextEntry(); e != nil {
			if e.Content == "" {
				m.entries = m.entries[:len(m.entries)-1]
			} else {
				e.Streaming = false
			}
		}
		m.entries = append(m.entries, &Entry{
			Role: RoleSystem,
			Tool: &ToolEntry{
				ToolUseID: sm.ToolUseID,
				ToolName:  sm.ToolName,
				Status:    ToolStatusInProgress,
				StartedAt: time.Now(), // fallback timing anchor until exec-start tightens it
				Folded:    true,
			},
		})
	case agentclient.TypeToolUseStop:
		// Args block finished streaming — humanize the raw call JSON into a
		// readable one-liner. Silent skip if the start event was missed.
		if t := m.findToolEntry(sm.ToolUseID); t != nil {
			t.ArgsSummary = humanizeArgs(t.ToolName, sm.ArgsSummary, m.root, m.home)
		}
	case agentclient.TypeToolExecStart:
		// Server is now running the tool. Re-anchor the timing clock here so the
		// measured duration covers execution, not arg streaming. We already show
		// InProgress from TypeToolUseStart.
		if t := m.findToolEntry(sm.ToolUseID); t != nil {
			t.Status = ToolStatusInProgress
			t.StartedAt = time.Now()
		}
	case agentclient.TypeToolExecComplete:
		// Tool finished — flip status to ✓ or ⚠ and build the result blurb
		// (detail · CLI-measured timing).
		if t := m.findToolEntry(sm.ToolUseID); t != nil {
			if sm.IsError {
				t.Status = ToolStatusError
			} else {
				t.Status = ToolStatusComplete
			}
			t.ResultSummary = humanizeResult(sm.Detail, sm.Summary, sm.IsError, time.Since(t.StartedAt))
		}
	case agentclient.TypePermissionRequired:
		// Server-side tool loop hit a W/X tool and is blocked on a decision.
		// Raise the confirm prompt; the y/n/esc resolver will RPC back via
		// AllowToolCall/DenyToolCall to unblock the loop.
		tc := &pendingToolCall{
			ToolUseID:  sm.ToolUseID,
			Name:       sm.ToolName,
			Args:       sm.ArgsJSON,
			Permission: sm.Tier,
		}
		m.pendingConfirm = toolConfirm(tc)
		prompt := m.renderConfirmPrompt(tc)
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: prompt})
	}
	m.refreshViewport()
	return m, waitForStream(m.streamCh)
}

// toolEntryIndices returns the m.entries positions of every tool-call entry,
// in order. Used by the up/down nav handlers to cycle focus among tool entries
// while skipping prose / system entries.
func (m Model) toolEntryIndices() []int {
	var out []int
	for i, e := range m.entries {
		if e.Tool != nil {
			out = append(out, i)
		}
	}
	return out
}

// findToolEntry returns the ToolEntry whose ToolUseID matches id, or nil if
// no such entry exists. Used by the stream-event handlers to update an
// in-flight tool-call line as it transitions through use-stop → exec-start →
// exec-complete.
func (m Model) findToolEntry(id string) *ToolEntry {
	if id == "" {
		return nil
	}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if t := m.entries[i].Tool; t != nil && t.ToolUseID == id {
			return t
		}
	}
	return nil
}

func (m Model) lastAssistantEntry() *Entry {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Role == RoleAssistant {
			return m.entries[i]
		}
	}
	return nil
}

// streamingTextEntry returns the currently-open assistant text entry: the last
// entry, and only if it is a streaming assistant. Streamed tokens append here.
// Appending any other entry (e.g. a tool call) "closes" it, so the next text
// starts a fresh entry positioned BELOW the tools — keeping scrollback in the
// order things actually happened (pre-tool prose, tool calls, then the answer).
func (m Model) streamingTextEntry() *Entry {
	if n := len(m.entries); n > 0 {
		if e := m.entries[n-1]; e.Role == RoleAssistant && e.Streaming {
			return e
		}
	}
	return nil
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
	if m.viewport.Width() > 0 && !m.contentPageActive() {
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
	if m.recap != "" {
		recapH = 2 // blank spacer line + the recap line itself
	}
	queuedH := len(m.queued) // one row per queued message, rendered above the prompt
	// Size the input first — DynamicHeight re-fits it to the wrapped content at
	// this width; the body claims whatever rows are left.
	m.input.SetWidth(contentW - 4)
	inputH := m.input.Height()
	bodyH := m.height - chromeNoInput - inputH - splashH - suggestH - recapH - queuedH
	if bodyH < 3 {
		bodyH = 3
	}
	m.viewport.SetWidth(contentW - 2) // reserve two right columns: a gap + the scrollbar
	m.viewport.SetHeight(bodyH)
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
	top += m.viewport.Height()
	if m.recap != "" {
		top += 2 // blank spacer line + the recap line
	}
	top += len(m.queued) // queued messages render above the prompt border
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

// refreshViewport rebuilds the viewport content from raw entries at the
// current width. Auto-scrolls to bottom ONLY if the user was already at the
// bottom — preserves scroll position when they've paged up to read history.
func (m *Model) refreshViewport() {
	wasAtBottom := m.viewport.AtBottom()
	var b strings.Builder
	for i, e := range m.entries {
		if i > 0 {
			// A blank line separates the user prompt, tool calls, and assistant
			// output so they don't squish together — but consecutive tool-call
			// entries stay tight as a group.
			if m.entries[i-1].Tool != nil && e.Tool != nil {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(m.renderEntry(e, i))
	}
	content := b.String()
	m.viewportPlainLines = plainLines(content)
	m.viewport.SetContent(content)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) preparePromptInput() Model {
	needsRefresh := m.focusedToolIdx >= 0
	if m.selection.Active {
		m.clearSelection()
	}
	m.selectionNotice = ""
	if needsRefresh {
		m.focusedToolIdx = -1
		m.refreshViewport()
	}
	return m
}

const entryIndent = 2

func (m *Model) renderEntry(e *Entry, idx int) string {
	wrapW := m.viewport.Width()
	if wrapW < 10 {
		wrapW = 10
	}
	textW := wrapW - entryIndent
	if textW < 8 {
		textW = 8
	}
	pad := strings.Repeat(" ", entryIndent)

	// Tool-call entries get their own renderer — folded one-liner with arrow
	// marker + status glyph. Indented to match the prose left-margin so the
	// scrollback's vertical rhythm stays consistent.
	if e.Tool != nil {
		return indentBlock(pad, renderToolEntry(*e.Tool, textW, idx == m.focusedToolIdx))
	}

	switch e.Role {
	case RoleUser:
		// User entries: bullet on the first line, hanging indent on wrapped lines.
		wrapped := lipgloss.NewStyle().Width(textW).Render(e.Content)
		lines := strings.Split(wrapped, "\n")
		for i := range lines {
			if i == 0 {
				lines[i] = m.styles.BufferUserPrompt.Render("▶ ") + lines[i]
			} else {
				lines[i] = pad + lines[i]
			}
		}
		return strings.Join(lines, "\n")

	case RoleAssistant:
		// Pre-text placeholder: no prose yet — show the live turn status inline
		// (activity · elapsed · tokens · engine) where the agent is working.
		if e.Streaming && e.Content == "" {
			activity := m.turnActivity
			if activity == "" {
				activity = "thinking"
			}
			line := turnStatusLine(activity, time.Since(m.turnStart), m.turnTokOut, m.turnModel, m.turnCloud)
			content := animateSpinnerGlyph() + " " + animateLimeSweep(line)
			return indentBlock(pad, content)
		}
		rendered := m.renderAssistantMarkdown(e, textW)
		if e.Streaming {
			rendered += m.styles.Accent.Render(" ⟳")
		}
		return indentBlock(pad, rendered)

	case RoleSystem:
		styled := m.styles.Muted.Render(e.Content)
		wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
		return indentBlock(pad, wrapped)
	}
	return e.Content
}

// renderAssistantMarkdown splits the assistant buffer into completed blocks plus
// a live tail, rendering prose via Glamour and tables via the responsive Table
// renderer. Committed blocks are cached; the tail renders live (with any open
// code fence synthetically closed) so streaming code highlights as it grows.
func (m *Model) renderAssistantMarkdown(e *Entry, textW int) string {
	blocks, tail := render.SplitBlocks(e.Content)
	var parts []string
	for _, b := range blocks {
		s := m.renderMdBlock(b, textW)
		// A blank line before a heading gives it breathing room — but not when
		// the heading is the very first thing in the reply.
		if len(parts) > 0 && isHeadingBlock(b) {
			s = "\n" + s
		}
		parts = append(parts, s)
	}
	if strings.TrimSpace(tail) != "" {
		parts = append(parts, m.md.RenderLive(closeOpenFence(tail), textW))
	}
	return strings.Join(parts, "\n")
}

// isHeadingBlock reports whether a prose block leads with an ATX heading marker.
func isHeadingBlock(b render.MdBlock) bool {
	return b.Kind == render.MdProse && strings.HasPrefix(strings.TrimSpace(b.Raw), "#")
}

func (m *Model) renderMdBlock(b render.MdBlock, textW int) string {
	switch {
	case b.Kind == render.MdTable && b.Table != nil:
		return b.Table.Render(textW, m.styles)
	case b.Kind == render.MdCode:
		body := trimBlankEdgeLines(m.md.Render(b.Raw, textW))
		top := codeRule(b.Lang, textW, m.styles)
		bottom := codeRule("", textW, m.styles)
		return top + "\n" + body + "\n" + bottom
	default:
		return m.md.Render(b.Raw, textW)
	}
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

// applyResume updates the model + the convRef shared with the slash registry,
// then rehydrates scrollback from the persisted turns.
func (m Model) applyResume(conversationID string) Model {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	turns, err := m.agent.ResumeConversation(ctx, conversationID)
	if err != nil {
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "resume failed: " + err.Error()})
		m.refreshViewport()
		return m
	}
	m.convID = conversationID
	if m.convRef != nil {
		m.convRef.id = conversationID
	}
	m.entries = nil
	m.cumIn = 0
	m.cumOut = 0
	m.focusedToolIdx = -1
	m.splashShown = false
	for _, t := range turns {
		role := RoleSystem
		switch t.Role {
		case "user":
			role = RoleUser
		case "assistant":
			role = RoleAssistant
		}
		m.entries = append(m.entries, &Entry{
			Role:    role,
			Content: t.Content,
		})
		m.cumIn += t.TokensIn
		m.cumOut += t.TokensOut
	}
	m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: fmt.Sprintf("⟲ resumed %d turn(s)", len(turns))})
	// Surface the prior session's living recap as a one-line banner + footer.
	if info, err := m.agent.GetConversation(ctx, conversationID); err == nil && info.Recap != "" {
		m.recap = info.Recap
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "Recap: " + info.Recap})
	}
	m.relayout()
	return m
}

// renderConfirmPrompt builds the single-line confirm message shown in
// scrollback while pendingConfirm is set. W-tier renders normally; X-tier
// gets a red ⚠ destructive emphasis.
func (m Model) renderConfirmPrompt(p *pendingToolCall) string {
	head := m.styles.Accent.Render("▸ ")
	if p.Permission == "X" {
		head = m.styles.Error.Render("▸ ⚠ DESTRUCTIVE ")
	}
	summary := p.Name + " " + truncateArgs(p.Args, 80)
	return head +
		m.styles.AgentProse.Render(summary) +
		m.styles.BorderDim.Render("   ·   ") +
		m.styles.Muted.Render("[") +
		m.styles.Accent.Render("y") +
		m.styles.Muted.Render("]es / [") +
		m.styles.Accent.Render("n") +
		m.styles.Muted.Render("]o / [") +
		m.styles.Accent.Render("d") +
		m.styles.Muted.Render("]iff")
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
func toolConfirm(tc *pendingToolCall) *confirmRequest {
	return &confirmRequest{
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Accent.Render("✓ approved — running…")})
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
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Muted.Render("canceled.")})
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
				m.entries = append(m.entries, &Entry{Role: RoleSystem,
					Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
			"D": func(m Model) (Model, tea.Cmd) {
				m.entries = append(m.entries, &Entry{Role: RoleSystem,
					Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
		},
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
		if cv.pane != nil {
			cv.pane.clearQueue()
		}
		m.content = nil
		m.contentScrollbarDragging = false
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.SetValue("")
		return m, cv.pane.Submit(text)
	case "up":
		// Pop the last queued message back into the prompt bar for editing
		// (mirrors d808952 unstageLastQueued behaviour in the main chat).
		if m.input.Value() == "" && cv.pane != nil {
			if msg, ok := cv.pane.unstageLastQueued(); ok {
				m.input.SetValue(msg)
				return m, nil
			}
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

// routeChatMsg routes a chat-pane event to the active *contextView. On
// chatConfirmMsg it appends the assistant message to the pane and raises the
// shared confirm gate. All other events are forwarded to pane.Apply.
func (m Model) routeChatMsg(msg tea.Msg) (Model, tea.Cmd) {
	cv, ok := m.content.(*contextView)
	if !ok {
		return m, nil
	}
	if cm, isConfirm := msg.(chatConfirmMsg); isConfirm {
		cv.pane.appendAssistant(cm.assistant)
		onYes, onNo := cm.onYes, cm.onNo
		m.pendingConfirm = &confirmRequest{
			onYes: func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, onYes },
			onNo:  func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; cv.pane.clearBusy(); return m, onNo },
		}
		return m, progressAnimTick()
	}
	drain := cv.pane.Apply(msg)
	if cv.pane.Busy() {
		return m, tea.Batch(drain, progressAnimTick())
	}
	return m, drain
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
func (m *Model) unstageLastQueued() bool {
	n := len(m.queued)
	if n == 0 {
		return false
	}
	m.input.SetValue(m.queued[n-1])
	m.queued = m.queued[:n-1]
	m.relayout()
	return true
}

// renderQueued draws the messages queued while a response streams, as dimmed
// lines just above the prompt — indented to the content margin, one per line,
// truncated to width. Empty when nothing is queued.
func (m Model) renderQueued() string {
	if len(m.queued) == 0 {
		return ""
	}
	pad := strings.Repeat(" ", entryIndent)
	avail := m.width - entryIndent - 2 // leave room for the "⊕ " marker
	lines := make([]string, len(m.queued))
	for i, q := range m.queued {
		text := q
		if avail > 1 && lipgloss.Width(text) > avail {
			r := []rune(text)
			if len(r) > avail-1 {
				text = string(r[:avail-1]) + "…"
			}
		}
		lines[i] = pad + m.styles.Muted.Render("⊕ "+text)
	}
	return strings.Join(lines, "\n")
}

// renderRecap draws the living one-line work summary at the bottom of the
// chat area, dimmed and truncated to terminal width. Only rendered in the
// default (no-overlay) view.
func (m Model) renderRecap() string {
	const labelText = "recap "
	pad := strings.Repeat(" ", entryIndent)
	// Label in lime (upright) to read as a label; the recap text in bright amber
	// italic so the two are visually distinct.
	labelStyle := lipgloss.NewStyle().Foreground(m.palette.Accent)
	textStyle := lipgloss.NewStyle().Foreground(m.palette.Bright).Italic(true)
	avail := m.width - entryIndent - lipgloss.Width(labelText)
	if avail < 8 {
		return ""
	}
	text := m.recap
	if lipgloss.Width(text) > avail {
		r := []rune(text)
		if len(r) > avail-1 {
			text = string(r[:avail-1]) + "…"
		}
	}
	// Blank line above for breathing room; indented to the content margin.
	return "\n" + pad + labelStyle.Render(labelText) + textStyle.Render(text)
}

// renderViewportWithScrollbar renders the chat viewport with a one-column
// vertical scrollbar on its right edge. The bar paints a thumb (█) + track (░)
// in subtle greys only when the content overflows; otherwise the reserved
// column is blank, so the bar appears and disappears without reflowing text.
func (m Model) renderViewportWithScrollbar() string {
	body := m.viewport.View()
	lines := strings.Split(body, "\n")
	height := m.viewport.Height()
	col := scrollbarColumn(m.viewport.TotalLineCount(), height, m.viewport.YOffset())
	var b strings.Builder
	for i, line := range lines {
		contentLine := m.viewport.YOffset() + i
		line = m.renderSelectionOnLine(line, contentLine)
		// Clamp to the viewport width so an over-wide content line (Glamour
		// pads prose a few columns past the wrap width) can't push the
		// composited row past m.width and wrap in the terminal — which would
		// shove the scrollbar onto a wrapped row and make it vanish.
		line = ansi.Truncate(line, m.viewport.Width(), "")
		b.WriteString(line)
		b.WriteString(" ") // one-column gap so content doesn't touch the scrollbar
		// Guard against any row-count mismatch between the rendered body and
		// the computed column.
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(m.styles.Border.Render("█"))
			case '░':
				b.WriteString(m.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
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
	if m.selection.hasRange() {
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
		m.renderPermissionModeChip(),
		m.styles.BorderDim.Render("  ·  "),
		help,
	}
	return lipgloss.NewStyle().Width(m.width).Render(strings.Join(parts, ""))
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
	bar := m.styles.MeterFill.Render(strings.Repeat("█", fillN)) +
		m.styles.MeterEmpty.Render(strings.Repeat("░", cells-fillN))
	pctStyle := m.styles.Muted
	switch {
	case pct >= 0.9:
		pctStyle = m.styles.Error
	case pct >= 0.7:
		pctStyle = m.styles.Warn
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
	}, "")
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
