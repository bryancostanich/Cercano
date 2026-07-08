package ui

import (
	"strings"
	"testing"
)

// TestPromptRowPredicates_AgreeWithRenderedCursor sweeps the cursor across
// every offset of a soft-wrapping draft and asserts that CursorOnFirstRow /
// CursorOnLastRow agree with the row the cursor is actually rendered on
// (cursorRowIndex, the same mapping Cursor() uses). A disagreement means ↑/↓
// would recall history at a spot where the user sees their cursor mid-draft —
// the soft-wrap boundary regression this guards against.
func TestPromptRowPredicates_AgreeWithRenderedCursor(t *testing.T) {
	p := newPromptInput()
	p.SetPromptFunc(2, func(promptInfo) string { return "> " })
	p.SetWidth(40)
	p.Focus()
	draft := strings.Repeat("wrap ", 30) // ~150 cols over width 40 → several visual rows
	p.SetValue(draft)

	rows := p.layoutRows()
	if len(rows) < 3 {
		t.Fatalf("precondition: draft should soft-wrap to several rows, got %d", len(rows))
	}

	for off := 0; off <= len(p.value); off++ {
		p.cursor = off
		p.goalColumn = p.cursorColumn()
		renderRow := p.cursorRowIndex(rows, off)
		if p.CursorOnFirstRow() != (renderRow == 0) {
			t.Errorf("offset %d: CursorOnFirstRow=%v but cursor renders on row %d of %d",
				off, p.CursorOnFirstRow(), renderRow, len(rows))
		}
		if p.CursorOnLastRow() != (renderRow == len(rows)-1) {
			t.Errorf("offset %d: CursorOnLastRow=%v but cursor renders on row %d of %d",
				off, p.CursorOnLastRow(), renderRow, len(rows))
		}
	}
}
