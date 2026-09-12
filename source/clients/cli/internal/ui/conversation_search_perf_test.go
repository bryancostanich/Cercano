package ui

import (
	"strings"
	"testing"
)

// Both cases have exactly the same history: 1000 messages, eight occurrences
// of needle in each. Only search visibility changes the refresh path.
func BenchmarkConversationSearchStreamingRefresh(b *testing.B) {
	for _, open := range []bool{false, true} {
		name := "closed"
		if open {
			name = "open"
		}
		b.Run(name, func(b *testing.B) {
			m := searchModel(b)
			entries := make([]*Entry, 1000)
			for i := range entries {
				entries[i] = &Entry{Role: RoleAssistant, Content: strings.Repeat("alpha beta needle gamma delta epsilon zeta eta theta.\n\n", 8)}
			}
			m.mainChat().SetEntries(entries)
			m.relayout()
			if open {
				m.openConversationSearch("")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.refreshVisibleDynamicViewport()
			}
		})
	}
}

// Each query below has exactly 8000 matches. Cold indexing visits roughly 400k
// characters and parses 1000 messages; it should allocate more than warm matching.
// Otherwise projection is warm; changing the
// query still scans the glyph index. Snapshot and result application happen on
// the UI thread; command execution happens in the background in production.
func BenchmarkConversationSearchPipeline(b *testing.B) {
	for _, phase := range []string{"snapshot", "cold-worker", "worker", "apply", "view"} {
		b.Run(phase, func(b *testing.B) {
			m := searchModel(b)
			entries := make([]*Entry, 1000)
			for i := range entries {
				entries[i] = &Entry{Role: RoleAssistant, Content: strings.Repeat("alpha beta needle gamma delta epsilon zeta eta theta.\n\n", 8)}
			}
			m.mainChat().SetEntries(entries)
			m.relayout()
			m.openConversationSearch("needle")
			settleSearch(b, &m)
			if len(m.search.matches) != 8000 {
				b.Fatalf("matches=%d", len(m.search.matches))
			}
			m.search.input.SetValue("gamma")
			m.search.invalidate(true)
			if phase == "cold-worker" {
				m.search.cache = nil
			}
			cmd := m.conversationSearchCmd()
			result := cmd().(conversationSearchResultMsg)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch phase {
				case "snapshot":
					m.search.dirty = true
					m.search.working = false
					_ = m.conversationSearchCmd()
				case "worker", "cold-worker":
					_ = cmd()
				case "apply":
					m.applyConversationSearchResult(result)
				case "view":
					_ = m.View()
				}
			}
		})
	}
}
