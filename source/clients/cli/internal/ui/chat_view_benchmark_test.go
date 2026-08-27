package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkChatViewViewLongTranscript(b *testing.B) {
	c := newTestChatView(126, 47)
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = fmt.Sprintf("long transcript line %05d %s", i, strings.Repeat("x", 80))
	}
	seedChatViewLines(c, lines)
	c.SetYOffset(5000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.View()
	}
}

func BenchmarkChatViewViewLargeHistoryNoAnimation(b *testing.B) {
	c := newTestChatView(126, 47)
	entries := make([]*Entry, 1200)
	body := strings.Repeat("rendered chat history line with enough prose to wrap and style ", 25)
	for i := range entries {
		entries[i] = &Entry{Role: RoleAssistant, Content: fmt.Sprintf("entry %d\n%s", i, body)}
	}
	c.SetEntries(entries)
	c.GotoBottom()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.View()
	}
}

func BenchmarkChatViewViewAnimatedTail(b *testing.B) {
	c := newTestChatView(126, 47)
	entries := make([]*Entry, 1200)
	body := strings.Repeat("rendered chat history line with enough prose to wrap and style ", 25)
	for i := range entries {
		entries[i] = &Entry{Role: RoleAssistant, Content: fmt.Sprintf("entry %d\n%s", i, body)}
	}
	c.SetEntries(entries)
	c.SetStreaming(true)
	c.SetTurnStatus(turnStatus{activity: "working", start: time.Unix(1, 0), model: "local"})
	c.SetEntries(entries)
	c.GotoBottom()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.SetAnimationTime(time.Unix(2, int64(i)*50_000_000))
		_ = c.View()
	}
}

func BenchmarkChatViewSetEntriesLongTranscriptCached(b *testing.B) {
	c := newTestChatView(100, 30)
	entries := make([]*Entry, 2000)
	for i := range entries {
		entries[i] = &Entry{Role: RoleSystem, Content: fmt.Sprintf("cached historical entry %05d %s", i, strings.Repeat("x", 40))}
	}
	c.SetEntries(entries) // warm frozen-entry render and split caches

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.SetEntries(entries)
	}
}
