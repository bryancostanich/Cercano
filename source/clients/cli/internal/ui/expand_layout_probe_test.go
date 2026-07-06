package ui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestModel_ExpandedToolResultStaysWithinTerminal reproduces the reported
// "expanding an arrow fucks up the prompt rendering" symptom headlessly: with a
// big, wide expanded tool result, the composited Model.View() must still be
// exactly the terminal's rows/cols. If the expanded body emits more rows than
// the viewport allots, or a row wider than the terminal, it wraps/scrolls in
// the real terminal and shoves the prompt/status — the observed breakage.
//
// Sized to the reporter's terminal (126x29) with a grep-shaped result.
func TestModel_ExpandedToolResultStaysWithinTerminal(t *testing.T) {
	m := New(nil, false)
	m.width = 126
	m.height = 29
	m.relayout()

	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("source/server/internal/capabilities/builtins/fs_write.go:")
		b.WriteString(strconv.Itoa(100 + i))
		b.WriteString(":    \"required\": [\"path\", \"old_string\", \"new_string\"], trailing text to widen the line well past the wrap\n")
	}
	m.chat.SetEntriesSlice([]*Entry{
		{Role: RoleUser, Content: "grep the edit schema"},
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "grep", ArgsSummary: "edit_file", FullResult: b.String(), Status: ToolStatusComplete, Folded: false}},
	})
	m.relayout()

	view := stripAnsiCSI(m.View().Content)
	rows := strings.Split(view, "\n")

	if len(rows) > m.height {
		t.Errorf("composed view has %d rows, exceeds terminal height %d — content bleeds past the prompt", len(rows), m.height)
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > m.width {
			t.Errorf("row %d width %d exceeds terminal width %d: %q", i, w, m.width, r)
		}
	}
}

// The loading state (fetch in flight, spinner) must also fit — that's the async
// window the transient likely lives in.
func TestModel_LoadingToolEntryStaysWithinTerminal(t *testing.T) {
	m := New(nil, false)
	m.width = 126
	m.height = 29
	m.relayout()

	m.chat.SetEntriesSlice([]*Entry{
		{Role: RoleUser, Content: "read a file"},
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "Read", ArgsSummary: "big.go", Status: ToolStatusComplete, Folded: false, Loading: true}},
	})
	m.relayout()

	view := stripAnsiCSI(m.View().Content)
	rows := strings.Split(view, "\n")
	if len(rows) > m.height {
		t.Errorf("loading view has %d rows, exceeds terminal height %d", len(rows), m.height)
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > m.width {
			t.Errorf("row %d width %d exceeds terminal width %d: %q", i, w, m.width, r)
		}
	}
}
