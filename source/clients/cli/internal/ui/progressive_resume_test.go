package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestChatViewProgressiveLoadSentinelAndBackfill(t *testing.T) {
	cv := newTestChatView(40, 8)
	cv.BeginProgressiveLoad([]*Entry{{Role: RoleAssistant, Content: "tail"}}, true)
	content := chatLayoutContent(cv)
	if !strings.Contains(content, progressiveOlderLoadingText) || !strings.Contains(content, "tail") {
		t.Fatalf("progressive tail content = %q", content)
	}
	cv.PrependProgressiveEntries([]*Entry{{Role: RoleUser, Content: "older"}})
	content = chatLayoutContent(cv)
	if strings.Index(content, "older") > strings.Index(content, "tail") {
		t.Fatalf("older chunk was not prepended before tail: %q", content)
	}
	cv.CompleteProgressiveLoad()
	content = chatLayoutContent(cv)
	if strings.Contains(content, progressiveOlderLoadingText) {
		t.Fatalf("sentinel still present after completion: %q", content)
	}
}

func TestChatViewProgressivePrependPreservesNonBottomAnchor(t *testing.T) {
	cv := newTestChatView(60, 5)
	entries := make([]*Entry, 0, 16)
	for i := 0; i < 16; i++ {
		entries = append(entries, &Entry{Role: RoleAssistant, Content: fmt.Sprintf("tail %02d", i)})
	}
	cv.BeginProgressiveLoad(entries, true)
	cv.SetYOffset(cv.TotalLineCount() / 2)
	before := cv.visibleLines(cv.YOffset(), cv.Height())[0]
	if !strings.Contains(ansi.Strip(before), "tail") {
		t.Fatalf("test setup expected a tail line anchor, got %q", before)
	}
	cv.PrependProgressiveEntries([]*Entry{{Role: RoleAssistant, Content: "older 0"}, {Role: RoleAssistant, Content: "older 1"}, {Role: RoleAssistant, Content: "older 2"}})
	after := cv.visibleLines(cv.YOffset(), cv.Height())[0]
	if ansi.Strip(after) != ansi.Strip(before) {
		t.Fatalf("visible anchor changed after prepend: before %q after %q", before, after)
	}
}

func BenchmarkProgressiveResumeTailVsFullLayout(b *testing.B) {
	const total = 1200
	const tail = 80
	all := make([]*Entry, 0, total)
	body := strings.Repeat("rendered chat history line with enough prose to wrap and style ", 12)
	for i := 0; i < total; i++ {
		all = append(all, &Entry{Role: RoleAssistant, Content: fmt.Sprintf("entry %d\n%s", i, body)})
	}
	b.Run("full-set-entries", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cv := newTestChatView(126, 47)
			cv.SetEntries(all)
		}
	})
	b.Run("tail-begin-progressive", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cv := newTestChatView(126, 47)
			cv.BeginProgressiveLoad(all[len(all)-tail:], true)
		}
	})
}
