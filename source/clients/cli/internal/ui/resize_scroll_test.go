package ui

import (
	"testing"
)

// buildEntries returns n RoleSystem "line" entries; used by the resize tests
// below. SetEntries renders from whatever slice is passed — it does NOT write
// to c.entries — so every re-render call must pass the same slice directly.
func buildEntries(n int) []*Entry {
	out := make([]*Entry, n)
	for i := range out {
		out[i] = &Entry{Role: RoleSystem, Content: "line"}
	}
	return out
}

// TestSetSize_PreservesBottomAnchor verifies that expand-then-shrink does not
// lose the scroll position when the user is at the bottom. Each SetEntries call
// passes the entries slice directly (c.Entries() is empty until entries are
// populated via AppendEntry/SetEntriesSlice, which is the host's job).
func TestSetSize_PreservesBottomAnchor(t *testing.T) {
	c := newTestChatView(80, 30)
	entries := buildEntries(60)
	c.SetEntries(entries)
	c.GotoBottom()
	if !c.AtBottom() {
		t.Fatal("precondition: expected to be at the bottom after GotoBottom")
	}

	c.SetSize(80, 50)
	c.SetEntries(entries)
	if !c.AtBottom() {
		t.Errorf("after expand, should still be at bottom")
	}

	c.SetSize(80, 20)
	c.SetEntries(entries)
	if !c.AtBottom() {
		t.Errorf("after expand-then-shrink, should still be at bottom")
	}
}

// TestSetSize_PreservesMidScrollAnchor verifies that resizing while scrolled to
// the middle keeps the same content line visible at the bottom of the viewport.
// The resize anchor is computed from the virtual scroll surface height so it is
// valid immediately after construction and after every relayout.
func TestSetSize_PreservesMidScrollAnchor(t *testing.T) {
	c := newTestChatView(80, 10)
	entries := buildEntries(80)
	c.SetEntries(entries)

	c.SetYOffset(20)
	if c.AtBottom() {
		t.Fatal("precondition: should not be at bottom after SetYOffset(20)")
	}

	// With height=10 and offset=20 the bottom visible content line is 29.
	// After resize to height=20 the anchor should keep line 29 at the bottom,
	// meaning new offset = 29 - 20 + 1 = 10.
	c.SetSize(80, 20)
	c.SetEntries(entries)

	if c.YOffset() != 10 {
		t.Errorf("YOffset after resize: got %d, want 10 (bottom anchor line 29)", c.YOffset())
	}
	bottom := c.YOffset() + c.Height() - 1
	if bottom != 29 {
		t.Errorf("bottom visible line: got %d, want 29", bottom)
	}
}
