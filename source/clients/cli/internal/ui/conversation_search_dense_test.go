package ui

import (
	"cercano/source/clients/cli/internal/theme"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestConversationSearchDenseHighlightOutputBounded(t *testing.T) {
	m := searchModel(t)
	c := m.mainChat()
	c.search = &conversationSearch{active: -1, paint: map[int][]searchPaint{}}
	for _, n := range []int{4, 8, 12} {
		c.search.paint[0] = nil
		for i := 0; i < n; i++ {
			c.search.paint[0] = append(c.search.paint[0], searchPaint{match: i, span: searchSpan{from: 2 * i, to: 2*i + 1}})
		}
		line := strings.Repeat("t ", n)
		got := c.renderSearchOnLine(line, 0)
		t.Logf("matches=%d input_bytes=%d output_bytes=%d", n, len(line), len(got))
		if ansi.Strip(got) != line {
			t.Fatal("highlight changed visible text")
		}
		if len(got) > len(line)+n*64 {
			t.Errorf("highlight output grew beyond bounded per-match formatting: %d bytes", len(got))
		}
	}
}

func TestConversationSearchHighlightPreservesStyledUnicode(t *testing.T) {
	m := searchModel(t)
	c := m.mainChat()
	c.search = &conversationSearch{active: 1, paint: map[int][]searchPaint{0: {
		{match: 0, span: searchSpan{from: 0, to: 2}},
		{match: 1, span: searchSpan{from: 5, to: 6}},
	}}}
	line := "\x1b[31m世界 e\u0301\x1b[0m!"
	got := c.renderSearchOnLine(line, 0)
	if ansi.Strip(got) != ansi.Strip(line) || ansi.StringWidth(got) != ansi.StringWidth(line) {
		t.Fatalf("text or width changed: %q", got)
	}
	if !strings.Contains(got, "\x1b[4m") {
		t.Fatal("inactive match underline missing")
	}
	if !strings.Contains(got, theme.SelectionBackgroundSGR(c.palette)) {
		t.Fatal("active match background missing")
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatal("source foreground missing")
	}
}

func BenchmarkConversationSearchDenseHighlight(b *testing.B) {
	m := searchModel(b)
	c := m.mainChat()
	c.search = &conversationSearch{active: 0, paint: map[int][]searchPaint{}}
	for i := 0; i < 40; i++ {
		c.search.paint[0] = append(c.search.paint[0], searchPaint{match: i, span: searchSpan{from: 2 * i, to: 2*i + 1}})
	}
	line := strings.Repeat("t ", 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.renderSearchOnLine(line, 0)
	}
}
