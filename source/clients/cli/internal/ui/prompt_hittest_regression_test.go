package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// promptTopOLD reproduces the hand-summed layout the fix replaced, so we can
// prove it drifts from the real render in the configs the fix now handles.
func (m Model) promptTopOLD() int {
	top := m.contentTop()
	top += m.mainChat().Height()
	if m.recap != "" {
		top += 2
	}
	if !m.contentPageActive() {
		top += len(m.queuedLines())
	}
	top++
	if hint := m.renderSlashSuggestions(); hint != "" && !m.contentPageActive() {
		top += strings.Count(hint, "\n") + 1
	}
	return top
}

// screenRowOfSentinel joins the frame and returns the 0-based SCREEN row of the
// sentinel (not the slice index — parts contain multi-line elements).
func screenRowOfSentinel(parts []string, sentinel string) int {
	for i, ln := range strings.Split(strings.Join(parts, "\n"), "\n") {
		if strings.Contains(ln, sentinel) {
			return i
		}
	}
	return -1
}

// With sub-agent tabs present, the OLD hand-summed promptTop omitted the two
// tab-strip rows (contentTop does not count them, but the real layout renders
// them), so it drifted above the true input row — the exact class of bug that
// made prompt clicks miss. The NEW promptTop, derived from composeFrame, lands
// on the real row.
func TestOLDpromptTop_DriftsWithSubAgentTabs(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown(strings.Repeat("body\n\n", 30))
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	m.refreshViewport()
	if !m.hasSubAgentTabs() {
		t.Fatal("precondition: sub-agent tabs should be present")
	}
	m.input.Focus()
	m.input.SetValue("ZZOLD")

	parts, _ := m.composeFrame()
	real := screenRowOfSentinel(parts, "ZZOLD")
	if real < 0 {
		t.Fatal("sentinel not rendered")
	}

	newTop := m.promptTop()
	oldTop := m.promptTopOLD()
	t.Logf("real=%d OLD=%d NEW=%d", real, oldTop, newTop)

	if newTop != real {
		t.Fatalf("NEW promptTop=%d must equal real render row %d", newTop, real)
	}
	if oldTop == real {
		t.Fatalf("expected OLD promptTop to drift from real row %d, but it matched (fix would be untested)", real)
	}
}
