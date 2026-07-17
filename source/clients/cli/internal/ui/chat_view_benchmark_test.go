package ui

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkChatViewViewLongTranscript(b *testing.B) {
	c := newTestChatView(100, 30)
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = fmt.Sprintf("long transcript line %05d %s", i, strings.Repeat("x", 40))
	}
	c.layout = transcriptLayout{units: []renderUnit{{kind: unitEntry, lineCount: len(lines), lines: lines}}, totalLines: len(lines)}
	c.scroll.SetTotalLineCount(len(lines))
	c.SetYOffset(5000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
