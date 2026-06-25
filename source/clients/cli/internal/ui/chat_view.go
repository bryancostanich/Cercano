package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

	turn              turnStatus
	selection         textSelection
	scrollbarDragging bool

	// dragMouse is the last pointer position during a text-selection drag,
	// stored in LOCAL coordinates (host subtracts scrollbarTop before forwarding).
	// dragScrolling is true while the edge auto-scroll tick loop is running.
	dragMouse     tea.Mouse
	dragScrolling bool
}

// dragScrollTickMsg drives continuous auto-scroll while a selection drag is
// held past the viewport's top/bottom edge (no mouse motion required).
type dragScrollTickMsg struct{}

func dragScrollTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return dragScrollTickMsg{} })
}

// atScrollEdge reports whether the last drag pointer position (local coords)
// sits past the top or bottom edge of the viewport.
func (c *chatView) atScrollEdge() bool {
	row := c.dragMouse.Y // dragMouse is already in local coords
	return row < 0 || row >= c.Height()
}

// newChatView constructs a chatView sized to vpWidth × vpHeight. The host
// reserves two columns to the right of this width for the gap and scrollbar.
func newChatView(styles theme.Styles, palette theme.Palette, vpWidth, vpHeight int) chatView {
	return chatView{
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

// ScrollUp scrolls the viewport up by n lines.
func (c *chatView) ScrollUp(n int) { c.vp.ScrollUp(n) }

// ScrollDown scrolls the viewport down by n lines.
func (c *chatView) ScrollDown(n int) { c.vp.ScrollDown(n) }

// Update passes a bubbletea message to the underlying viewport and returns any
// resulting command. Callers outside chat_view.go must use this instead of
// accessing vp directly.
func (c *chatView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	return cmd
}

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

// View renders the viewport with a one-column scrollbar, applying selection
// highlighting internally.
func (c *chatView) View() string {
	body := c.vp.View()
	lines := strings.Split(body, "\n")
	height := c.vp.Height()
	col := scrollbarColumn(c.vp.TotalLineCount(), height, c.vp.YOffset())
	var b strings.Builder
	for i, line := range lines {
		contentLine := c.vp.YOffset() + i
		line = c.renderSelectionOnLine(line, contentLine)
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

// ── selection surface ──────────────────────────────────────────────────────

// renderSelectionOnLine applies selection highlighting to one rendered line.
func (c *chatView) renderSelectionOnLine(line string, contentLine int) string {
	start, end, ok := c.selection.lineRange(contentLine, c.vp.Width())
	if !ok {
		return line
	}
	return highlightRange(line, start, end)
}

// selectedText returns the plain-text content covered by the current selection.
func (c *chatView) selectedText() string {
	plainLns := c.plainLines
	if !c.selection.hasRange() || len(plainLns) == 0 {
		return ""
	}
	start, end := c.selection.ordered()
	start.Line = clampInt(start.Line, 0, len(plainLns)-1)
	end.Line = clampInt(end.Line, 0, len(plainLns)-1)
	if beforePoint(end, start) {
		return ""
	}

	parts := make([]string, 0, end.Line-start.Line+1)
	for line := start.Line; line <= end.Line; line++ {
		text := plainLns[line]
		switch {
		case start.Line == end.Line:
			parts = append(parts, ansi.Cut(text, start.Col, end.Col))
		case line == start.Line:
			parts = append(parts, ansi.Cut(text, start.Col, ansi.StringWidth(text)))
		case line == end.Line:
			parts = append(parts, ansi.Cut(text, 0, end.Col))
		default:
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// selectionPointFromLocal converts a LOCAL coordinate (row already relative to
// the viewport top, i.e. host has subtracted scrollbarTop) to a selectionPoint.
// If allowScroll is true and the row is out of bounds, the viewport is scrolled
// one line in the appropriate direction.
func (c *chatView) selectionPointFromLocal(localX, localY int, allowScroll bool) selectionPoint {
	height := c.vp.Height()
	row := localY
	if allowScroll {
		switch {
		case row < 0:
			c.vp.ScrollUp(1)
			row = 0
		case row >= height:
			c.vp.ScrollDown(1)
			row = height - 1
		}
	}
	row = clampInt(row, 0, maxInt(0, height-1))
	line := c.vp.YOffset() + row
	if len(c.plainLines) > 0 {
		line = clampInt(line, 0, len(c.plainLines)-1)
	}
	return selectionPoint{
		Line: line,
		Col:  clampInt(localX, 0, c.vp.Width()),
	}
}

// MouseInText reports whether a LOCAL coordinate falls inside the viewport text
// region (not on the scrollbar column).
func (c *chatView) MouseInText(localX, localY int) bool {
	return localX >= 0 &&
		localX < c.vp.Width() &&
		localY >= 0 &&
		localY < c.vp.Height()
}

// ClearSelection resets the selection state.
func (c *chatView) ClearSelection() {
	c.selection = textSelection{}
}

// SelectionActive reports whether a selection is active.
func (c *chatView) SelectionActive() bool { return c.selection.Active }

// SelectionHasRange reports whether the selection covers a non-empty range.
func (c *chatView) SelectionHasRange() bool { return c.selection.hasRange() }

// SelectionDragging reports whether a selection drag is in progress.
func (c *chatView) SelectionDragging() bool { return c.selection.Dragging }

// ── scrollbar drag surface ─────────────────────────────────────────────────

// ScrollbarHit reports whether a local point is on the grabbable scrollbar
// column. The bar sits at the viewport's right edge (column Width()); accept
// that column and one past — terminals sometimes report the rightmost visible
// cell as Width() or Width()+1.
func (c *chatView) ScrollbarHit(localX, localY int) bool {
	return localX >= c.vp.Width() && localY >= 0 && localY < c.vp.Height()
}

// scrollbarScrub jumps the scroll offset to match the local click row.
func (c *chatView) scrollbarScrub(localY int) {
	off := scrollOffsetFromClick(localY, 0, c.vp.Height(), c.vp.TotalLineCount())
	c.vp.SetYOffset(off)
}

// ScrollbarDragging reports whether a scrollbar drag is in progress.
func (c *chatView) ScrollbarDragging() bool { return c.scrollbarDragging }

// StopScrollbarDrag clears the scrollbar drag flag.
func (c *chatView) StopScrollbarDrag() { c.scrollbarDragging = false }

// ClearSelectionDrag clears the selection's Dragging flag without clearing the
// selection itself. Used when a competing gesture (pending confirm, etc.) must
// cancel an in-progress selection drag.
func (c *chatView) ClearSelectionDrag() { c.selection.Dragging = false }

// MouseDrag handles a drag motion event in local viewport coordinates.
// If a scrollbar drag is active it scrubs; if a selection drag is active it
// extends the selection and starts the edge auto-scroll tick when needed.
// Returns the tick cmd (or nil) so the host can return it directly.
func (c *chatView) MouseDrag(localX, localY int) tea.Cmd {
	if c.scrollbarDragging {
		c.scrollbarScrub(localY)
		return nil
	}
	if !c.selection.Dragging {
		return nil
	}
	c.dragMouse = tea.Mouse{X: localX, Y: localY}
	c.updateSelection(localX, localY, true)
	if c.atScrollEdge() && !c.dragScrolling {
		c.dragScrolling = true
		return dragScrollTick()
	}
	return nil
}

// DragScrollTick is called by the host when a dragScrollTickMsg arrives.
// It continues the edge auto-scroll loop while the drag is held at an edge.
// Returns (cmd, true) when the loop reschedules, (nil, false) when it stops.
func (c *chatView) DragScrollTick() (tea.Cmd, bool) {
	if !c.selection.Dragging || !c.atScrollEdge() {
		c.dragScrolling = false
		return nil, false
	}
	c.updateSelection(c.dragMouse.X, c.dragMouse.Y, true)
	return dragScrollTick(), true
}

// StopDragScroll clears the edge auto-scroll flag.
func (c *chatView) StopDragScroll() { c.dragScrolling = false }

// DragScrolling reports whether the edge auto-scroll tick loop is active.
func (c *chatView) DragScrolling() bool { return c.dragScrolling }

// Wheel scrolls the viewport up or down by promptWheelDelta lines.
// Provisional for step-4 reuse; the host's chat-wheel path keeps Update(msg)
// for byte-identical behavior (zero behavior change for now).
func (c *chatView) Wheel(up bool) {
	if up {
		c.ScrollUp(promptWheelDelta)
	} else {
		c.ScrollDown(promptWheelDelta)
	}
}

// selectionEmpty reports whether the selection covers no range.
func (c *chatView) selectionEmpty() bool { return c.selection.empty() }

// beginSelection starts a new selection drag anchored at localX/localY.
func (c *chatView) beginSelection(localX, localY int) {
	pt := c.selectionPointFromLocal(localX, localY, false)
	c.selection = textSelection{
		Active:   true,
		Dragging: true,
		Anchor:   pt,
		Cursor:   pt,
	}
}

// updateSelection extends the selection cursor to localX/localY (host has
// already translated from screen coords). If allowScroll is true and the row
// is out of bounds, the viewport is scrolled one line in the appropriate
// direction.
func (c *chatView) updateSelection(localX, localY int, allowScroll bool) {
	if !c.selection.Active {
		return
	}
	c.selection.Cursor = c.selectionPointFromLocal(localX, localY, allowScroll)
}

// MouseDown is the unified handler for a left-click inside the viewport region.
// localX/localY are already translated to viewport-local coordinates (host
// subtracts scrollbarTop from Y).
func (c *chatView) MouseDown(localX, localY int) {
	if c.ScrollbarHit(localX, localY) {
		// A bar grab is a scroll gesture, not a text selection; cancel any
		// in-progress selection drag so it can't hijack subsequent motion.
		c.selection.Dragging = false
		c.scrollbarDragging = true
		c.scrollbarScrub(localY)
		return
	}
	if c.MouseInText(localX, localY) {
		c.scrollbarDragging = false
		c.beginSelection(localX, localY)
		return
	}
	c.ClearSelection()
}

// HandleSelectionKey handles a key press while a selection is active.
// Returns (cmd, handled, copied): cmd is a clipboard cmd or nil; handled=true
// means the host should not process the key further; copied=true means the host
// should set its status notice.
func (c *chatView) HandleSelectionKey(msg tea.KeyPressMsg) (tea.Cmd, bool, bool) {
	switch msg.String() {
	case "esc":
		c.ClearSelection()
		return nil, true, false
	case "enter", "c", "y", "ctrl+c":
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if isSelectionCopyKey(msg) {
		text := c.selectedText()
		c.ClearSelection()
		if text == "" {
			return nil, true, false
		}
		return selectionClipboardCmd(text), true, true
	}
	if msg.Text != "" {
		c.ClearSelection()
	}
	return nil, false, false
}
