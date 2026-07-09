package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSubAgentEventCreatesEphemeralTabWithGrant(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{
		id:    "child-1",
		title: "sub",
		kind:  "started",
		tools: []string{"Read", "Grep"},
		text:  "sub-agent start",
	})

	if !m.hasSubAgentTabs() {
		t.Fatalf("expected sub-agent tabs to be visible")
	}
	if m.chatTabs.active != mainChatTabID {
		t.Fatalf("active tab = %q, want main (creating a sub tab must not steal focus)", m.chatTabs.active)
	}
	tab := m.chatTabs.tabs["child-1"]
	if tab == nil {
		t.Fatalf("missing child tab")
	}
	if tab.title != "sub 1" {
		t.Fatalf("title = %q, want sub 1", tab.title)
	}
	var joined []string
	for _, e := range tab.view.Entries() {
		joined = append(joined, e.Content)
	}
	content := strings.Join(joined, "\n")
	if !strings.Contains(content, "Tools: Read, Grep") {
		t.Fatalf("grant not rendered in tab entries:\n%s", content)
	}
	if !strings.Contains(content, "sub-agent start") {
		t.Fatalf("lifecycle text not rendered in tab entries:\n%s", content)
	}
}

func TestSubAgentNestedLabelsUseParentOrdinal(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "grandchild-1", parentID: "child-1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "grandchild-2", parentID: "child-1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-2", kind: "started"})

	checks := map[string]string{
		"child-1":      "sub 1",
		"grandchild-1": "sub 1.1",
		"grandchild-2": "sub 1.2",
		"child-2":      "sub 2",
	}
	for id, want := range checks {
		tab := m.chatTabs.tabs[id]
		if tab == nil {
			t.Fatalf("missing tab %s", id)
		}
		if tab.title != want {
			t.Fatalf("tab %s title = %q, want %q", id, tab.title, want)
		}
	}
}

func TestSubAgentToolEventRoutesToChildTab(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", title: "sub", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{
		id:    "child-1",
		title: "sub",
		kind:  "tool_use_start",
		inner: toolEntryStartMsg{id: "tool-1", name: "Read"},
	})

	tab := m.chatTabs.tabs["child-1"]
	if tab == nil {
		t.Fatalf("missing child tab")
	}
	found := false
	for _, e := range tab.view.Entries() {
		if e.Tool != nil && e.Tool.ToolName == "Read" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected child tab transcript to contain routed tool entry")
	}
}

func TestChatTabStripFocusNavAndClose(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "c1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "c2", kind: "started"})

	if m.handleChatTabStripKey("x") {
		t.Fatal("keys must be ignored while the strip is unfocused")
	}
	if !m.handleChatTabStripKey("shift+tab") || !m.chatTabs.focused {
		t.Fatal("shift+tab should move focus to the strip")
	}
	m.handleChatTabStripKey("tab")
	if m.chatTabs.active != "c1" {
		t.Fatalf("after tab active=%q want c1", m.chatTabs.active)
	}
	m.handleChatTabStripKey("tab")
	if m.chatTabs.active != "c2" {
		t.Fatalf("after 2x tab active=%q want c2", m.chatTabs.active)
	}
	m.handleChatTabStripKey("shift+tab")
	if m.chatTabs.active != "c1" {
		t.Fatalf("after shift+tab active=%q want c1", m.chatTabs.active)
	}
	// close c1 while focused; c2 remains so focus persists.
	m.handleChatTabStripKey("x")
	if _, ok := m.chatTabs.tabs["c1"]; ok {
		t.Fatal("c1 should be closed")
	}
	if !m.chatTabs.focused {
		t.Fatal("focus should persist while a sub tab remains")
	}
	// closed ids are not resurrected by late events.
	m.applySubAgentEvent(subAgentEventMsg{id: "c1", kind: "token", text: "late"})
	if _, ok := m.chatTabs.tabs["c1"]; ok {
		t.Fatal("closed tab must not be resurrected")
	}
	// close the last sub tab; focus drops back to the prompt.
	m.switchChatTab("c2")
	m.handleChatTabStripKey("x")
	if m.hasSubAgentTabs() {
		t.Fatal("all sub tabs should be closed")
	}
	if m.chatTabs.focused {
		t.Fatal("focus should drop when no sub tabs remain")
	}
}
