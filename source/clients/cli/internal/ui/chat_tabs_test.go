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
	if m.chatTabs.active != "child-1" {
		t.Fatalf("active tab = %q, want child-1", m.chatTabs.active)
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
