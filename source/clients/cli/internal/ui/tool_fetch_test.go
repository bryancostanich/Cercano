package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// Expanding a folded tool entry whose full body hasn't been fetched queues a
// lazy GetToolCall and marks the entry loading; the host drains the queue once.
func TestToggleEntryFold_QueuesFetchAndSetsLoading(t *testing.T) {
	c := newTestChatView(100, 20)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Folded: true}},
	})

	c.toggleEntryFold(0)

	tool := c.entries[0].Tool
	if tool.Folded {
		t.Fatal("entry should be unfolded after toggle")
	}
	if !tool.Loading {
		t.Error("expanding an unfetched entry should set Loading")
	}
	if !c.hasLoadingTool() {
		t.Error("hasLoadingTool should be true while a fetch is queued")
	}
	ids := c.TakePendingToolFetches()
	if len(ids) != 1 || ids[0] != "u1" {
		t.Errorf("pending fetches = %v, want [u1]", ids)
	}
	if got := c.TakePendingToolFetches(); got != nil {
		t.Errorf("second take should be empty, got %v", got)
	}
}

// An entry whose body is already loaded must not re-fetch on expand.
func TestToggleEntryFold_NoFetchWhenAlreadyLoaded(t *testing.T) {
	c := newTestChatView(100, 20)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "Read", FullResult: "already here", Status: ToolStatusComplete, Folded: true}},
	})

	c.toggleEntryFold(0)

	if c.entries[0].Tool.Loading {
		t.Error("an entry with a body already loaded should not enter Loading")
	}
	if ids := c.TakePendingToolFetches(); ids != nil {
		t.Errorf("no fetch expected for a loaded entry, got %v", ids)
	}
}

// Re-folding (collapsing) does not queue a fetch.
func TestToggleEntryFold_CollapseNoFetch(t *testing.T) {
	c := newTestChatView(100, 20)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolUseID: "u1", ToolName: "Read", Status: ToolStatusComplete, Folded: false}},
	})

	c.toggleEntryFold(0) // folds it

	if !c.entries[0].Tool.Folded {
		t.Fatal("entry should be folded after toggle from unfolded")
	}
	if c.entries[0].Tool.Loading {
		t.Error("collapsing should never set Loading")
	}
	if ids := c.TakePendingToolFetches(); ids != nil {
		t.Errorf("collapse should not queue a fetch, got %v", ids)
	}
}

func TestRenderToolEntry_LoadingShowsSpinner(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	e := ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Folded: false, Loading: true}
	s := stripAnsiCSI(renderToolEntry(e, 100, false, styles, md))
	if !strings.Contains(s, "loading…") {
		t.Errorf("expanded loading entry should show a loading line, got: %q", s)
	}
}

func TestRenderToolEntry_EmptyBodyShowsNoDetails(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	// Unfolded, not loading, and no full args/result recorded.
	e := ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Folded: false}
	s := stripAnsiCSI(renderToolEntry(e, 100, false, styles, md))
	if !strings.Contains(s, "(no details)") {
		t.Errorf("expanded entry with no body should say (no details), got: %q", s)
	}
}
