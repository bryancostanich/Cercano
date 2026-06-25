package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// turnStatus holds the live streaming telemetry that renderEntry needs while a
// turn is in-flight. Grouped here so chatView can own it rather than Model.
type turnStatus struct {
	activity string
	start    time.Time
	tokOut   int
	model    string
	cloud    bool
}

// chatView owns the transcript viewport: the rendered content, the scroll
// surface, and all entry rendering. Model delegates to it for everything that
// was previously handled via m.viewport, m.viewportPlainLines, and m.md.
type chatView struct {
	styles  theme.Styles
	palette theme.Palette
	md      *render.Markdown

	vp             viewport.Model
	plainLines     []string
	focusedToolIdx int

	turn turnStatus
}

// newChatView constructs a chatView sized to vpWidth × vpHeight. The host
// reserves two columns to the right of this width for the gap and scrollbar.
func newChatView(styles theme.Styles, palette theme.Palette, vpWidth, vpHeight int) *chatView {
	return &chatView{
		styles:         styles,
		palette:        palette,
		md:             render.NewMarkdown(theme.CrackerMarkdownStyle()),
		vp:             viewport.New(viewport.WithWidth(vpWidth), viewport.WithHeight(vpHeight)),
		focusedToolIdx: -1,
	}
}

// SetSize resizes the underlying viewport. Call from relayout.
func (c *chatView) SetSize(w, h int) {
	c.vp.SetWidth(w)
	c.vp.SetHeight(h)
}

// SetFocusedTool sets the tool entry index that is currently keyboard-focused.
// Pass -1 to clear (input has focus).
func (c *chatView) SetFocusedTool(idx int) {
	c.focusedToolIdx = idx
}

// SetTurnStatus updates the live turn telemetry used while a streaming turn is
// in progress.
func (c *chatView) SetTurnStatus(ts turnStatus) {
	c.turn = ts
}

// ── scroll surface ─────────────────────────────────────────────────────────

// Width returns the viewport width.
func (c *chatView) Width() int { return c.vp.Width() }

// Height returns the viewport height.
func (c *chatView) Height() int { return c.vp.Height() }

// TotalLineCount returns the total number of content lines.
func (c *chatView) TotalLineCount() int { return c.vp.TotalLineCount() }

// YOffset returns the current scroll offset.
func (c *chatView) YOffset() int { return c.vp.YOffset() }

// SetYOffset sets the scroll offset.
func (c *chatView) SetYOffset(n int) { c.vp.SetYOffset(n) }

// AtBottom reports whether the viewport is scrolled to the last line.
func (c *chatView) AtBottom() bool { return c.vp.AtBottom() }

// GotoBottom scrolls to the last line.
func (c *chatView) GotoBottom() { c.vp.GotoBottom() }

// PlainLines returns the ANSI-stripped content lines (for selection copy).
func (c *chatView) PlainLines() []string { return c.plainLines }

// SetEntries rebuilds the viewport content from the provided entries and
// auto-scrolls to bottom only if the viewport was already there.
func (c *chatView) SetEntries(entries []*Entry) {
	wasAtBottom := c.vp.AtBottom()
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			// A blank line separates the user prompt, tool calls, and assistant
			// output so they don't squish together — but consecutive tool-call
			// entries stay tight as a group.
			if entries[i-1].Tool != nil && e.Tool != nil {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(c.renderEntry(e, i))
	}
	content := b.String()
	c.plainLines = plainLines(content)
	c.vp.SetContent(content)
	if wasAtBottom {
		c.vp.GotoBottom()
	}
}

// View renders the viewport with a one-column scrollbar using the provided
// selOverlay function to apply selection highlighting per line.
func (c *chatView) View(selOverlay func(line string, contentLine int) string) string {
	body := c.vp.View()
	lines := strings.Split(body, "\n")
	height := c.vp.Height()
	col := scrollbarColumn(c.vp.TotalLineCount(), height, c.vp.YOffset())
	var b strings.Builder
	for i, line := range lines {
		contentLine := c.vp.YOffset() + i
		line = selOverlay(line, contentLine)
		// Clamp to the viewport width so an over-wide content line (Glamour
		// pads prose a few columns past the wrap width) can't push the
		// composited row past m.width and wrap in the terminal — which would
		// shove the scrollbar onto a wrapped row and make it vanish.
		line = ansi.Truncate(line, c.vp.Width(), "")
		b.WriteString(line)
		b.WriteString(" ") // one-column gap so content doesn't touch the scrollbar
		// Guard against any row-count mismatch between the rendered body and
		// the computed column.
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
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ── entry rendering ────────────────────────────────────────────────────────

func (c *chatView) renderEntry(e *Entry, idx int) string {
	wrapW := c.vp.Width()
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
		return indentBlock(pad, renderToolEntry(*e.Tool, textW, idx == c.focusedToolIdx))
	}

	switch e.Role {
	case RoleUser:
		// User entries: lime ▶ marker on a full-width navy fill so prompts stand
		// out when scrolling back. Each wrapped line is padded to the content
		// width, then the navy background spans the whole line (marker + body).
		wrapped := lipgloss.NewStyle().Width(textW).Render(e.Content)
		lines := strings.Split(wrapped, "\n")
		for i := range lines {
			if i == 0 {
				lines[i] = c.styles.BufferUserMarker.Render("▶ ") + c.styles.BufferUserLine.Render(lines[i])
			} else {
				lines[i] = c.styles.BufferUserLine.Render(pad + lines[i])
			}
		}
		return strings.Join(lines, "\n")

	case RoleAssistant:
		// Pre-text placeholder: no prose yet — show the live turn status inline
		// (activity · elapsed · tokens · engine) where the agent is working.
		if e.Streaming && e.Content == "" {
			activity := c.turn.activity
			if activity == "" {
				activity = "thinking"
			}
			line := turnStatusLine(activity, time.Since(c.turn.start), c.turn.tokOut, c.turn.model, c.turn.cloud)
			content := animateSpinnerGlyph() + " " + animateLimeSweep(line)
			return indentBlock(pad, content)
		}
		rendered := c.renderAssistantMarkdown(e, textW)
		if e.Streaming {
			rendered += c.styles.Accent.Render(" ⟳")
		}
		return indentBlock(pad, rendered)

	case RoleSystem:
		styled := c.styles.Muted.Render(e.Content)
		wrapped := lipgloss.NewStyle().Width(textW).Render(styled)
		return indentBlock(pad, wrapped)
	}
	return e.Content
}

// renderAssistantMarkdown splits the assistant buffer into completed blocks plus
// a live tail, rendering prose via Glamour and tables via the responsive Table
// renderer. Committed blocks are cached; the tail renders live (with any open
// code fence synthetically closed) so streaming code highlights as it grows.
func (c *chatView) renderAssistantMarkdown(e *Entry, textW int) string {
	blocks, tail := render.SplitBlocks(e.Content)
	var parts []string
	for _, b := range blocks {
		s := c.renderMdBlock(b, textW)
		// A blank line before a heading gives it breathing room — but not when
		// the heading is the very first thing in the reply.
		if len(parts) > 0 && isHeadingBlock(b) {
			s = "\n" + s
		}
		parts = append(parts, s)
	}
	if strings.TrimSpace(tail) != "" {
		parts = append(parts, c.md.RenderLive(closeOpenFence(tail), textW))
	}
	return strings.Join(parts, "\n")
}

func (c *chatView) renderMdBlock(b render.MdBlock, textW int) string {
	switch {
	case b.Kind == render.MdTable && b.Table != nil:
		return b.Table.Render(textW, c.styles)
	case b.Kind == render.MdCode:
		body := trimBlankEdgeLines(c.md.Render(b.Raw, textW))
		top := codeRule(b.Lang, textW, c.styles)
		bottom := codeRule("", textW, c.styles)
		return top + "\n" + body + "\n" + bottom
	default:
		return c.md.Render(b.Raw, textW)
	}
}
