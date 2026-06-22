// Package ui hosts the Bubble Tea root model for cercano-cli.
//
// Cercano renders inline (not in an alt-screen buffer), so the terminal owns
// scrollback: completed turns, tool entries, and system messages are written
// to the terminal via tea.Println as they finalize and stay in the user's
// terminal history afterward. The View() returns only the live frame —
// in-flight streaming content + the input row + the status footer.
//
// This mirrors how Claude Code (Ink), npm/yarn/pnpm (Ink), and the official
// `bubbletea/examples/package-manager` reference render. Bubble Tea flushes
// tea.Println / tea.Printf above the rendered View only when alt-screen mode
// is OFF (standard_renderer.go's altScreenActive gate), which is why this
// pattern works only without WithAltScreen.
package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"cercano/source/server/internal/cli/agentclient"
	"cercano/source/server/internal/cli/banner"
	"cercano/source/server/internal/cli/render"
	"cercano/source/server/internal/cli/slash"
	"cercano/source/server/internal/cli/theme"
)

// Role tags a scrollback entry's origin so the renderer can style it.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
	RoleSystem // /help output, errors, progress notes
)

// Entry is one item in scrollback. In inline mode entries are short-lived:
// they exist only while in-flight (streaming assistant token by token, or a
// tool call mid-execution). Once finalized they're committed to terminal
// scrollback via tea.Println and the in-memory Entry is discarded.
type Entry struct {
	Role      Role
	Content   string // grows live for streaming assistant turns
	Streaming bool   // true while tokens are flowing in
	// Status is the current pre-stream progress note (e.g. "classifying
	// intent", "selecting provider", "generating response"). Set by
	// progress messages while Content is empty; shown in place of the
	// "thinking…" placeholder. Cleared as soon as tokens start arriving.
	Status string
	// Tables are markdown tables extracted from Content at stream-done.
	// Content carries `{{TABLE_N}}` sentinels; the renderer substitutes them
	// with freshly-rendered Table strings at m.width when committing.
	Tables []render.Table

	// Tool, when non-nil, makes this entry a tool-call line — Role/Content
	// are ignored and renderToolEntry produces the visible row.
	Tool *ToolEntry
}

// Model is the Bubble Tea root model.
type Model struct {
	width, height int

	palette theme.Palette
	styles  theme.Styles

	agent  *agentclient.Client
	convID string

	registry *slash.Registry

	splashShown bool // hide after first user input
	splash      banner.AnimModel
	input       textinput.Model
	streamCh    <-chan agentclient.StreamMsg
	streaming   bool

	// inflightAssistant holds the assistant turn currently being streamed —
	// shown in the live View() above the input. On TypeDone it is rendered
	// and committed to terminal scrollback via tea.Println, then cleared.
	inflightAssistant *Entry

	// inflightTools tracks tool calls whose execution hasn't completed yet.
	// Rendered as folded one-liners above the input. On TypeToolExecComplete
	// the matching entry is Println'd and removed from this slice.
	inflightTools []*Entry

	tokIn, tokOut  int
	cumIn, cumOut  int
	lastLatencyMs  int
	modelMaxTokens int
	lastModel      string // local model name (from config)
	cloudModel     string // cloud model name (from config); empty when no cloud configured
	cloudState     string // "" = unknown, "NONE" = absent, "ok" = real cloud configured
	ctrlCArmed     bool   // first ctrl-c on empty input arms quit; any other key disarms
	errMsg         string

	editorActive bool
	editor       configEditor

	historyActive bool
	history       historyPicker

	recap string // living one-line work summary; shown in the chat footer

	// convRef shares the current convID with the slash registry by reference,
	// so /rename always targets whatever conversation the model currently has
	// active (including after /resume).
	convRef *struct{ id string }

	openHistoryOnStart bool // -r flag → open the history picker after first WindowSizeMsg

	// promptBorderColor is the color of the lines immediately above and
	// below the input row. Defaults to the palette's accent (lime). /color
	// sets it at runtime.
	promptBorderColor lipgloss.Color

	// sessionTitle is the current conversation's display title. Shown as the
	// leftmost element of the status footer. Empty for fresh sessions.
	sessionTitle string

	// toolCache is the registry of available tools, fetched at startup so
	// the CLI can decide locally (no extra RPC) whether to prompt before
	// invoking a tool. Keyed by tool name.
	toolCache map[string]agentclient.ToolInfo

	// pendingConfirm carries a tool invocation waiting on a y/n/d keypress.
	// While non-nil, all key events route to the confirm resolver instead
	// of the input.
	pendingConfirm *pendingToolCall

	// permissionMode caches the agent's current session permission mode
	// ("strict" | "permissive" | "bypass") so the status bar can render a
	// colored chip without an RPC round-trip every frame.
	permissionMode string

	// bannerCommitted ensures the static banner is Println'd to scrollback
	// exactly once when the splash gets dismissed.
	bannerCommitted bool
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

const defaultInputPlaceholder = "type a message, /help for commands"
const armedInputPlaceholder = "(press ^C again to quit, or type a message)"

// New builds the root model. The provided agent client must already be Dial'd.
// openHistoryOnStart=true makes the CLI open the /history picker as soon as
// the terminal size is known (used by the `cercano -r` flag).
func New(ag *agentclient.Client, openHistoryOnStart bool) Model {
	p := theme.Cracker()
	s := theme.NewStyles(p)

	ti := textinput.New()
	ti.Placeholder = defaultInputPlaceholder
	ti.Prompt = s.UserPrompt.Render("▶ ")
	ti.CharLimit = 0
	ti.Focus()

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

	splash := banner.NewAnimModel(p, banner.Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		Model:   "qwen3-coder",
	})

	initialConvID := newConvID()
	convRef.id = initialConvID

	return Model{
		palette:            p,
		styles:             s,
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
	}
}

func newConvID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Init is called by Bubble Tea once at startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.splash.Init(), fetchConfigCmd(m.agent), fetchToolsCmd(m.agent), fetchPermissionModeCmd(m.agent))
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
// the status footer can render both. Called on Init and whenever the config
// editor closes so user edits flow into the footer immediately.
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
		// the correct dimensions on resize.
		if m.editorActive {
			m.editor = m.editor.setSize(m.width, m.height)
		}
		if m.historyActive {
			m.history.width = m.width
			m.history.height = m.height
		}
		// -r boot: open the history picker on the first sized frame.
		if m.openHistoryOnStart && m.width > 0 {
			m.openHistoryOnStart = false
			hp, _ := newHistoryPicker(m.agent, m.palette, m.styles, m.width, m.height, m.convID)
			m.history = hp
			m.historyActive = true
		}
		// Inline mode: terminal owns scrollback. Already-committed lines
		// reflow as the terminal sees fit; we only need to re-lay out the
		// live frame. No ClearScreen — there's no alt-screen to clear.
		return m, nil

	case tea.KeyMsg:
		// Pending confirm gates ALL keys — until the user resolves it, the
		// input and any in-flight slash commands stay dormant.
		if m.pendingConfirm != nil {
			next, cmd := m.resolveConfirmKey(msg.String())
			return next, cmd
		}
		// Overlays swallow keys when active.
		if m.editorActive {
			next, cmd, closed := m.editor.Update(msg)
			m.editor = next
			if closed {
				m.editorActive = false
				// Refresh the status footer's model names — the editor may
				// have just changed local-model / cloud-model / cloud-base-url.
				return m, fetchConfigCmd(m.agent)
			}
			return m, cmd
		}
		if m.historyActive {
			next, cmd, closed := m.history.Update(msg)
			m.history = next
			if closed {
				m.historyActive = false
			}
			return m, cmd
		}
		// Ctrl-C semantics: clear the input first; if input was already
		// empty, arm a quit-on-next-Ctrl-C state. Any other key disarms.
		// Matches bash / python REPL / node convention.
		key := msg.String()
		if key == "ctrl+c" {
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
		if m.ctrlCArmed {
			m.ctrlCArmed = false
			m.input.Placeholder = defaultInputPlaceholder
		}
		// Tab completion for slash commands.
		if key == "tab" {
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
		switch key {
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			wasSplashShown := m.splashShown
			m.splashShown = false
			next, cmd := m.submit(text)
			// Commit the static banner to scrollback now that the splash
			// has been dismissed. tea.Println prints above the live frame.
			if wasSplashShown {
				nm := next.(Model)
				bannerCmd := nm.commitBannerIfNeeded()
				return nm, tea.Batch(bannerCmd, cmd)
			}
			return next, cmd
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case streamTickMsg:
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
		m.recap = msg.recap
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
		return m, m.printlnSystem(body)

	case streamEndMsg:
		m.streaming = false
		// Finalize any still-streaming assistant entry: commit it to
		// scrollback and clear the in-flight slot.
		var cmds []tea.Cmd
		if m.inflightAssistant != nil {
			m.inflightAssistant.Streaming = false
			cmds = append(cmds, tea.Println(m.renderAssistantForScrollback(m.inflightAssistant)))
			m.inflightAssistant = nil
		}
		// Poll the agent for authoritative context-window usage. Result
		// arrives as a ctxUsageMsg and overrides the local cumIn approx.
		cmds = append(cmds, fetchContextUsage(m.agent, m.convID), fetchRecap(m.agent, m.convID))
		return m, tea.Batch(cmds...)

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
		next, cmd := m.applyResume(msg.ConversationID)
		if msg.Title != "" {
			next.sessionTitle = msg.Title
		}
		return next, cmd

	case progressAnimTickMsg:
		// Keep ticking while the in-flight assistant entry is awaiting its
		// first token — that's when the animated status line is visible.
		// The View renders fresh each frame; the tick re-issues so Bubble
		// Tea redraws.
		if e := m.inflightAssistant; e != nil && e.Streaming && e.Content == "" {
			return m, progressAnimTick()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) submit(text string) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(text, "/") {
		next, cmd := m.runSlash(text)
		return next, cmd
	}
	// Commit the user turn to scrollback immediately, then drop an in-flight
	// assistant placeholder so the streaming spinner shows in the live frame.
	userCmd := tea.Println(m.renderUserLine(text))
	m.inflightAssistant = &Entry{Role: RoleAssistant, Content: "", Streaming: true}

	// Pass cwd so the agent prepends .cercano/context.md if present.
	wd, _ := os.Getwd()
	ch, err := m.agent.StreamChat(context.Background(), m.convID, text, wd)
	if err != nil {
		m.errMsg = err.Error()
		m.inflightAssistant = nil
		return m, tea.Batch(userCmd, m.printlnSystem("error: "+err.Error()))
	}
	m.streamCh = ch
	m.streaming = true
	// Fire the stream drainer + the progress-text animator; both re-issue
	// themselves until streaming ends.
	return m, tea.Batch(userCmd, waitForStream(ch), progressAnimTick())
}

func (m Model) runSlash(line string) (tea.Model, tea.Cmd) {
	res, _ := m.registry.Dispatch(line)
	switch res.Kind {
	case slash.ResultQuit:
		return m, tea.Quit
	case slash.ResultClearConversation:
		m.convID = newConvID()
		if m.convRef != nil {
			m.convRef.id = m.convID
		}
		m.sessionTitle = ""
		m.cumIn = 0
		m.cumOut = 0
		m.inflightAssistant = nil
		m.inflightTools = nil
	case slash.ResultOpenConfigEditor:
		ed, _ := newConfigEditor(m.agent, m.palette, m.styles, m.width, m.height)
		m.editor = ed
		m.editorActive = true
	case slash.ResultOpenHistoryPicker:
		hp, _ := newHistoryPicker(m.agent, m.palette, m.styles, m.width, m.height, m.convID)
		m.history = hp
		m.historyActive = true
	case slash.ResultResumeConversation:
		// /resume <id> path — slash already validated against the agent.
		return m.applyResume(res.Text)
	case slash.ResultSetPromptColor:
		m.promptBorderColor = m.resolvePromptColor(res.Text)
		return m, m.printlnSystem("prompt color set")
	case slash.ResultSetSessionTitle:
		m.sessionTitle = res.Text
		return m, m.printlnSystem("renamed to: " + res.Text)
	case slash.ResultSetPermissionMode:
		// Fire-and-forget: server persistence is the source of truth, but the
		// local cache flips immediately so the status-bar chip reflects the
		// new mode on the very next frame.
		mode := res.PermissionMode
		ag := m.agent
		go func() {
			_ = ag.SetPermissionMode(context.Background(), mode)
		}()
		m.permissionMode = mode
		return m, m.printlnSystem("Permission mode → " + mode)
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
			return m, tea.Batch(
				m.printlnSystem(m.styles.Muted.Render("running tool:"+res.ToolName)),
				invokeToolCmd(m.agent, res.ToolName, res.ToolArgs),
			)
		}
		// W or X — queue confirm. The confirm prompt lives in the live
		// frame (rendered above the input) so the user can see and act
		// on it directly.
		m.pendingConfirm = &pendingToolCall{Name: res.ToolName, Args: res.ToolArgs, Permission: perm}
	case slash.ResultText:
		return m, m.printlnSystem(res.Text)
	}
	return m, nil
}

func (m Model) applyStreamMsg(sm agentclient.StreamMsg) (tea.Model, tea.Cmd) {
	switch sm.Type {
	case agentclient.TypeToken:
		if e := m.inflightAssistant; e != nil {
			e.Content += sm.Token
			// Once real tokens arrive, the pre-stream progress note is
			// no longer relevant; clear so the renderer drops it.
			e.Status = ""
		}
	case agentclient.TypeProgress:
		// Collapse progress messages onto the live assistant entry's Status
		// field — one line that mutates as the agent advances through phases
		// (classifying intent → selecting provider → generating response).
		// Falls back to a Println'd system entry if there's no streaming
		// assistant to attach to.
		note := normalizeProgress(sm.Note)
		if e := m.inflightAssistant; e != nil && e.Streaming && e.Content == "" {
			e.Status = note
		} else {
			return m, tea.Batch(m.printlnSystem(note), waitForStream(m.streamCh))
		}
	case agentclient.TypeDone:
		var cmds []tea.Cmd
		// Surface non-fatal notices (e.g. "cloud not configured — answered
		// locally") above the assistant body. Sticks the cloud state to
		// NONE so the status bar shows it.
		if sm.Notice != "" {
			cmds = append(cmds, m.printlnSystem("⚠ "+sm.Notice))
			m.cloudState = "NONE"
		} else {
			m.cloudState = "ok"
		}
		if e := m.inflightAssistant; e != nil {
			// If we never received any tokens, fall back to the full final response.
			if e.Content == "" {
				e.Content = sm.Final
			}
			e.Streaming = false
			// Extract markdown tables into Entry.Tables; Content keeps the
			// `{{TABLE_N}}` sentinels. renderAssistantForScrollback resolves
			// them at commit time.
			cleaned, tables := render.InterceptMarkdownTables(e.Content)
			e.Content = cleaned
			e.Tables = tables
			cmds = append(cmds, tea.Println(m.renderAssistantForScrollback(e)))
			m.inflightAssistant = nil
		}
		m.tokIn = sm.TokIn
		m.tokOut = sm.TokOut
		// cumIn/cumOut here are local approximations until the agent
		// answers GetContextUsage in streamEndMsg.
		m.cumIn += sm.TokIn
		m.cumOut += sm.TokOut
		if sm.Model != "" {
			m.lastModel = sm.Model
		}
		cmds = append(cmds, waitForStream(m.streamCh))
		return m, tea.Batch(cmds...)
	case agentclient.TypeError:
		errCmd := m.printlnSystem("stream error: " + sm.Err.Error())
		if e := m.inflightAssistant; e != nil {
			e.Streaming = false
		}
		return m, tea.Batch(errCmd, waitForStream(m.streamCh))
	case agentclient.TypeToolUseStart:
		// Model just emitted a tool_use block. Track the entry in the
		// in-flight list so the View renders a folded in-progress line
		// above the input until exec completes.
		m.inflightTools = append(m.inflightTools, &Entry{
			Role: RoleSystem,
			Tool: &ToolEntry{
				ToolUseID: sm.ToolUseID,
				ToolName:  sm.ToolName,
				Status:    ToolStatusInProgress,
				Folded:    true,
			},
		})
	case agentclient.TypeToolUseStop:
		// Args block finished streaming — attach the summary to the existing
		// entry. Silent skip if the start event was missed.
		if t := m.findInflightTool(sm.ToolUseID); t != nil {
			t.ArgsSummary = sm.ArgsSummary
		}
	case agentclient.TypeToolExecStart:
		// Server is now running the tool. We already show InProgress from
		// TypeToolUseStart; nothing to do unless the start was missed.
		if t := m.findInflightTool(sm.ToolUseID); t != nil {
			t.Status = ToolStatusInProgress
		}
	case agentclient.TypeToolExecComplete:
		// Tool finished — flip status, attach the result summary, commit
		// the folded line to scrollback, and remove from in-flight.
		var cmd tea.Cmd
		for i, e := range m.inflightTools {
			if e.Tool != nil && e.Tool.ToolUseID == sm.ToolUseID {
				if sm.IsError {
					e.Tool.Status = ToolStatusError
				} else {
					e.Tool.Status = ToolStatusComplete
				}
				e.Tool.ResultSummary = sm.Summary
				cmd = tea.Println(m.renderToolForScrollback(e.Tool))
				m.inflightTools = append(m.inflightTools[:i], m.inflightTools[i+1:]...)
				break
			}
		}
		if cmd != nil {
			return m, tea.Batch(cmd, waitForStream(m.streamCh))
		}
	case agentclient.TypePermissionRequired:
		// Server-side tool loop hit a W/X tool and is blocked on a decision.
		// Raise the confirm prompt; the y/n/esc resolver will RPC back via
		// AllowToolCall/DenyToolCall to unblock the loop. The prompt lives
		// in the live View() above the input.
		m.pendingConfirm = &pendingToolCall{
			ToolUseID:  sm.ToolUseID,
			Name:       sm.ToolName,
			Args:       sm.ArgsJSON,
			Permission: sm.Tier,
		}
	}
	return m, waitForStream(m.streamCh)
}

// findInflightTool returns the ToolEntry whose ToolUseID matches id, or nil if
// no such entry exists.
func (m Model) findInflightTool(id string) *ToolEntry {
	if id == "" {
		return nil
	}
	for i := len(m.inflightTools) - 1; i >= 0; i-- {
		if t := m.inflightTools[i].Tool; t != nil && t.ToolUseID == id {
			return t
		}
	}
	return nil
}

// relayout sets the input width to match the current terminal width minus
// the indent gutter. In inline mode there's no viewport to size — the live
// frame is just the input row plus the status footer, and scrollback is
// owned by the terminal.
func (m *Model) relayout() {
	contentW := m.width
	if contentW < 20 {
		contentW = 20
	}
	m.input.Width = contentW - 4
}

// splashEffective reports whether the splash banner is currently showable.
// We hide it on terminals narrower than the banner's fixed width because
// the 62-col chrome wraps catastrophically on a 40-col terminal — every
// banner row becomes 2-3 terminal rows, the whole layout fragments.
func (m Model) splashEffective() bool {
	return m.splashShown && m.width >= banner.Width
}

const entryIndent = 2

// renderUserLine renders a user turn for commit to terminal scrollback.
// Same visual treatment as the previous in-viewport rendering: bullet on
// the first line, hanging indent on wrapped lines.
func (m Model) renderUserLine(text string) string {
	wrapW, textW, pad := m.bodyMetrics()
	_ = wrapW
	wrapped := lipgloss.NewStyle().Width(textW).Render(text)
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = m.styles.UserPrompt.Render("▶ ") + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// renderAssistantForScrollback formats an assistant Entry for tea.Println.
// Resolves any {{TABLE_N}} sentinels at the current terminal width.
func (m Model) renderAssistantForScrollback(e *Entry) string {
	_, textW, pad := m.bodyMetrics()
	content := e.Content
	if len(e.Tables) > 0 {
		for i, t := range e.Tables {
			marker := fmt.Sprintf("{{TABLE_%d}}", i)
			rendered := t.Render(textW, m.styles)
			content = strings.Replace(content, marker, rendered, 1)
		}
		// Skip outer wrap; tables already fit textW.
		return indentBlock(pad, m.styles.AgentProse.Render(content))
	}
	styled := m.styles.AgentProse.Render(content)
	wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
	return indentBlock(pad, wrapped)
}

// renderSystemLine formats a system message for tea.Println at current width.
func (m Model) renderSystemLine(content string) string {
	_, textW, pad := m.bodyMetrics()
	styled := m.styles.Muted.Render(content)
	wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
	return indentBlock(pad, wrapped)
}

// printlnSystem is a small convenience that wraps tea.Println with the
// same system-message styling the old viewport rendering used.
func (m Model) printlnSystem(content string) tea.Cmd {
	return tea.Println(m.renderSystemLine(content))
}

// renderToolForScrollback formats a (folded) tool entry for tea.Println.
func (m Model) renderToolForScrollback(t *ToolEntry) string {
	_, textW, pad := m.bodyMetrics()
	t.Folded = true
	return indentBlock(pad, renderToolEntry(*t, textW, false))
}

// commitBannerIfNeeded Println's the static banner to terminal scrollback
// the first time the splash is dismissed. Idempotent: subsequent calls do
// nothing.
func (m *Model) commitBannerIfNeeded() tea.Cmd {
	if m.bannerCommitted {
		return nil
	}
	m.bannerCommitted = true
	if !(m.width >= banner.Width) {
		return nil
	}
	static := banner.Render(m.palette, banner.Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		Model:   "qwen3-coder",
	})
	return tea.Println(static + "\n")
}

// bodyMetrics returns the wrap width, the inner text width (after the
// gutter), and the gutter pad string used by every entry renderer.
func (m Model) bodyMetrics() (wrapW, textW int, pad string) {
	wrapW = m.width
	if wrapW < 10 {
		wrapW = 10
	}
	textW = wrapW - entryIndent
	if textW < 8 {
		textW = 8
	}
	pad = strings.Repeat(" ", entryIndent)
	return
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
func progressColorAt(col int, sweepPos float64, tail float64) lipgloss.Color {
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

// resolvePromptColor maps a slash-command color token into a lipgloss.Color
// the View can apply directly. Tokens take one of two shapes:
//
//   - `#RRGGBB` — literal hex, used as-is
//   - `palette:<key>` — looked up against the model's palette
//
// Falls back to the current value (silently — the slash command already
// validated; this is just the model-side dispatch).
func (m Model) resolvePromptColor(token string) lipgloss.Color {
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

// applyResume calls the agent's ResumeConversation RPC, switches the active
// conversation id, dismisses the splash, and commits the persisted turns to
// terminal scrollback so the user picks up where they left off.
func (m Model) applyResume(conversationID string) (Model, tea.Cmd) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	turns, err := m.agent.ResumeConversation(ctx, conversationID)
	if err != nil {
		return m, m.printlnSystem("resume failed: " + err.Error())
	}
	m.convID = conversationID
	if m.convRef != nil {
		m.convRef.id = conversationID
	}
	m.cumIn = 0
	m.cumOut = 0
	m.inflightAssistant = nil
	m.inflightTools = nil
	wasSplashShown := m.splashShown
	m.splashShown = false

	var cmds []tea.Cmd
	if wasSplashShown {
		if c := m.commitBannerIfNeeded(); c != nil {
			cmds = append(cmds, c)
		}
	}
	for _, t := range turns {
		switch t.Role {
		case "user":
			cmds = append(cmds, tea.Println(m.renderUserLine(t.Content)))
		case "assistant":
			cleaned, tables := render.InterceptMarkdownTables(t.Content)
			e := &Entry{Role: RoleAssistant, Content: cleaned, Tables: tables}
			cmds = append(cmds, tea.Println(m.renderAssistantForScrollback(e)))
		default:
			cmds = append(cmds, m.printlnSystem(t.Content))
		}
		m.cumIn += t.TokensIn
		m.cumOut += t.TokensOut
	}
	cmds = append(cmds, m.printlnSystem(fmt.Sprintf("⟲ resumed %d turn(s)", len(turns))))
	if info, err := m.agent.GetConversation(ctx, conversationID); err == nil && info.Recap != "" {
		m.recap = info.Recap
		cmds = append(cmds, m.printlnSystem("Recap: "+info.Recap))
	}
	return m, tea.Batch(cmds...)
}

// renderConfirmPrompt builds the single-line confirm message shown above the
// input while pendingConfirm is set. W-tier renders normally; X-tier gets a
// red ⚠ destructive emphasis.
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

// resolveConfirmKey processes a keystroke while a confirm is pending. y → run,
// n / esc → cancel, d → reveal the full args, anything else → ignored.
//
// Two pending-confirm origins are supported:
//
//  1. PermissionRequired stream event (ToolUseID set) — the server-side tool
//     loop is blocked waiting on a decision. y/n RPCs back via
//     AllowToolCall/DenyToolCall so the loop unblocks; we don't call
//     InvokeTool here because the server already has the call queued.
//
//  2. Local `/tool <name>` invocation (ToolUseID empty) — the legacy local
//     flow; y fires invokeToolCmd directly, n drops the request.
func (m Model) resolveConfirmKey(key string) (Model, tea.Cmd) {
	switch key {
	case "y", "Y":
		pending := m.pendingConfirm
		m.pendingConfirm = nil
		approveMsg := m.printlnSystem(m.styles.Accent.Render("✓ approved — running…"))
		if pending.ToolUseID != "" {
			// Stream-event origin: unblock the server-side tool loop. The
			// server resumes execution and surfaces results through the same
			// stream, so we return no invoke cmd here.
			ag := m.agent
			id := pending.ToolUseID
			if ag != nil {
				go func() { _ = ag.AllowToolCall(context.Background(), id) }()
			}
			return m, approveMsg
		}
		// Local /tool origin: fire the invoke directly.
		return m, tea.Batch(approveMsg, invokeToolCmd(m.agent, pending.Name, pending.Args))
	case "n", "N", "esc", "ctrl+c":
		pending := m.pendingConfirm
		m.pendingConfirm = nil
		cancelMsg := m.printlnSystem(m.styles.Muted.Render("canceled."))
		if pending != nil && pending.ToolUseID != "" {
			ag := m.agent
			id := pending.ToolUseID
			if ag != nil {
				go func() { _ = ag.DenyToolCall(context.Background(), id) }()
			}
		}
		return m, cancelMsg
	case "d", "D":
		// Show the full args JSON so the user can inspect before approving.
		return m, m.printlnSystem("args:\n```json\n" + m.pendingConfirm.Args + "\n```")
	}
	// Any other key is ignored while the confirm is pending.
	return m, nil
}

// truncateArgs renders the JSON args compactly for the confirm prompt one-liner.
func truncateArgs(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// abbreviateModel produces a short display name for the status footer:
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

// View renders the live frame at the bottom of the terminal. Inline mode:
// the terminal owns scrollback (committed via tea.Println), the View()
// only shows in-flight content + input row + status footer. Nothing renders
// below the status, which is why the status visually pins to the bottom.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "" // first paint before WindowSizeMsg
	}

	// Overlays take the whole live frame — same behavior as before, just
	// without the header band on top.
	if m.editorActive {
		return m.editor.View()
	}
	if m.historyActive {
		return m.history.View()
	}

	var parts []string

	// Splash plays in the live frame on startup. Once dismissed (first user
	// input or /resume) the static banner is Println'd to scrollback by
	// commitBannerIfNeeded.
	if m.splashEffective() {
		parts = append(parts, m.splash.View())
		parts = append(parts, "")
	}

	// In-flight streaming assistant content. Lives in the live frame until
	// TypeDone commits it to scrollback.
	if e := m.inflightAssistant; e != nil {
		parts = append(parts, m.renderLiveAssistant(e))
	}
	// In-flight tool entries: rendered as folded one-liners above the input
	// until exec completes and they get Println'd.
	for _, e := range m.inflightTools {
		if e.Tool != nil {
			_, textW, pad := m.bodyMetrics()
			parts = append(parts, indentBlock(pad, renderToolEntry(*e.Tool, textW, false)))
		}
	}
	// Confirm prompt: rendered above the input while pendingConfirm is set
	// so the user can read it together with the y/n hint.
	if m.pendingConfirm != nil {
		_, _, pad := m.bodyMetrics()
		parts = append(parts, indentBlock(pad, m.renderConfirmPrompt(m.pendingConfirm)))
	}
	if m.recap != "" {
		parts = append(parts, m.renderRecap())
	}

	promptLine := lipgloss.NewStyle().Foreground(m.promptBorderColor).Render(strings.Repeat("─", m.width))
	parts = append(parts, promptLine)
	if hint := m.renderSlashSuggestions(); hint != "" {
		parts = append(parts, hint)
	}
	parts = append(parts, m.input.View())
	parts = append(parts, promptLine)
	parts = append(parts, m.renderStatus())

	return strings.Join(parts, "\n")
}

// renderLiveAssistant draws the in-flight streaming assistant entry for the
// live View frame. While awaiting the first token, shows the spinner + the
// animated progress status. While streaming tokens, shows the accumulated
// content plus a trailing accent glyph.
func (m Model) renderLiveAssistant(e *Entry) string {
	_, textW, pad := m.bodyMetrics()
	content := e.Content
	if e.Streaming && content == "" {
		status := e.Status
		if status == "" {
			status = "thinking…"
		}
		content = animateSpinnerGlyph() + " " + animateLimeSweep(status)
	} else if e.Streaming {
		content = e.Content + m.styles.Accent.Render(" ⟳")
	}
	styled := m.styles.AgentProse.Render(content)
	wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
	return indentBlock(pad, wrapped)
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

// renderRecap draws the living one-line work summary just above the input
// border, dimmed and truncated to terminal width. Only rendered when set.
func (m Model) renderRecap() string {
	label := m.styles.Muted.Render("recap ")
	avail := m.width - lipgloss.Width(label)
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
	return label + m.styles.BorderDim.Render(text)
}

// renderStatus renders the status footer at the bottom of the live frame.
// It absorbs the session title (when set) plus the cloud + local model strip
// that used to live in the header, since inline mode has no header band.
func (m Model) renderStatus() string {
	if m.streaming {
		return lipgloss.NewStyle().Width(m.width).Render(m.styles.Accent.Render("⟳ streaming"))
	}
	cloudPart := ""
	switch m.cloudState {
	case "NONE":
		cloudPart = m.styles.BorderDim.Render("  ·  ") + m.styles.Muted.Render("cloud:") + m.styles.Error.Render(" NONE")
	case "ok":
		cloudPart = m.styles.BorderDim.Render("  ·  ") + m.styles.Muted.Render("cloud:") + m.styles.Success.Render(" ok")
	}
	titlePart := ""
	if m.sessionTitle != "" {
		titlePart = m.styles.Info.Render(m.sessionTitle) + m.styles.BorderDim.Render("  ·  ")
	}
	modelPart := m.styles.BorderDim.Render("  ·  ") + m.styles.Muted.Render("l:") + m.styles.Accent.Render(abbreviateModel(m.lastModel))
	if m.cloudModel != "" {
		modelPart = m.styles.BorderDim.Render("  ·  ") +
			m.styles.Muted.Render("c:") + m.styles.Accent.Render(abbreviateModel(m.cloudModel)) +
			m.styles.BorderDim.Render(" │ ") +
			m.styles.Muted.Render("l:") + m.styles.Accent.Render(abbreviateModel(m.lastModel))
	}
	parts := []string{
		titlePart,
		m.renderContextMeter(),
		m.styles.BorderDim.Render("  ·  "),
		m.styles.Muted.Render(fmt.Sprintf("turn %d↑/%d↓", m.tokIn, m.tokOut)),
		cloudPart,
		m.renderPermissionModeChip(),
		modelPart,
		m.styles.BorderDim.Render("  ·  "),
		m.styles.Muted.Render("/help for cmds"),
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
