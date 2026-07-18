package ui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

type promptInfo struct {
	LineNumber int
	Focused    bool
}

type promptInputStyles struct {
	Text        lipgloss.Style
	Placeholder lipgloss.Style
	Selection   lipgloss.Style
	Chip        lipgloss.Style
}

type promptInput struct {
	Placeholder string
	// Suggestion is ghost-text rendered in place of Placeholder when the
	// input is empty. Displayed with the same muted style as the
	// placeholder so it reads as "you can Tab to accept this" rather than
	// as typed input. Cleared when the user starts typing (the model
	// syncs this) or accepted by Tab (the model consumes it).
	Suggestion string
	MinHeight  int
	MaxHeight  int

	value  []rune
	cursor int
	focus  bool

	width       int
	promptWidth int
	promptFunc  func(promptInfo) string
	styles      promptInputStyles

	height     int
	scrollY    int
	goalColumn int
	hasGoal    bool

	selectionAnchor int
	dragging        bool

	undo        []promptUndoRecord
	redo        []promptUndoRecord
	canCoalesce bool

	attachments []promptImage
	nextImageID int
}

type promptUndoRecord struct {
	kind   string
	before promptSnapshot
	after  promptSnapshot
}

type promptSnapshot struct {
	text            string
	cursor          int
	selectionAnchor int
}

type promptRow struct {
	start int
	end   int
	text  string
}

const noPromptSelection = -1
const promptWheelDelta = 3

func newPromptInput() promptInput {
	p := promptInput{
		MinHeight:       1,
		MaxHeight:       maxInputLines,
		width:           20,
		selectionAnchor: noPromptSelection,
		goalColumn:      -1,
	}
	p.recalculate()
	return p
}

func (p *promptInput) SetPromptFunc(width int, fn func(promptInfo) string) {
	p.promptWidth = maxInt(0, width)
	p.promptFunc = fn
	p.recalculate()
}

func (p *promptInput) SetStyles(styles promptInputStyles) {
	p.styles = styles
}

func (p *promptInput) Focus() tea.Cmd {
	p.focus = true
	return nil
}

func (p *promptInput) Blur() {
	p.focus = false
}

func (p promptInput) Focused() bool {
	return p.focus
}

func (p promptInput) Value() string {
	return string(p.value)
}

func (p *promptInput) SetValue(s string) {
	p.value = []rune(s)
	p.cursor = len(p.value)
	p.selectionAnchor = noPromptSelection
	p.attachments = nil
	p.nextImageID = 0
	p.undo = nil
	p.redo = nil
	p.breakUndoCoalescing()
	p.recalculate()
	p.ensureCursorVisible()
}

func (p *promptInput) Reset() {
	p.SetValue("")
}

func (p *promptInput) CursorEnd() {
	p.moveCursor(len(p.value), false)
}

func (p *promptInput) CursorStart() {
	p.moveCursor(0, false)
}

func (p *promptInput) InsertString(s string) {
	p.applyEdit("insert", false, func() {
		p.replaceSelectionOrInsert([]rune(s))
	})
}

func (p *promptInput) SetWidth(width int) {
	p.width = maxInt(1, width)
	p.recalculate()
	p.clampScroll()
	p.ensureCursorVisible()
}

func (p promptInput) Height() int {
	return p.height
}

func (p promptInput) Line() int {
	line := 0
	for i := 0; i < p.cursor && i < len(p.value); i++ {
		if p.value[i] == '\n' {
			line++
		}
	}
	return line
}

func (p promptInput) LineCount() int {
	count := 1
	for _, r := range p.value {
		if r == '\n' {
			count++
		}
	}
	return count
}

// CursorOnFirstRow reports whether the cursor sits on the first *visual* row,
// counting soft-wrapped rows — not just hard newlines like Line(). This mirrors
// the up-navigation no-op condition: it is true exactly when pressing ↑ could
// not move the cursor to a row above. History recall gates on this so ↑ inside a
// long soft-wrapped line moves the cursor up a row instead of clobbering the
// draft with a past submission.
func (p promptInput) CursorOnFirstRow() bool {
	rows := p.layoutRows()
	// Use the same row mapping the cursor is *rendered* with (cursorRowIndex,
	// as in Cursor()), not cursorRowIndexForVertical: at a soft-wrap boundary
	// offset the vertical variant reports the row above, which would recall
	// history while the user sees their cursor on the second row.
	return p.cursorRowIndex(rows, p.cursor) == 0
}

// CursorOnLastRow reports whether the cursor sits on the last *visual* row
// (soft-wrapped rows included). True exactly when pressing ↓ could not move the
// cursor to a row below; ↓-history recall gates on this.
func (p promptInput) CursorOnLastRow() bool {
	rows := p.layoutRows()
	return p.cursorRowIndex(rows, p.cursor) == len(rows)-1
}

func (p promptInput) HasSelection() bool {
	return p.selectionRangeOK()
}

func (p promptInput) ScrollYOffset() int {
	return p.scrollY
}

func (p promptInput) MaxScrollYOffset() int {
	rows := p.layoutRows()
	return maxInt(0, len(rows)-p.height)
}

func (p *promptInput) ScrollView(delta int) {
	if delta == 0 {
		return
	}
	p.scrollY = clampInt(p.scrollY+delta, 0, p.MaxScrollYOffset())
	p.breakUndoCoalescing()
}

func (p *promptInput) MouseDown(x, y int) {
	p.dragging = true
	offset := p.offsetFromPoint(x, y)
	p.cursor = offset
	p.selectionAnchor = offset
	p.goalColumn = p.cursorColumn()
	p.hasGoal = true
	p.breakUndoCoalescing()
	p.ensureCursorVisible()
}

func (p *promptInput) MouseDrag(x, y int) {
	if !p.dragging {
		return
	}
	switch {
	case y < 0:
		p.ScrollView(-1)
		y = 0
	case y >= p.height:
		p.ScrollView(1)
		y = p.height - 1
	}
	if p.selectionAnchor < 0 {
		p.selectionAnchor = p.cursor
	}
	p.cursor = p.offsetFromPoint(x, y)
	p.goalColumn = p.cursorColumn()
	p.hasGoal = true
}

// MouseUp ends a drag and returns the plain text of the resulting selection
// (empty when the drag collapsed to a caret). The host copies that text to the
// clipboard so releasing a prompt drag auto-copies — matching the scrollback
// viewport, and covering terminals that reserve Cmd+C for their own copy and
// so never deliver it to the app.
func (p *promptInput) MouseUp(x, y int) string {
	if p.dragging {
		p.MouseDrag(x, y)
	}
	p.dragging = false
	if p.selectionAnchor == p.cursor {
		p.selectionAnchor = noPromptSelection
	}
	return p.selectedText()
}

func (p promptInput) Dragging() bool {
	return p.dragging
}

func (p *promptInput) CancelDrag() {
	p.dragging = false
}

func (p promptInput) Update(msg tea.Msg) (promptInput, tea.Cmd) {
	if !p.focus {
		return p, nil
	}

	switch msg := msg.(type) {
	case tea.PasteMsg:
		p.applyEdit("paste", false, func() {
			p.replaceSelectionOrInsert([]rune(sanitizePromptPaste(msg.Content)))
		})
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(msg)
	}
	return p, nil
}

func (p promptInput) handleKey(msg tea.KeyPressMsg) (promptInput, tea.Cmd) {
	k := msg.Key()
	shift := promptShiftMod(msg)
	alt := promptAltMod(msg)
	cmd := promptCommandMod(msg.Key())

	if isPromptRedoKey(msg) {
		p.redoEdit()
		return p, nil
	}
	if isPromptUndoKey(msg) {
		p.undoEdit()
		return p, nil
	}
	if cmd && keyIs(k, 'a') {
		p.selectionAnchor = 0
		p.cursor = len(p.value)
		p.ensureCursorVisible()
		p.breakUndoCoalescing()
		return p, nil
	}
	if (cmd && keyIs(k, 'c')) || msg.String() == "ctrl+c" {
		if text := p.selectedText(); text != "" {
			p.breakUndoCoalescing()
			return p, selectionClipboardCmd(text)
		}
	}

	switch {
	case isPromptDocStartKey(msg):
		p.moveCursor(0, shift)
		return p, nil
	case isPromptDocEndKey(msg):
		p.moveCursor(len(p.value), shift)
		return p, nil
	case isPromptLineStartKey(msg):
		p.navigate(promptNavLeft, shift, false, true)
		return p, nil
	case isPromptLineEndKey(msg):
		p.navigate(promptNavRight, shift, false, true)
		return p, nil
	case isKeyLeft(k, msg) || isPromptWordLeftKey(msg):
		p.navigate(promptNavLeft, shift, alt, cmd)
		return p, nil
	case isKeyRight(k, msg) || isPromptWordRightKey(msg):
		p.navigate(promptNavRight, shift, alt, cmd)
		return p, nil
	case isKeyUp(k, msg):
		p.navigate(promptNavUp, shift, alt, cmd)
		return p, nil
	case isKeyDown(k, msg):
		p.navigate(promptNavDown, shift, alt, cmd)
		return p, nil
	case k.Code == tea.KeyBackspace || msg.String() == "backspace" || msg.String() == "ctrl+h":
		p.applyEdit("delete-backward", true, func() {
			p.deleteBackward()
		})
		return p, nil
	case k.Code == tea.KeyDelete || msg.String() == "delete":
		p.applyEdit("delete-forward", true, func() {
			p.deleteForward()
		})
		return p, nil
	case msg.Text != "" && !cmd && !k.Mod.Contains(tea.ModCtrl):
		p.applyEdit("insert", true, func() {
			p.replaceSelectionOrInsert([]rune(msg.Text))
		})
		return p, nil
	}

	p.breakUndoCoalescing()
	return p, nil
}

type promptNav int

const (
	promptNavLeft promptNav = iota
	promptNavRight
	promptNavUp
	promptNavDown
)

func (p *promptInput) moveCursor(offset int, extend bool) {
	old := p.cursor
	offset = clampInt(offset, 0, len(p.value))
	if extend {
		if p.selectionAnchor < 0 {
			p.selectionAnchor = old
		}
		p.cursor = offset
		if p.selectionAnchor == p.cursor {
			p.selectionAnchor = noPromptSelection
		}
	} else {
		p.selectionAnchor = noPromptSelection
		p.cursor = offset
	}
	p.goalColumn = p.cursorColumn()
	p.hasGoal = true
	p.ensureCursorVisible()
	p.breakUndoCoalescing()
}

func (p *promptInput) navigate(nav promptNav, extend, alt, cmd bool) {
	old := p.cursor
	next := old

	switch nav {
	case promptNavLeft:
		switch {
		case cmd:
			next = p.lineStartOffset(old)
		case alt:
			next = p.prevWordOffset(old)
		default:
			next = p.stepLeftOverChip(maxInt(0, old-1))
		}
	case promptNavRight:
		switch {
		case cmd:
			next = p.lineEndOffset(old)
		case alt:
			next = p.nextWordOffset(old)
		default:
			next = p.stepRightOverChip(minInt(len(p.value), old+1))
		}
	case promptNavUp:
		if cmd {
			next = 0
		} else {
			next = p.offsetByVisualLine(-1)
		}
	case promptNavDown:
		if cmd {
			next = len(p.value)
		} else {
			next = p.offsetByVisualLine(1)
		}
	}

	if extend {
		if p.selectionAnchor < 0 {
			p.selectionAnchor = old
		}
		p.cursor = next
		if p.selectionAnchor == p.cursor {
			p.selectionAnchor = noPromptSelection
		}
	} else {
		p.selectionAnchor = noPromptSelection
		p.cursor = next
	}

	if nav == promptNavUp || nav == promptNavDown {
		if !p.hasGoal {
			p.goalColumn = p.cursorColumnAt(old)
			p.hasGoal = true
		}
	} else {
		p.goalColumn = p.cursorColumn()
		p.hasGoal = true
	}
	p.ensureCursorVisible()
	p.breakUndoCoalescing()
}

func (p *promptInput) applyEdit(kind string, coalesce bool, edit func()) {
	before := p.snapshot()
	edit()
	p.cursor = clampInt(p.cursor, 0, len(p.value))
	if p.selectionAnchor == p.cursor {
		p.selectionAnchor = noPromptSelection
	}
	p.recalculate()
	p.ensureCursorVisible()
	after := p.snapshot()
	if before == after {
		return
	}

	if coalesce && p.canCoalesce && len(p.undo) > 0 && p.undo[len(p.undo)-1].kind == kind {
		p.undo[len(p.undo)-1].after = after
	} else {
		p.undo = append(p.undo, promptUndoRecord{kind: kind, before: before, after: after})
	}
	p.redo = nil
	p.canCoalesce = coalesce
}

func (p *promptInput) undoEdit() {
	if len(p.undo) == 0 {
		return
	}
	rec := p.undo[len(p.undo)-1]
	p.undo = p.undo[:len(p.undo)-1]
	p.redo = append(p.redo, rec)
	p.restore(rec.before)
	p.breakUndoCoalescing()
}

func (p *promptInput) redoEdit() {
	if len(p.redo) == 0 {
		return
	}
	rec := p.redo[len(p.redo)-1]
	p.redo = p.redo[:len(p.redo)-1]
	p.undo = append(p.undo, rec)
	p.restore(rec.after)
	p.breakUndoCoalescing()
}

func (p *promptInput) snapshot() promptSnapshot {
	return promptSnapshot{
		text:            string(p.value),
		cursor:          p.cursor,
		selectionAnchor: p.selectionAnchor,
	}
}

func (p *promptInput) restore(s promptSnapshot) {
	p.value = []rune(s.text)
	p.cursor = clampInt(s.cursor, 0, len(p.value))
	p.selectionAnchor = s.selectionAnchor
	if p.selectionAnchor >= 0 {
		p.selectionAnchor = clampInt(p.selectionAnchor, 0, len(p.value))
	}
	p.recalculate()
	p.ensureCursorVisible()
}

func (p *promptInput) breakUndoCoalescing() {
	p.canCoalesce = false
}

func sanitizePromptPaste(s string) string {
	return ansi.Strip(s)
}

func (p *promptInput) replaceSelectionOrInsert(runes []rune) {
	start, end, ok := p.selectionRange()
	if ok {
		p.value = append(append([]rune{}, p.value[:start]...), append(runes, p.value[end:]...)...)
		p.cursor = start + len(runes)
		p.selectionAnchor = noPromptSelection
		return
	}
	p.value = append(append([]rune{}, p.value[:p.cursor]...), append(runes, p.value[p.cursor:]...)...)
	p.cursor += len(runes)
	p.selectionAnchor = noPromptSelection
}

func (p *promptInput) deleteBackward() {
	if sp, ok := p.spanForBackspace(); ok && !p.selectionRangeOK() {
		p.deleteSpan(sp)
		return
	}
	if start, end, ok := p.selectionRange(); ok {
		p.value = append(append([]rune{}, p.value[:start]...), p.value[end:]...)
		p.cursor = start
		p.selectionAnchor = noPromptSelection
		return
	}
	if p.cursor == 0 {
		return
	}
	p.value = append(append([]rune{}, p.value[:p.cursor-1]...), p.value[p.cursor:]...)
	p.cursor--
}

func (p *promptInput) deleteForward() {
	if sp, ok := p.spanForDeleteForward(); ok && !p.selectionRangeOK() {
		p.deleteSpan(sp)
		return
	}
	if start, end, ok := p.selectionRange(); ok {
		p.value = append(append([]rune{}, p.value[:start]...), p.value[end:]...)
		p.cursor = start
		p.selectionAnchor = noPromptSelection
		return
	}
	if p.cursor >= len(p.value) {
		return
	}
	p.value = append(append([]rune{}, p.value[:p.cursor]...), p.value[p.cursor+1:]...)
}

func (p promptInput) selectedText() string {
	start, end, ok := p.selectionRange()
	if !ok {
		return ""
	}
	return string(p.value[start:end])
}

func (p promptInput) selectionRangeOK() bool {
	_, _, ok := p.selectionRange()
	return ok
}

func (p promptInput) selectionRange() (int, int, bool) {
	if p.selectionAnchor < 0 || p.selectionAnchor == p.cursor {
		return 0, 0, false
	}
	a := clampInt(p.selectionAnchor, 0, len(p.value))
	b := clampInt(p.cursor, 0, len(p.value))
	if b < a {
		a, b = b, a
	}
	for _, sp := range p.imageSpans() {
		if a > sp.start && a < sp.end {
			a = sp.start
		}
		if b > sp.start && b < sp.end {
			b = sp.end
		}
	}
	return a, b, a != b
}

func (p *promptInput) recalculate() {
	rows := p.layoutRows()
	minH := maxInt(1, p.MinHeight)
	h := maxInt(minH, len(rows))
	if p.MaxHeight > 0 {
		h = minInt(h, p.MaxHeight)
	}
	p.height = h
	p.clampScroll()
}

func (p *promptInput) clampScroll() {
	p.scrollY = clampInt(p.scrollY, 0, p.MaxScrollYOffset())
}

func (p *promptInput) ensureCursorVisible() {
	rows := p.layoutRows()
	idx := p.cursorRowIndex(rows, p.cursor)
	if idx < p.scrollY {
		p.scrollY = idx
	}
	if idx >= p.scrollY+p.height {
		p.scrollY = idx - p.height + 1
	}
	p.clampScroll()
}

func (p promptInput) View() string {
	rows := p.layoutRows()
	selectionStart, selectionEnd, hasSelection := p.selectionRange()
	lines := make([]string, 0, p.height)
	textWidth := p.textWidth()
	for screenRow := 0; screenRow < p.height; screenRow++ {
		rowIdx := p.scrollY + screenRow
		prompt := p.promptView(rowIdx)
		if rowIdx >= len(rows) {
			lines = append(lines, prompt+strings.Repeat(" ", textWidth))
			continue
		}
		row := rows[rowIdx]
		lineText := row.text
		if len(p.value) == 0 && rowIdx == 0 && (p.Placeholder != "" || p.Suggestion != "") {
			// Prefer the model-generated Suggestion over the static
			// Placeholder — when a "what to do next" ghost is available,
			// it's more useful to show than the generic "type a message"
			// prompt. Both render in the same Placeholder style so the
			// input still reads as empty.
			ghost := p.Placeholder
			if p.Suggestion != "" {
				ghost = p.Suggestion
			}
			ghost = ansi.Truncate(ghost, textWidth, "")
			lineText = p.styles.Placeholder.Render(ghost)
			lines = append(lines, prompt+lineText+strings.Repeat(" ", maxInt(0, textWidth-lipgloss.Width(ghost))))
			continue
		}

		plainWidth := lipgloss.Width(row.text)
		lineText = p.renderRowText(row, selectionStart, selectionEnd, hasSelection)
		padding := strings.Repeat(" ", maxInt(0, textWidth-plainWidth))
		if p.rowLineBreakSelected(row, selectionStart, selectionEnd, hasSelection) {
			padding = p.styles.Selection.Render(padding)
		}
		lines = append(lines, prompt+lineText+padding)
	}
	return strings.Join(lines, "\n")
}

func (p promptInput) renderRowText(row promptRow, selectionStart, selectionEnd int, hasSelection bool) string {
	var b strings.Builder
	for i := row.start; i < row.end; {
		// is i the start of a chip span within this row?
		if sp, ok := p.chipSpanStartingAt(i); ok && sp.end <= row.end {
			seg := string(p.value[sp.start:sp.end])
			if hasSelection && selectionStart <= sp.start && selectionEnd >= sp.end {
				b.WriteString(p.styles.Selection.Render(seg))
			} else {
				b.WriteString(p.styles.Chip.Render(seg))
			}
			i = sp.end
			continue
		}
		// plain rune run until the next chip start or row end
		j := i + 1
		for j < row.end {
			if _, ok := p.chipSpanStartingAt(j); ok {
				break
			}
			j++
		}
		seg := string(p.value[i:j])
		if hasSelection {
			s := maxInt(selectionStart, i)
			e := minInt(selectionEnd, j)
			if s < e {
				b.WriteString(p.styles.Text.Render(string(p.value[i:s])))
				b.WriteString(p.styles.Selection.Render(string(p.value[s:e])))
				b.WriteString(p.styles.Text.Render(string(p.value[e:j])))
				i = j
				continue
			}
		}
		b.WriteString(p.styles.Text.Render(seg))
		i = j
	}
	return b.String()
}

func (p promptInput) chipSpanStartingAt(offset int) (imageSpan, bool) {
	for _, sp := range p.imageSpans() {
		if sp.start == offset {
			return sp, true
		}
	}
	return imageSpan{}, false
}

func (p promptInput) rowLineBreakSelected(row promptRow, selectionStart, selectionEnd int, hasSelection bool) bool {
	return hasSelection &&
		row.end < len(p.value) &&
		p.value[row.end] == '\n' &&
		selectionStart <= row.end &&
		selectionEnd > row.end
}

func (p promptInput) Cursor() *tea.Cursor {
	if !p.focus {
		return nil
	}
	rows := p.layoutRows()
	idx := p.cursorRowIndex(rows, p.cursor)
	if idx < p.scrollY || idx >= p.scrollY+p.height {
		return nil
	}
	row := rows[idx]
	x := p.promptWidth + p.widthBetween(row.start, p.cursor)
	y := idx - p.scrollY
	return tea.NewCursor(x, y)
}

func (p promptInput) promptView(row int) string {
	if p.promptFunc == nil {
		return strings.Repeat(" ", p.promptWidth)
	}
	prompt := p.promptFunc(promptInfo{LineNumber: row, Focused: p.focus})
	w := lipgloss.Width(prompt)
	if w < p.promptWidth {
		return strings.Repeat(" ", p.promptWidth-w) + prompt
	}
	return prompt
}

func (p promptInput) textWidth() int {
	return maxInt(1, p.width-p.promptWidth)
}

func (p promptInput) layoutRows() []promptRow {
	textWidth := p.textWidth()
	rows := make([]promptRow, 0, maxInt(1, len(p.value)/maxInt(1, textWidth)))
	rowStart := 0
	col := 0
	lastSpace := -1 // rune index of the last ' ' seen in the current row, for word wrap
	i := 0
	for i < len(p.value) {
		r := p.value[i]
		if r == '\n' {
			rows = append(rows, p.rowFromRange(rowStart, i))
			rowStart = i + 1
			col = 0
			lastSpace = -1
			i++
			continue
		}
		w := runeWidth(r)
		if col > 0 && col+w > textWidth {
			// Soft-wrap: prefer breaking after the last space in the row so words
			// stay intact. The trailing space stays on the current row (no text is
			// dropped; row ranges remain contiguous). Reprocess from the break.
			if lastSpace >= rowStart {
				breakAt := lastSpace + 1
				rows = append(rows, p.rowFromRange(rowStart, breakAt))
				rowStart = breakAt
				i = breakAt
				col = 0
				lastSpace = -1
				continue
			}
			// A single word longer than the line — hard-break before this rune.
			rows = append(rows, p.rowFromRange(rowStart, i))
			rowStart = i
			col = 0
			lastSpace = -1
			continue
		}
		if r == ' ' {
			lastSpace = i
		}
		col += w
		i++
	}
	rows = append(rows, p.rowFromRange(rowStart, len(p.value)))
	return rows
}

func (p promptInput) rowFromRange(start, end int) promptRow {
	return promptRow{
		start: start,
		end:   end,
		text:  string(p.value[start:end]),
	}
}

func (p promptInput) cursorRowIndex(rows []promptRow, offset int) int {
	offset = clampInt(offset, 0, len(p.value))
	for i, row := range rows {
		if offset == row.start {
			return i
		}
	}
	for i, row := range rows {
		if row.start == row.end && offset == row.start {
			return i
		}
		if offset > row.start && offset <= row.end {
			return i
		}
	}
	return maxInt(0, len(rows)-1)
}

func (p promptInput) cursorColumn() int {
	return p.cursorColumnAt(p.cursor)
}

func (p promptInput) cursorColumnAt(offset int) int {
	rows := p.layoutRows()
	idx := p.cursorRowIndex(rows, offset)
	return p.widthBetween(rows[idx].start, offset)
}

func (p promptInput) offsetByVisualLine(delta int) int {
	rows := p.layoutRows()
	idx := p.cursorRowIndexForVertical(rows, p.cursor, delta)
	if delta < 0 && idx == 0 {
		return 0
	}
	if delta > 0 && idx == len(rows)-1 {
		return len(p.value)
	}
	target := clampInt(idx+delta, 0, len(rows)-1)
	goal := p.goalColumn
	if !p.hasGoal || goal < 0 {
		goal = p.cursorColumn()
	}
	return p.offsetAtColumn(rows[target], goal)
}

func (p promptInput) cursorRowIndexForVertical(rows []promptRow, offset, delta int) int {
	if delta < 0 && offset > 0 {
		for i := 1; i < len(rows); i++ {
			if offset == rows[i].start && p.value[offset-1] != '\n' {
				return i - 1
			}
		}
	}
	return p.cursorRowIndex(rows, offset)
}

func (p promptInput) offsetFromPoint(x, y int) int {
	rows := p.layoutRows()
	rowIdx := clampInt(p.scrollY+y, 0, len(rows)-1)
	col := clampInt(x-p.promptWidth, 0, p.textWidth())
	return p.offsetAtColumn(rows[rowIdx], col)
}

func (p promptInput) offsetAtColumn(row promptRow, col int) int {
	col = maxInt(0, col)
	width := 0
	for i := row.start; i < row.end; i++ {
		w := runeWidth(p.value[i])
		if width+w > col {
			return i
		}
		width += w
	}
	return row.end
}

func (p promptInput) widthBetween(start, end int) int {
	start = clampInt(start, 0, len(p.value))
	end = clampInt(end, start, len(p.value))
	w := 0
	for _, r := range p.value[start:end] {
		w += runeWidth(r)
	}
	return w
}

func (p promptInput) lineStartOffset(offset int) int {
	offset = clampInt(offset, 0, len(p.value))
	for offset > 0 && p.value[offset-1] != '\n' {
		offset--
	}
	return offset
}

func (p promptInput) lineEndOffset(offset int) int {
	offset = clampInt(offset, 0, len(p.value))
	for offset < len(p.value) && p.value[offset] != '\n' {
		offset++
	}
	return offset
}

func (p promptInput) prevWordOffset(offset int) int {
	i := clampInt(offset, 0, len(p.value))
	for i > 0 && unicode.IsSpace(p.value[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(p.value[i-1]) {
		i--
	}
	return i
}

func (p promptInput) nextWordOffset(offset int) int {
	i := clampInt(offset, 0, len(p.value))
	for i < len(p.value) && unicode.IsSpace(p.value[i]) {
		i++
	}
	for i < len(p.value) && !unicode.IsSpace(p.value[i]) {
		i++
	}
	return i
}

func runeWidth(r rune) int {
	if r == '\n' {
		return 0
	}
	w := runewidth.RuneWidth(r)
	if w < 1 {
		return 1
	}
	return w
}

func keyIs(k tea.Key, r rune) bool {
	upper := unicode.ToUpper(r)
	return k.Code == r || k.Code == upper || k.BaseCode == r || k.BaseCode == upper
}

func isPromptUndoKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	if keyIs(k, '_') && k.Mod.Contains(tea.ModCtrl) {
		return true
	}
	if !keyIs(k, 'z') {
		return false
	}
	if k.Mod.Contains(tea.ModCtrl) && !k.Mod.Contains(tea.ModShift) {
		return true
	}
	return promptCommandMod(k) && !k.Mod.Contains(tea.ModShift)
}

func isPromptRedoKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	if !keyIs(k, 'z') {
		return false
	}
	if k.Mod.Contains(tea.ModCtrl) && k.Mod.Contains(tea.ModShift) {
		return true
	}
	return promptCommandMod(k) && k.Mod.Contains(tea.ModShift)
}

func promptCommandMod(k tea.Key) bool {
	return k.Mod.Contains(tea.ModSuper) || k.Mod.Contains(tea.ModMeta)
}

func promptShiftMod(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return k.Mod.Contains(tea.ModShift) || keyHasMod(msg, "shift")
}

func promptAltMod(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return k.Mod.Contains(tea.ModAlt) || keyHasMod(msg, "alt")
}

func promptCtrlMod(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return k.Mod.Contains(tea.ModCtrl) || keyHasMod(msg, "ctrl")
}

func keyHasMod(msg tea.KeyPressMsg, mod string) bool {
	for _, repr := range promptKeyReprs(msg) {
		parts := strings.Split(repr, "+")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == mod {
				return true
			}
		}
	}
	return false
}

func keyNamed(msg tea.KeyPressMsg, names ...string) bool {
	for _, repr := range promptKeyReprs(msg) {
		for _, name := range names {
			if repr == name || strings.HasSuffix(repr, "+"+name) {
				return true
			}
		}
	}
	return false
}

func promptKeyReprs(msg tea.KeyPressMsg) []string {
	reprs := []string{
		strings.ToLower(msg.Keystroke()),
		strings.ToLower(msg.String()),
	}
	if text := strings.ToLower(msg.Key().Text); text != "" {
		reprs = append(reprs, text)
	}
	return reprs
}

func isPromptWordLeftKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return promptAltMod(msg) && keyIs(k, 'b')
}

func isPromptWordRightKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return promptAltMod(msg) && keyIs(k, 'f')
}

func isPromptLineStartKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return keyNamed(msg, "home") ||
		(promptCtrlMod(msg) && keyIs(k, 'a') && !promptAltMod(msg) && !promptCommandMod(k))
}

func isPromptLineEndKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return keyNamed(msg, "end") ||
		(promptCtrlMod(msg) && keyIs(k, 'e') && !promptAltMod(msg) && !promptCommandMod(k))
}

func isPromptDocStartKey(msg tea.KeyPressMsg) bool {
	return (promptCommandMod(msg.Key()) || promptCtrlMod(msg)) && keyNamed(msg, "home")
}

func isPromptDocEndKey(msg tea.KeyPressMsg) bool {
	return (promptCommandMod(msg.Key()) || promptCtrlMod(msg)) && keyNamed(msg, "end")
}

func isKeyLeft(k tea.Key, msg tea.KeyPressMsg) bool {
	return k.Code == tea.KeyLeft || k.BaseCode == tea.KeyLeft || keyNamed(msg, "left")
}

func isKeyRight(k tea.Key, msg tea.KeyPressMsg) bool {
	return k.Code == tea.KeyRight || k.BaseCode == tea.KeyRight || keyNamed(msg, "right")
}

func isKeyUp(k tea.Key, msg tea.KeyPressMsg) bool {
	return k.Code == tea.KeyUp || k.BaseCode == tea.KeyUp || keyNamed(msg, "up")
}

func isKeyDown(k tea.Key, msg tea.KeyPressMsg) bool {
	return k.Code == tea.KeyDown || k.BaseCode == tea.KeyDown || keyNamed(msg, "down")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
