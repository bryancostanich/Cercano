package ui

import (
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

func TestProgressAnimTick_AnimationOnlyViewportRefreshIsThrottled(t *testing.T) {
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
	if !m.lastAnimViewportRefresh.Equal(first) {
		t.Fatalf("animation-only tick before throttle interval should not refresh viewport; got %v want %v", m.lastAnimViewportRefresh, first)
	}

	third := first.Add(animationViewportRefreshInterval)
	next, _ = m.Update(progressAnimTickMsg(third))
	m = next.(Model)
	if !m.lastAnimViewportRefresh.Equal(third) {
		t.Fatalf("animation-only tick after throttle interval should refresh viewport; got %v want %v", m.lastAnimViewportRefresh, third)
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
