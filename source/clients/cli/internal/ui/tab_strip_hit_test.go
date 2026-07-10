package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTabStripHitAtX_CloseVsLabel(t *testing.T) {
	items := []tabStripItem{
		{ID: "main", Label: "main"},
		{ID: "c1", Label: "sub 1", Closable: true},
	}
	var c1 tabStripSegment
	for _, s := range tabStripSegments(items) {
		if s.id == "c1" {
			c1 = s
		}
	}
	if c1.closeStart == 0 {
		t.Fatal("closable segment must have a close region")
	}
	// Click on the label part -> switch (not close).
	if id, isClose, ok := tabStripHitAtX(items, c1.start+1); !ok || id != "c1" || isClose {
		t.Fatalf("label hit = %q,%v,%v want c1,false,true", id, isClose, ok)
	}
	// Click on the [x] region -> close.
	if id, isClose, ok := tabStripHitAtX(items, c1.closeStart); !ok || id != "c1" || !isClose {
		t.Fatalf("close hit = %q,%v,%v want c1,true,true", id, isClose, ok)
	}
	// main is not closable: no column reads as close.
	if id, isClose, ok := tabStripHitAtX(items, 1); !ok || id != "main" || isClose {
		t.Fatalf("main hit = %q,%v,%v want main,false,true", id, isClose, ok)
	}
}

func TestCloseSubAgentTab_ByID(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "c1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "c2", kind: "started"})
	m.switchChatTab("c2")
	if !m.closeSubAgentTab("c1") {
		t.Fatal("closeSubAgentTab(c1) should succeed")
	}
	if _, ok := m.chatTabs.tabs["c1"]; ok {
		t.Fatal("c1 should be removed")
	}
	if m.chatTabs.active != "c2" {
		t.Fatalf("active = %q, want c2 (unchanged when closing a non-active tab)", m.chatTabs.active)
	}
	if m.closeSubAgentTab(mainChatTabID) {
		t.Fatal("main must never be closable")
	}
}
