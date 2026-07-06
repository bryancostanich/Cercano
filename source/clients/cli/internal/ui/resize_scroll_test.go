package ui

import (
	"testing"
)

// TestSetSize_PreservesBottomAnchor verifies that shrinking the viewport height
// while the user is at the bottom does not lose the scroll position. Before the
// fix, SetSize changed the height without re-anchoring, so the subsequent
// SetEntries/AtBottom check returned false and GotoBottom was never called.
func TestSetSize_PreservesBottomAnchor(t *testing.T) {
	c := newTestChatView(80, 30)
	entries := make([]*Entry, 0, 60)
	for i := 0; i < 60; i++ {
		entries = append(entries, &Entry{Role: RoleSystem, Content: "line"})
	}
	c.SetEntries(entries)
	c.GotoBottom()
	if !c.AtBottom() {
		t.Fatal("precondition: expected to be at the bottom after GotoBottom")
	}

	// Simulate expand then shrink (the failing sequence).
	c.SetSize(80, 50)
	c.SetEntries(c.Entries())
	c.SetSize(80, 20)
	c.SetEntries(c.Entries())

	if !c.AtBottom() {
		t.Errorf("after expand-then-shrink, viewport should still be at the bottom")
	}
}
