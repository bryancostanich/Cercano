package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
type chatDoneMsg struct{ text string } // optional closing line; clears busy
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

	entries      []*Entry
	busy         bool
	activity     string
	started      time.Time
	queued       []string // FIFO; messages submitted while busy (mirrors main chat d808952)
	scrollOffset int
}

func newChatPane(d ChatDriver, s theme.Styles, p theme.Palette, w, h int) *chatPane {
	return &chatPane{driver: d, styles: s, palette: p, width: w, height: h}
}

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
	case chatDoneMsg:
		if m.text != "" {
			c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: m.text})
		}
		c.busy = false
		return c.drainNext()
	case chatErrorMsg:
		c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: c.styles.Error.Render("error: " + m.err.Error())})
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

// rolePrefix returns a readable label for a Role value.
func rolePrefix(r Role) string {
	switch r {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	default:
		return "system"
	}
}

// View renders the message log plus, while busy, the animated status line.
// The spinner uses animateSpinnerGlyph; the status text animates with lime
// sweep (animateLimeSweep), matching the UX of the main page.
func (c *chatPane) View() string {
	var b strings.Builder
	for _, e := range c.entries {
		role := c.styles.Muted.Render(rolePrefix(e.Role) + ": ")
		b.WriteString(role + e.Content + "\n")
	}
	if c.busy {
		line := c.activity + "  ·  " + time.Since(c.started).Truncate(time.Second).String()
		b.WriteString(animateSpinnerGlyph() + " " + animateLimeSweep(line) + "\n")
	}
	for _, q := range c.queued {
		b.WriteString(c.styles.Muted.Render("⏳ "+q) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
