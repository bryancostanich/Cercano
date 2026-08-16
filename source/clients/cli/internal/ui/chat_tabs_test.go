package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
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
	entries := tab.view.Entries()
	if len(entries) != 1 || entries[0].SubAgentStart == nil {
		t.Fatalf("expected one startup-card entry, got %+v", entries)
	}
	card := entries[0].SubAgentStart
	if strings.Join(card.Tools, ", ") != "Read, Grep" {
		t.Fatalf("grant not recorded in startup card: %+v", card)
	}
	if card.Kind != "Sub-agent" || card.Title != "sub 1" {
		t.Fatalf("startup title not recorded in card: %+v", card)
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

func TestResearchActivityOpensTabAndAttachesOpenTabAffordance(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.mainChat().Apply(toolEntryStartMsg{id: "tool-r", name: "research"})
	m.mainChat().Apply(toolEntryStopMsg{id: "tool-r", argsSummary: "query=task tracking max_results=6"})

	m.applySubAgentEvent(subAgentEventMsg{id: "activity:research:1", kind: "started", title: "research", toolUseID: "tool-r", text: `research start: query="task tracking" max_results=6`})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:research:1", kind: "prompt", text: "Query: task tracking"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:research:1", kind: "progress", text: "searching the web…"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:research:1", kind: "done", text: "research complete: 6 sources"})

	tab := m.chatTabs.tabs["activity:research:1"]
	if tab == nil {
		t.Fatal("research tool should open its own activity tab")
	}
	var body []string
	for _, e := range tab.view.Entries() {
		body = append(body, e.Content)
	}
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "Query: task tracking") || !strings.Contains(joined, "• searching the web") || !strings.Contains(joined, "✓ research complete") {
		t.Fatalf("research activity tab missing formatted lifecycle: %q", joined)
	}

	entries := m.mainChat().Entries()
	if len(entries) == 0 || entries[0].Tool == nil || entries[0].Tool.SubAgentID != "activity:research:1" {
		t.Fatalf("research tool row should link to its activity tab: %+v", entries)
	}
	out := renderToolEntry(*entries[0].Tool, 100, false, m.styles, m.mainChat().md)
	if !strings.Contains(stripAnsiCSI(out), "open tab") {
		t.Fatalf("research tool row should show open tab affordance, got %q", stripAnsiCSI(out))
	}
}

func TestDeepResearchActivityFormattingSuppressesRawStartAndFormatsProgress(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "started", title: "research", text: `deep research start: topic="ux" depth=standard phase=all`})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "progress", text: "planning sources and research phases…"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "done", text: "deep research complete: phase=synthesize next=none"})

	tab := m.chatTabs.tabs["activity:deep_research:1"]
	if tab == nil {
		t.Fatal("missing research activity tab")
	}
	var body []string
	for _, e := range tab.view.Entries() {
		body = append(body, e.Content)
	}
	joined := strings.Join(body, "\n")
	if strings.Contains(joined, "deep research start:") {
		t.Fatalf("raw start metadata should not be duplicated in activity tab: %q", joined)
	}
	if !strings.Contains(joined, "• planning sources") || !strings.Contains(joined, "✓ deep research complete") {
		t.Fatalf("activity progress should be formatted, got %q", joined)
	}
}

func TestOpenExistingActivityTabPreservesLiveContents(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "started", title: "research"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "prompt", text: "Topic: tabs\nIntent: live feedback"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "progress", text: "planning sources…"})

	m.applySubAgentReopen(subAgentReopenMsg{id: "activity:deep_research:1"})
	if m.chatTabs.active != "activity:deep_research:1" {
		t.Fatalf("active tab = %q", m.chatTabs.active)
	}
	tab := m.chatTabs.tabs["activity:deep_research:1"]
	var joined []string
	for _, e := range tab.view.Entries() {
		joined = append(joined, e.Content)
	}
	body := strings.Join(joined, "\n")
	if !strings.Contains(body, "Topic: tabs") || !strings.Contains(body, "planning sources") {
		t.Fatalf("existing live activity contents were lost: %q", body)
	}
}

func TestDeepResearchActivityAttachesOpenTabAffordanceToTool(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.mainChat().Apply(toolEntryStartMsg{id: "tool-1", name: "deep_research"})
	m.mainChat().Apply(toolEntryStopMsg{id: "tool-1", argsSummary: "topic=activity tabs intent=live feedback"})

	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "started", title: "research", toolUseID: "tool-1"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "prompt", text: "Topic: activity tabs\nIntent: live feedback"})
	m.applySubAgentEvent(subAgentEventMsg{id: "activity:deep_research:1", kind: "progress", text: "planning sources…"})

	entries := m.mainChat().Entries()
	if len(entries) == 0 || entries[0].Tool == nil || entries[0].Tool.SubAgentID != "activity:deep_research:1" {
		t.Fatalf("deep_research tool did not record spawned activity tab: %+v", entries)
	}
	out := renderToolEntry(*entries[0].Tool, 100, false, m.styles, m.mainChat().md)
	if !strings.Contains(stripAnsiCSI(out), "open tab") {
		t.Fatalf("deep_research tool line should show open tab affordance, got %q", stripAnsiCSI(out))
	}
	tab := m.chatTabs.tabs["activity:deep_research:1"]
	if tab == nil {
		t.Fatal("missing research activity tab")
	}
	var body []string
	for _, e := range tab.view.Entries() {
		body = append(body, e.Content)
	}
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "Activity research started") || !strings.Contains(joined, "Topic: activity tabs") || !strings.Contains(joined, "planning sources") {
		t.Fatalf("activity tab missing live feedback entries: %q", joined)
	}
}

func TestSubAgentStartedAttachesOpenTabAffordanceToDispatchTool(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.mainChat().Apply(toolEntryStartMsg{id: "tool-1", name: "dispatch"})
	m.mainChat().Apply(toolEntryStopMsg{id: "tool-1", argsSummary: "conversation_id=recon task=trace"})
	m.mainChat().Apply(toolEntryExecCompleteMsg{id: "tool-1", summary: "done"})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started", toolUseID: "tool-1", tools: []string{"Read"}})

	entries := m.mainChat().Entries()
	if len(entries) == 0 || entries[0].Tool == nil || entries[0].Tool.SubAgentID != "child-1" {
		t.Fatalf("dispatch tool did not record spawned sub-agent: %+v", entries)
	}
	out := renderToolEntry(*entries[0].Tool, 100, false, m.styles, m.mainChat().md)
	if !strings.Contains(stripAnsiCSI(out), "open tab") {
		t.Fatalf("dispatch tool line should show open tab affordance, got %q", stripAnsiCSI(out))
	}
	m.mainChat().rebuild()
	if subID, ok := m.mainChat().SubAgentTabAt(10, 0); !ok || subID != "child-1" {
		t.Fatalf("expected clickable sub-agent hit on tool row, got id=%q ok=%v", subID, ok)
	}
}

func TestReopenSubAgentTabClearsClosedState(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	if !m.closeSubAgentTab("child-1") {
		t.Fatal("expected close to succeed")
	}
	if !m.chatTabs.closed["child-1"] {
		t.Fatal("closed state was not recorded")
	}

	if cmd := m.reopenSubAgentTabCmd("child-1"); cmd != nil {
		t.Fatal("nil-agent reopen should be synchronous")
	}
	if _, ok := m.chatTabs.tabs["child-1"]; !ok {
		t.Fatal("expected sub-agent tab to reopen")
	}
	if m.chatTabs.closed["child-1"] {
		t.Fatal("reopen should clear closed state so late events can route")
	}
}

func TestSubAgentPromptEventRendersLaunchingPrompt(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started", tools: []string{"Read", "Grep"}})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "prompt", text: "Trace the dispatch behavior and return file:line evidence."})

	tab := m.chatTabs.tabs["child-1"]
	if tab == nil {
		t.Fatal("missing child tab")
	}
	entries := tab.view.Entries()
	if len(entries) != 1 || entries[0].SubAgentStart == nil {
		t.Fatalf("expected prompt to update the startup-card entry, got %+v", entries)
	}
	card := entries[0].SubAgentStart
	if card.Task != "Trace the dispatch behavior and return file:line evidence." {
		t.Fatalf("launch prompt rendered incorrectly in card: %+v", card)
	}
	rendered := stripAnsiCSI(tab.view.renderEntry(entries[0], 0))
	if !strings.Contains(rendered, "Task") || !strings.Contains(rendered, "│ Trace the dispatch behavior") {
		t.Fatalf("startup card did not render task with pipe rail:\n%s", rendered)
	}
}

func TestSubAgentStartCardDaylightBorderUsesReadableBrown(t *testing.T) {
	m := New(nil, false)
	m.palette = builtinPaletteForTest("daylight")
	m.styles = theme.NewStyles(m.palette)
	m.chatTabs.tabs[mainChatTabID].view.palette = m.palette
	m.chatTabs.tabs[mainChatTabID].view.styles = m.styles
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started", title: "sub 1"})
	tab := m.chatTabs.tabs["child-1"]
	if tab == nil || len(tab.view.Entries()) != 1 || tab.view.Entries()[0].SubAgentStart == nil {
		t.Fatalf("missing startup card entry: %+v", tab)
	}
	rendered := tab.view.renderEntry(tab.view.Entries()[0], 0)
	if !strings.Contains(rendered, "38;2;122;78;10") {
		t.Fatalf("daylight sub-agent card border should use readable brown Bright (#7A4E0A), got %q", rendered)
	}
	if !strings.Contains(rendered, "38;2;59;48;32") {
		t.Fatalf("daylight sub-agent card title should use dark primary ink (#3B3020), got %q", rendered)
	}
	if strings.Contains(rendered, "38;2;216;199;160") {
		t.Fatalf("daylight sub-agent card border should not use low-contrast BorderDim (#D8C7A0), got %q", rendered)
	}
}

func TestSubAgentStartCardRendersContiguousMeasuredBox(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{
		id:    "child-1",
		kind:  "started",
		tools: []string{"git_info", "git_push"},
		text:  "sub-agent start: conv=3e33d6c1870b75a5fedadf4e route=open provider=mistralrs model=qwen3-30b-a3b-instruct-2507 tier=fast_light tools=git_info,git_push",
	})
	m.applySubAgentEvent(subAgentEventMsg{
		id:   "child-1",
		kind: "prompt",
		text: "In /Users/bryancostanich/git_repos/bryan_costanich/Cercano, inspect the current git branch/ahead state and push the current main branch to origin if it is ahead. Do not modify files.",
	})

	tab := m.chatTabs.tabs["child-1"]
	if tab == nil || len(tab.view.Entries()) != 1 || tab.view.Entries()[0].SubAgentStart == nil {
		t.Fatalf("missing startup card entry: %+v", tab)
	}
	rendered := stripAnsiCSI(tab.view.renderEntry(tab.view.Entries()[0], 0))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 8 {
		t.Fatalf("startup card too short:\n%s", rendered)
	}
	wantW := lipgloss.Width(lines[0])
	for _, line := range lines {
		if got := lipgloss.Width(line); got != wantW {
			t.Fatalf("card line width = %d, want %d for line %q\nfull card:\n%s", got, wantW, line, rendered)
		}
	}
	body := strings.Join(lines, "\n")
	for _, want := range []string{"╭─ Sub-agent sub 1 started", "Route", "open / mistralrs", "Model", "qwen3-30b-a3b-instruct-2507", "Tier", "fast_light", "Tools", "git_info, git_push", "Task", "│ In /Users"} {
		if !strings.Contains(body, want) {
			t.Fatalf("startup card missing %q:\n%s", want, body)
		}
	}
}

func TestSubAgentErrorEventMarksTabAndShowsMessage(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "error", text: "sub-agent failed: boom"})

	tab := m.chatTabs.tabs["child-1"]
	if tab == nil || !tab.done || !tab.errored {
		t.Fatalf("expected errored done tab, got %+v", tab)
	}
	var joined []string
	for _, e := range tab.view.Entries() {
		joined = append(joined, e.Content)
	}
	if !strings.Contains(strings.Join(joined, "\n"), "sub-agent failed: boom") {
		t.Fatalf("error text not rendered in sub-agent tab entries: %q", strings.Join(joined, "\n"))
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

func TestCleanupFinishedSubAgentTabs(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "c1", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "c2", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "c3", kind: "started"})

	// c1 finished, c2 finished-and-active, c3 still running. This test targets
	// cleanupFinishedSubAgentTabs directly, so mark c1/c2 done without routing
	// through applySubAgentEvent(done), whose approved behavior is now to retire
	// the completed sub-agent tab immediately.
	m.applySubAgentEvent(subAgentEventMsg{id: "c2", kind: "token", inner: chatAssistantDeltaMsg{token: "c2 result worth reading"}})
	m.chatTabs.tabs["c1"].done = true
	m.chatTabs.tabs["c2"].done = true
	m.switchChatTab("c2")

	m.cleanupFinishedSubAgentTabs()

	if _, ok := m.chatTabs.tabs["c1"]; ok {
		t.Fatal("finished non-active tab c1 should be pruned")
	}
	if _, ok := m.chatTabs.tabs["c2"]; !ok {
		t.Fatal("finished but active tab c2 must be spared")
	}
	if _, ok := m.chatTabs.tabs["c3"]; !ok {
		t.Fatal("still-running tab c3 must be kept")
	}
	if _, ok := m.chatTabs.tabs[mainChatTabID]; !ok {
		t.Fatal("main tab must always remain")
	}
	for _, id := range m.chatTabs.order {
		if id == "c1" {
			t.Fatal("order still references pruned c1")
		}
	}

	// Finish c3, move focus to main, and sweep again: with no sub tab active,
	// every finished sub tab is pruned and strip focus is released.
	m.applySubAgentEvent(subAgentEventMsg{id: "c3", kind: "done"})
	m.switchChatTab(mainChatTabID)
	m.chatTabs.focused = true
	m.cleanupFinishedSubAgentTabs()
	if m.hasSubAgentTabs() {
		t.Fatal("all finished sub tabs should be pruned when none is active")
	}
	if m.chatTabs.focused {
		t.Fatal("strip focus should release when no sub tabs remain")
	}
}

func TestCleanupFinishedSubAgentTabs_RemovesActiveErroredEmptySubAgent(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "stale", kind: "started", tools: []string{"Read"}})
	m.switchChatTab("stale")

	tab := m.chatTabs.tabs["stale"]
	if tab == nil {
		t.Fatal("missing stale tab")
	}
	tab.done = true
	tab.errored = true
	m.cleanupFinishedSubAgentTabs()

	if _, ok := m.chatTabs.tabs["stale"]; ok {
		t.Fatal("active errored tab with no assistant/tool transcript should be pruned")
	}
	if m.chatTabs.active != mainChatTabID {
		t.Fatalf("active tab = %q, want main after stale tab prune", m.chatTabs.active)
	}
}

func TestCleanupFinishedSubAgentTabs_KeepsActiveErroredSubAgentWithErrorMessage(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "err", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "err", kind: "error", text: "sub-agent failed: boom"})
	m.switchChatTab("err")

	m.cleanupFinishedSubAgentTabs()

	if _, ok := m.chatTabs.tabs["err"]; !ok {
		t.Fatal("active errored tab with explicit error message should be kept for review")
	}
}

func TestCleanupFinishedSubAgentTabs_KeepsActiveErroredSubAgentWithTranscript(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "err", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "err", kind: "token", inner: chatAssistantDeltaMsg{token: "partial findings"}})
	m.switchChatTab("err")

	tab := m.chatTabs.tabs["err"]
	if tab == nil {
		t.Fatal("missing err tab")
	}
	tab.done = true
	tab.errored = true
	m.cleanupFinishedSubAgentTabs()

	if _, ok := m.chatTabs.tabs["err"]; !ok {
		t.Fatal("active errored tab with assistant transcript should be kept for review")
	}
}

func TestChatDoneMarksAndPrunesStaleSubAgentTabs(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "stale", kind: "started"})
	m.switchChatTab("stale")

	m.finishStaleSubAgentTabs("sub-agent stopped without a terminal event")
	m.cleanupFinishedSubAgentTabs()

	if _, ok := m.chatTabs.tabs["stale"]; ok {
		t.Fatal("stale empty sub-agent tab should be pruned once parent turn ends")
	}
}

func TestCleanupFinishedSubAgentTabs_SparesRestored(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "r1", kind: "started"})
	// Mark it as applyResume would: a finished tab rebuilt from a transcript.
	if tab := m.chatTabs.tabs["r1"]; tab != nil {
		tab.done = true
		tab.restored = true
	}
	// Focus main so r1 is finished AND non-active: a normal finished tab would
	// be swept here, but a restored one must survive.
	m.switchChatTab(mainChatTabID)
	m.cleanupFinishedSubAgentTabs()
	if _, ok := m.chatTabs.tabs["r1"]; !ok {
		t.Fatal("restored finished tab must survive cleanup")
	}
}

// A sub-agent that finishes must retire its own tab on its OWN done event,
// without waiting for the parent turn's chatDoneMsg sweep. This is the fix for
// the orphaned-tab bug: a sub-agent that finishes after the parent turn's sweep
// already ran previously sat visible-but-finished forever.
func TestSubAgentDoneEventPrunesOwnTabImmediately(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started"})
	// The user is NOT viewing the sub tab — they're back on main.
	m.switchChatTab(mainChatTabID)
	if _, ok := m.chatTabs.tabs["sa"]; !ok {
		t.Fatal("sub tab should exist while running")
	}
	// The sub-agent finishes. No parent chatDoneMsg is delivered here.
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "done"})
	if _, ok := m.chatTabs.tabs["sa"]; ok {
		t.Fatal("finished non-active sub tab must be retired on its own done event")
	}
}

// A sub-agent that finishes successfully must retire its own tab on its OWN
// done event regardless of transcript length. The transcript remains reopenable
// elsewhere; the tab strip itself is ephemeral and should not accumulate
// finished sub-agent tabs just because they produced output.
func TestSubAgentDoneEventPrunesSubstantiveTabImmediately(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started"})
	// Substantive assistant output the user can reopen later.
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: chatAssistantDeltaMsg{token: "found the thing"}})
	// User is on main — not viewing the tab — when it finishes.
	m.switchChatTab(mainChatTabID)
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "done"})
	if _, ok := m.chatTabs.tabs["sa"]; ok {
		t.Fatal("finished substantive sub tab must retire on its own done event")
	}
}

func TestSubAgentDoneEventPrunesActiveSubstantiveTabImmediately(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: chatAssistantDeltaMsg{token: "found the thing"}})
	m.switchChatTab("sa")
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "done"})
	if _, ok := m.chatTabs.tabs["sa"]; ok {
		t.Fatal("finished active substantive sub tab must retire on its own done event")
	}
	if m.chatTabs.active != mainChatTabID {
		t.Fatalf("active tab = %q, want main after active sub tab retires", m.chatTabs.active)
	}
}
