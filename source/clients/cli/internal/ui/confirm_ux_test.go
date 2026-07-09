package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// The key hints render on their own second line so a long tool summary never
// wraps mid-hint (they previously trailed the summary on one line and wrapped
// awkwardly at narrow widths).
func TestRenderConfirmPrompt_HintsOnSecondLine(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "git_land", Args: `{"feature":"perf/x","trunk":"main"}`, Permission: "X",
	}))
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), s)
	}
	if strings.Contains(lines[0], "[y]es") {
		t.Errorf("key hints must not be on the summary line: %q", lines[0])
	}
	for _, want := range []string{"[y]es", "[n]o", "[d]etails"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("expected %q on the hint line, got: %q", want, lines[1])
		}
	}
}

// The [a]lways hint for MCP tools lands on the hint line too.
func TestRenderConfirmPrompt_MCPAlwaysOnHintLine(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "mcp__github__list_issues", Args: "{}", Permission: "W",
	}))
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), s)
	}
	if !strings.Contains(lines[1], "[a]lways") {
		t.Errorf("expected [a]lways on the hint line, got: %q", lines[1])
	}
}

// buildConfirmClickModel makes a Model with a folded tool entry in scrollback
// and a pending confirm, geometry arranged so screen Y == viewport-local Y.
func buildConfirmClickModel() Model {
	const w, vh = 100, 40
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", w-2, vh)
	cv.SetEntriesSlice([]*Entry{
		{Role: RoleUser, Content: "do stuff"},
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "Read", ArgsSummary: "a.go", FullArgs: `{"path":"a.go"}`, FullResult: "body", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Role: RoleAssistant, Content: "prose after the tools"},
	})
	cv.rebuild()
	return Model{
		width:          w,
		height:         vh + 6,
		scrollbarTop:   0,
		chat:           cv,
		pendingConfirm: &confirmRequest{},
	}
}

// While a confirm is pending, a left click on a tool entry's arrow row still
// toggles its fold — the user must be able to review prior tool output before
// answering y/n. The confirm itself must stay pending.
func TestConfirmPending_ClickTogglesFold(t *testing.T) {
	m := buildConfirmClickModel()
	line := findPlainLine(t, &m.chat, "Read")
	m = send(t, m, tea.MouseClickMsg{X: 2, Y: line, Button: tea.MouseLeft})
	if m.chat.entries[1].Tool.Folded {
		t.Error("click on the arrow row should unfold the entry while a confirm is pending")
	}
	if m.pendingConfirm == nil {
		t.Error("a fold toggle must not resolve the pending confirm")
	}
}

// Clicks that don't land on an arrow row stay swallowed while a confirm is
// pending — no selection drag, no input focus, confirm still pending.
func TestConfirmPending_ProseClickStaysIgnored(t *testing.T) {
	m := buildConfirmClickModel()
	line := findPlainLine(t, &m.chat, "prose after the tools")
	m = send(t, m, tea.MouseClickMsg{X: 10, Y: line, Button: tea.MouseLeft})
	if !m.chat.entries[1].Tool.Folded {
		t.Error("a prose click must not toggle any fold")
	}
	if m.pendingConfirm == nil {
		t.Error("a prose click must not resolve the pending confirm")
	}
	if m.chat.SelectionDragging() {
		t.Error("a click during a pending confirm must not begin a selection drag")
	}
}
