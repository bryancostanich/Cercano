package ui

import (
	"strings"
	"testing"
)

func expandedReadEntry() []*Entry {
	return []*Entry{
		{Tool: &ToolEntry{
			ToolUseID:  "u1",
			ToolName:   "Read",
			ArgsSummary: "foo.go",
			FullResult: "line one\nline two\nline three\nline four",
			Status:     ToolStatusComplete,
			Folded:     false,
		}},
	}
}

// An expanded entry renders a collapse rail (│ down the body, ╰ hook at the
// bottom), and records rail rows at the absolute rail column.
func TestExpandedEntry_RendersRailAndRecordsRows(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice(expandedReadEntry())
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
	if !strings.Contains(got, "│") || !strings.Contains(got, "╰") {
		t.Fatalf("expected a rail (│ … ╰) in the expanded body, got:\n%s", got)
	}

	r, ok := c.arrowRowAt(1) // first body line
	if !ok || !r.rail {
		t.Fatalf("expected a rail row at body line 1, got %+v ok=%v", r, ok)
	}
	// Single entry: renderToolEntry col 2 + renderToolGroupBlock entryIndent 2.
	if r.railCol != 4 {
		t.Errorf("railCol = %d, want 4", r.railCol)
	}
}

// Clicking the rail gutter collapses the entry.
func TestMouseToggleFold_RailGutterCollapses(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice(expandedReadEntry())
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	r, ok := c.arrowRowAt(2) // a body line, not the arrow
	if !ok || !r.rail {
		t.Fatalf("expected a rail row at body line 2")
	}
	if !c.MouseToggleFold(r.railCol, 2) {
		t.Fatal("click on the rail gutter should be handled")
	}
	if !c.entries[0].Tool.Folded {
		t.Error("clicking the rail should collapse (fold) the entry")
	}
}

// Clicking the body content (right of the rail) does NOT collapse — it falls
// through so text selection still works.
func TestMouseToggleFold_RailContentClickFallsThrough(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice(expandedReadEntry())
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	r, ok := c.arrowRowAt(2)
	if !ok || !r.rail {
		t.Fatalf("expected a rail row at body line 2")
	}
	if c.MouseToggleFold(r.railCol+10, 2) {
		t.Error("clicking body content should not be handled as a collapse")
	}
	if c.entries[0].Tool.Folded {
		t.Error("a content click must not collapse the entry")
	}
}
