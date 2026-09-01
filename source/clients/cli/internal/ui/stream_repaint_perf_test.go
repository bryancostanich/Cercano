package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestStreamDirtyTickUpdatesVisibleTailWithoutRebuildingHistory(t *testing.T) {
	m := New(nil, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 126, Height: 47})
	m = next.(Model)

	entries := make([]*Entry, 0, 22)
	for i := 0; i < 20; i++ {
		entries = append(entries, &Entry{Role: RoleAssistant, Content: fmt.Sprintf("entry %d\n", i) + strings.Repeat("history line ", 20)})
	}
	entries = append(entries, &Entry{Role: RoleUser, Content: "go"})
	entries = append(entries, &Entry{Role: RoleAssistant, Content: "", Streaming: true})
	m.mainChat().SetEntries(entries)
	m.mainChat().SetStreaming(true)
	m.mainChat().GotoBottom()
	m.streaming = true
	m.animTickActive = true

	if len(m.mainChat().layout.units) < 2 {
		t.Fatal("test setup expected history and streaming tail layout units")
	}
	firstHistoryUnit := &m.mainChat().layout.units[0]

	m.mainChat().Apply(chatAssistantDeltaMsg{token: "hello"})
	m.chatDirty = true
	next, _ = m.Update(progressAnimTickMsg(time.Now()))
	m = next.(Model)

	if m.chatDirty {
		t.Fatalf("dirty stream tick should flush chatDirty")
	}
	if len(m.mainChat().layout.units) == 0 || &m.mainChat().layout.units[0] != firstHistoryUnit {
		t.Fatalf("dirty stream tick rebuilt frozen history layout")
	}
	if !strings.Contains(chatLayoutContent(m.mainChat()), "hello") {
		t.Fatalf("dirty stream tick did not repaint visible streaming tail")
	}
}
