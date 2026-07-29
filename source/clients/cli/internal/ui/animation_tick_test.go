package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestProgressAnimTick_CompactingOnlyDoesNotRefreshViewport(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.compacting = true
	now := time.Unix(100, 0)

	next, cmd := m.Update(progressAnimTickMsg(now))
	m = next.(Model)

	if !m.animTickActive || cmd == nil {
		t.Fatalf("compacting should keep the cheap animation tick alive; active=%v cmd=%v", m.animTickActive, cmd)
	}
	if !m.lastAnimViewportRefresh.IsZero() {
		t.Fatalf("compaction-only tick should not rebuild transcript viewport, got last refresh %v", m.lastAnimViewportRefresh)
	}
}

func TestProgressAnimTick_AnimationOnlyViewportRefreshesEveryFrame(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "", Tool: &ToolEntry{ToolName: "research", Status: ToolStatusInProgress}})

	first := time.Unix(100, 0)
	next, _ := m.Update(progressAnimTickMsg(first))
	m = next.(Model)
	if !m.lastAnimViewportRefresh.Equal(first) {
		t.Fatalf("first animation-only tick should refresh viewport, got %v", m.lastAnimViewportRefresh)
	}

	second := first.Add(50 * time.Millisecond)
	next, _ = m.Update(progressAnimTickMsg(second))
	m = next.(Model)
	if !m.lastAnimViewportRefresh.Equal(second) {
		t.Fatalf("next animation-only tick should refresh viewport; got %v want %v", m.lastAnimViewportRefresh, second)
	}
}

func TestProgressAnimTick_ContentDirtyBypassesAnimationThrottle(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.lastAnimViewportRefresh = time.Unix(100, 0)
	m.chatDirty = true
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "new content"})

	next, _ := m.Update(progressAnimTickMsg(time.Unix(100, 1)))
	m = next.(Model)

	if m.chatDirty {
		t.Fatal("content repaint should flush chatDirty immediately")
	}
	if !m.lastAnimViewportRefresh.IsZero() {
		t.Fatalf("content repaint should reset animation refresh throttle, got %v", m.lastAnimViewportRefresh)
	}
}

func TestThinkingSpinnerAdvancesEveryProgressTick(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.animTickActive = true
	m.streaming = true
	m.turnActivity = "thinking"
	m.turnStart = time.Unix(10, 0)
	m.turnModel = "local"
	m.mainChat().SetStreaming(true)
	m.mainChat().SetEntriesSlice([]*Entry{{Role: RoleAssistant, Streaming: true}})
	m.refreshViewport()

	base := time.Unix(12, 0)
	var got []string
	for i := 0; i < 8; i++ {
		next, _ := m.Update(progressAnimTickMsg(base.Add(time.Duration(i) * 50 * time.Millisecond)))
		m = next.(Model)
		got = append(got, thinkingGlyphFromView(t, m.View().Content))
	}
	want := []string{"▌", "▘", "▀", "▝", "▐", "▗", "▄", "▖"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("thinking glyphs = %#v, want %#v", got, want)
		}
	}
}

func thinkingGlyphFromView(t *testing.T, view string) string {
	t.Helper()
	out := plain(view)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "thinking") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	t.Fatalf("thinking line not found in view:\n%s", out)
	return ""
}
