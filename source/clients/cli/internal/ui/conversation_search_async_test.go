package ui

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"
)

func TestConversationSearchTypingDoesNotScanInline(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.search.input.Value() != "n" {
		t.Fatal("typing must update the editor immediately")
	}
	if len(m.search.cache) != 0 {
		t.Fatal("typing scanned the transcript inline instead of deferring the expensive work")
	}
	if cmd == nil {
		t.Fatal("typing must schedule search work")
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search != nil {
		t.Fatal("Escape must close while matching is still outstanding")
	}
}

// The existing layout tests call reducer helpers directly and intentionally do
// not run Bubble Tea commands (some commands access the system clipboard).
// Drain only search work in those tests, discarding their scheduled command.
func settleSearch(t testing.TB, m *Model) {
	t.Helper()
	for i := 0; i < 4 && m.search != nil && (m.search.dirty || m.search.working); i++ {
		if m.search.working {
			m.search.working = false
			m.search.dirty = true
		}
		cmd := m.conversationSearchCmd()
		if cmd == nil {
			return
		}
		result, ok := cmd().(conversationSearchResultMsg)
		if !ok {
			t.Fatal("search command returned wrong message")
		}
		m.applyConversationSearchResult(result)
	}
}
func sendSearch(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	m = send(t, m, msg)
	settleSearch(t, &m)
	return m
}

func TestConversationSearchCoalescesTypingAndIgnoresStaleResults(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("")
	next, first := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	next, second := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	m = next.(Model)
	if second != nil {
		t.Fatal("typing must not start another concurrent search worker")
	}
	if m.search.input.Value() != "ne" {
		t.Fatal("typing blocked while first worker was outstanding")
	}
	next, latest := m.Update(first())
	m = next.(Model)
	if len(m.search.matches) != 0 {
		t.Fatal("old-query results were painted over the new query")
	}
	if latest == nil {
		t.Fatal("latest query was not scheduled after previous worker finished")
	}
	next, _ = m.Update(latest())
	m = next.(Model)
	if m.search.query != "ne" || len(m.search.matches) != 4 {
		t.Fatalf("latest query not applied: %q (%d matches)", m.search.query, len(m.search.matches))
	}
}

func TestConversationSearchLateResultCannotReopenClosedSession(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	m = send(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	replacement := m.search
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.search != replacement || m.search.input.Value() != "" || len(m.search.matches) != 0 {
		t.Fatal("late result affected a replacement session")
	}
}

func TestConversationSearchWorkerOwnsSnapshot(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "needle"})
	m = next.(Model)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	// Mutate both message source and its rendered layout while matching runs.
	m.mainChat().entries[0].Content = "replaced text"
	m.mainChat().markTranscriptDirty()
	m.refreshViewport()
	next, _ = m.Update(<-done)
	m = next.(Model)
	for _, match := range m.search.matches {
		if match.entry == m.mainChat().entries[0] {
			t.Fatal("old spans painted over changed text")
		}
	}
}

func TestConversationSearchResultRebasesPrependedHistory(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("")
	target := m.mainChat().entries[0]
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "needle"})
	m = next.(Model)
	m.mainChat().SetEntries(append([]*Entry{{Role: RoleUser, Content: "older message"}}, m.mainChat().entries...))
	m.refreshViewport()
	result := cmd().(conversationSearchResultMsg)
	oldRow := result.matches[0].spans[0].row
	next, followup := m.Update(result)
	m = next.(Model)
	if len(m.search.matches) != 4 || m.search.matches[0].entry != target {
		t.Fatal("lost unchanged matches during history prepend")
	}
	if m.search.matches[0].spans[0].row <= oldRow {
		t.Fatal("match was not rebased to its new transcript row")
	}
	if followup == nil {
		t.Fatal("updated history was not scheduled for searching")
	}
}

func TestConversationSearchIdleRefreshDoesNotScheduleMatching(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("needle")
	settleSearch(t, &m)
	revision := m.search.revision
	m.refreshVisibleDynamicViewport()
	if m.search.revision != revision || m.search.dirty {
		t.Fatal("unchanged transcript refresh invalidated search")
	}
}

func TestConversationSearchRefreshFindsOffscreenStreamingText(t *testing.T) {
	m := searchModel(t)
	entry := &Entry{Role: RoleAssistant, Content: "before", Streaming: true}
	m.mainChat().SetEntries([]*Entry{{Role: RoleUser, Content: strings.Repeat("long history\n", 80)}, entry})
	m.relayout()
	m.openConversationSearch("needle")
	settleSearch(t, &m)
	m.mainChat().SetYOffset(0)
	entry.Content = "after needle"
	m.refreshVisibleDynamicViewport()
	settleSearch(t, &m)
	if len(m.search.matches) != 1 {
		t.Fatal("off-screen streamed message was not refreshed for search")
	}
}
