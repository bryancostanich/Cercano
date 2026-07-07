package ui

import (
	"strings"
	"testing"
	"time"
)

func expandedReadEntry() []*Entry {
	return []*Entry{
		{Tool: &ToolEntry{
			ToolUseID:   "u1",
			ToolName:    "Read",
			ArgsSummary: "foo.go",
			FullResult:  "line one\nline two\nline three\nline four",
			Status:      ToolStatusComplete,
			Folded:      false,
		}},
	}
}

// A standalone expanded entry renders a collapse rail (│ … ╰) and records a
// left-gutter rail zone [0,6) targeting the entry.
func TestExpandedEntry_RendersRailAndRecordsZone(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice(expandedReadEntry())
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
	if !strings.Contains(got, "│") || !strings.Contains(got, "╰") {
		t.Fatalf("expected a rail (│ … ╰) in the expanded body, got:\n%s", got)
	}

	r, ok := c.arrowRowAt(2, 0) // far-left of a body line
	if !ok || r.railMax == 0 {
		t.Fatalf("expected a rail zone at a body line, got %+v ok=%v", r, ok)
	}
	if r.railMin != 0 || r.railMax != 6 {
		t.Errorf("rail zone = [%d,%d), want [0,6)", r.railMin, r.railMax)
	}
}

// Clicking the rail gutter collapses a standalone entry; a content click falls
// through so the output stays selectable.
func TestMouseToggleFold_StandaloneRail(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice(expandedReadEntry())
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	if c.MouseToggleFold(20, 2) {
		t.Error("body content (x=20) should not be handled as a collapse")
	}
	if c.entries[0].Tool.Folded {
		t.Error("a content click must not collapse the entry")
	}
	if !c.MouseToggleFold(2, 2) {
		t.Fatal("click on the rail gutter (x=2) should be handled")
	}
	if !c.entries[0].Tool.Folded {
		t.Error("clicking the rail should collapse the entry")
	}
}

// In an expanded group, the far-left gutter is the outer group rail (collapses
// the whole run); a nested expanded entry's own rail is one level in; body
// content falls through.
func TestGroupRail_OuterAndInnerZones(t *testing.T) {
	c := newTestChatView(100, 30)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "b.go", FullResult: "one\ntwo\nthree", Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: false}},
	})
	c.groupExpanded[0] = true // run starts at entry 0
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	bodyLine := -1
	for i, l := range c.PlainLines() {
		if strings.Contains(l, "two") { // a result line of the second entry
			bodyLine = i
			break
		}
	}
	if bodyLine < 0 {
		t.Fatalf("could not find the nested entry body line, got:\n%s", strings.Join(c.PlainLines(), "\n"))
	}

	// Far-left gutter (x=2) → group rail.
	if r, ok := c.arrowRowAt(bodyLine, 2); !ok || !r.group {
		t.Errorf("x=2 should be the group rail, got %+v ok=%v", r, ok)
	}
	// One level in (x=6) → the nested entry's own rail.
	if r, ok := c.arrowRowAt(bodyLine, 6); !ok || r.group {
		t.Errorf("x=6 should be the nested entry rail (not group), got %+v ok=%v", r, ok)
	}
	// Content (x=20) → falls through.
	if _, ok := c.arrowRowAt(bodyLine, 20); ok {
		t.Error("body content (x=20) should fall through to selection")
	}
}
