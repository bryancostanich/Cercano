package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// ChatDriver plugs an agent into a chatPane. Submit returns a tea.Cmd that emits
// chatPaneMsg events (chatStatusMsg / chatAssistantMsg / chatConfirmMsg /
// chatDoneMsg / chatErrorMsg). The pane is agent-agnostic; all agent specifics
// live in the driver.
type ChatDriver interface {
	Name() string
	Submit(ctx context.Context, input string) tea.Cmd
}

// chatPaneMsg is the closed set of events a driver emits. They are top-level
// tea.Msg values routed by the model to the active pane.
type chatStatusMsg struct{ activity string }
type chatAssistantMsg struct{ text string }
// chatDoneMsg signals the end of a driver turn. text is an optional closing
// line for /c (pane appends it as a system entry). tokIn/tokOut/notice/model
// carry main-chat turn telemetry; the /c driver never sets them so they
// default to zero and are ignored by chatPane.Apply.
type chatDoneMsg struct {
	text   string // /c: optional closing system line
	tokIn  int    // main-chat: input tokens for the completed turn
	tokOut int    // main-chat: output tokens for the completed turn
	notice string // main-chat: non-fatal notice (e.g. "cloud not configured")
	model  string // main-chat: local model name reported by the agent
}
type chatErrorMsg struct{ err error }

// chatConfirmMsg asks the host to raise the shared confirm gate. onYes/onNo are
// the driver's follow-up cmds (e.g. perform the delete, or cancel). The pane
// renders `assistant` as the agent's message; the MODEL raises the confirm
// (it owns m.pendingConfirm) — see model.go routing.
type chatConfirmMsg struct {
	assistant string
	onYes     tea.Cmd
	onNo      tea.Cmd
}

type chatPane struct {
	driver  ChatDriver
	styles  theme.Styles
	palette theme.Palette
	width   int
	height  int

	md           *render.Markdown
	entries      []*Entry
	busy         bool
	activity     string
	started      time.Time
	queued       []string // FIFO; messages submitted while busy (mirrors main chat d808952)
	scrollOffset int
	follow       bool // when true, View pins to the bottom against the CURRENT height
}

func newChatPane(d ChatDriver, s theme.Styles, p theme.Palette, w, h int) *chatPane {
	return &chatPane{
		driver:  d,
		styles:  s,
		palette: p,
		width:   w,
		height:  h,
		md:      render.NewMarkdown(theme.CrackerMarkdownStyle()),
		follow:  true,
	}
}

// maxScroll is the largest valid scroll offset for the current content + height.
func (c *chatPane) maxScroll() int { return maxInt(0, len(c.contentLines())-c.contentHeight()) }

func (c *chatPane) Busy() bool { return c.busy }

func (c *chatPane) SetSize(w, h int) { c.width = w; c.height = h }

// Submit appends the user message, marks the pane busy, and returns the driver's
// cmd batched with the animation tick. While busy it enqueues (FIFO) instead.
func (c *chatPane) Submit(input string) tea.Cmd {
	if c.busy {
		c.queued = append(c.queued, input)
		return nil
	}
	c.entries = append(c.entries, &Entry{Role: RoleUser, Content: input})
	c.scrollToBottom()
	c.busy = true
	c.activity = "working…"
	c.started = time.Now()
	ctx := context.Background()
	return tea.Batch(c.driver.Submit(ctx, input), progressAnimTick())
}

// Apply mutates pane state for a driver event and returns any follow-up cmd
// (notably auto-draining the next queued message when an exchange ends).
// chatConfirmMsg is handled by the model (it raises the confirm gate).
func (c *chatPane) Apply(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case chatStatusMsg:
		c.activity = m.activity
	case chatAssistantMsg:
		c.entries = append(c.entries, &Entry{Role: RoleAssistant, Content: m.text})
		c.scrollToBottom()
	case chatDoneMsg:
		if m.text != "" {
			c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: m.text})
		}
		c.scrollToBottom()
		c.busy = false
		return c.drainNext()
	case chatErrorMsg:
		c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: c.styles.Error.Render("error: " + m.err.Error())})
		c.scrollToBottom()
		c.busy = false
		return c.drainNext()
	}
	return nil
}

// drainNext pops and submits the oldest queued message after an exchange ends.
func (c *chatPane) drainNext() tea.Cmd {
	if len(c.queued) == 0 {
		return nil
	}
	next := c.queued[0]
	c.queued = c.queued[1:]
	return c.Submit(next) // busy is false here, so this starts the exchange
}

// unstageLastQueued pops the most-recently-queued message off the queue and
// returns it (for the host to put back into the prompt for editing). Returns
// "", false when the queue is empty. Mirrors the main chat.
func (c *chatPane) unstageLastQueued() (string, bool) {
	n := len(c.queued)
	if n == 0 {
		return "", false
	}
	last := c.queued[n-1]
	c.queued = c.queued[:n-1]
	return last, true
}

// clearQueue drops all pending messages (cancel/esc).
func (c *chatPane) clearQueue() { c.queued = nil }

// appendAssistant is used by the model when it handles chatConfirmMsg, so the
// agent's pre-confirm message shows in the log.
func (c *chatPane) appendAssistant(text string) {
	if text != "" {
		c.entries = append(c.entries, &Entry{Role: RoleAssistant, Content: text})
	}
}

func (c *chatPane) clearBusy() { c.busy = false }

// renderChatEntry renders one chat Entry with the same primitives as the main
// buffer: assistant content through the markdown engine, user/system styled.
// Free function so both the pane and (later) the main chat can use it.
func renderChatEntry(e *Entry, md *render.Markdown, s theme.Styles, width int) string {
	switch e.Role {
	case RoleAssistant:
		return md.Render(e.Content, width)
	case RoleUser:
		return s.UserPrompt.Render("▶ ") + e.Content
	default: // RoleSystem
		return s.Muted.Render(e.Content)
	}
}

// ScrollBy advances the scroll offset by delta lines (positive = down).
func (c *chatPane) ScrollBy(delta int) {
	c.scrollOffset += delta
	c.clampScroll()
	c.follow = c.scrollOffset >= c.maxScroll()
}

// ScrollTo sets the scroll offset to a specific line.
func (c *chatPane) ScrollTo(offset int) {
	c.scrollOffset = offset
	c.clampScroll()
	c.follow = c.scrollOffset >= c.maxScroll()
}

// ScrollState returns a snapshot of the current scroll geometry.
func (c *chatPane) ScrollState() contentPageScrollState {
	allLines := c.contentLines()
	total := len(allLines)
	contentH := c.contentHeight()
	return contentPageScrollState{
		Total:  total,
		Height: contentH,
		Offset: clampInt(c.scrollOffset, 0, maxInt(0, total-contentH)),
	}
}

func (c *chatPane) clampScroll() { c.scrollOffset = c.ScrollState().Offset }

// scrollToBottom pins the view to the most-recent line. The sentinel value is
// clamped to the real maximum by clampScroll / ScrollState.
func (c *chatPane) scrollToBottom() {
	c.follow = true
	c.scrollOffset = c.maxScroll()
}

// contentHeight is the scrollable area: total height minus the pinned rows
// (1 busy line + 1 per queued item, minimum 1).
func (c *chatPane) contentHeight() int {
	pinned := 0
	if c.busy {
		pinned++
	}
	pinned += len(c.queued)
	h := c.height - pinned
	if h < 1 {
		h = 1
	}
	return h
}

// DesiredHeight reports how many rows the pane wants — its content lines plus the
// pinned status/queued rows. A host (e.g. the /c split view) uses this to size
// the pane's band so it grows with the chat instead of eating the whole panel.
func (c *chatPane) DesiredHeight() int {
	n := len(c.contentLines())
	if c.busy {
		n++
	}
	n += len(c.queued)
	if n < 1 {
		n = 1
	}
	return n
}

// contentLines renders all entries into a flat line slice for windowing.
func (c *chatPane) contentLines() []string {
	contentW := c.width - 2 // reserve 1 col for scrollbar gutter + 1 space
	if contentW < 1 {
		contentW = 1
	}
	var lines []string
	for _, e := range c.entries {
		rendered := renderChatEntry(e, c.md, c.styles, contentW)
		for _, l := range strings.Split(rendered, "\n") {
			lines = append(lines, l)
		}
	}
	return lines
}

// View renders the message log with a scrollbar, plus the pinned status/queued
// rows at the bottom.
func (c *chatPane) View() string {
	contentH := c.contentHeight()
	allLines := c.contentLines()
	total := len(allLines)
	if c.follow {
		c.scrollOffset = maxInt(0, total-contentH)
	} else {
		c.scrollOffset = clampInt(c.scrollOffset, 0, maxInt(0, total-contentH))
	}
	col := scrollbarColumn(total, contentH, c.scrollOffset)
	contentW := c.width - 2
	if contentW < 1 {
		contentW = 1
	}

	var b strings.Builder
	for i := 0; i < contentH; i++ {
		line := ""
		if src := c.scrollOffset + i; src >= 0 && src < total {
			line = allLines[src]
		}
		b.WriteString(padToWidth(ansi.Truncate(line, contentW, ""), contentW))
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(c.styles.Border.Render("█"))
			case '░':
				b.WriteString(c.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		b.WriteString("\n")
	}

	// Pinned rows: busy status then queued messages.
	if c.busy {
		line := c.activity + "  ·  " + time.Since(c.started).Truncate(time.Second).String()
		b.WriteString(animateSpinnerGlyph() + " " + animateLimeSweep(line) + "\n")
	}
	for _, q := range c.queued {
		b.WriteString(c.styles.Muted.Render("⏳ "+q) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
