package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// The /c viewer renders tool turns through the same renderers as the main
// chat: Edit args expand to a +/- diff, and a tool_result correlates back to
// its originating tool_use for syntax highlighting.
func TestContextView_ToolTurnsUseSharedRenderers(t *testing.T) {
	p := theme.Cracker()
	cv := &contextView{
		palette:  p,
		styles:   theme.NewStyles(p),
		width:    100,
		expanded: map[string]bool{},
		md:       render.NewMarkdown(theme.MarkdownStyle(p)),
	}
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "1", Role: "assistant", Kind: "tool_use", ToolName: "Edit", ToolUseID: "e1",
			ToolArgs: `{"path":"a.go","old_string":"old line","new_string":"new line"}`,
			Body:     `Edit {"path":"a.go","old_string":"old line","new_string":"new line"}`},
		{ID: "2", Role: "user", Kind: "tool_result", ToolUseRef: "e1", Body: "ok"},
		{ID: "3", Role: "assistant", Kind: "tool_use", ToolName: "Read", ToolUseID: "r1",
			ToolArgs: `{"path":"main.go"}`, Body: `Read {"path":"main.go"}`},
		{ID: "4", Role: "user", Kind: "tool_result", ToolUseRef: "r1",
			Body: "package main\n\nfunc main() {}\n"},
	}

	// Edit tool_use expands to diff lines, not raw args JSON.
	got := strings.Join(cv.expandedBodyLines(cv.snapshot.Turns[0]), "\n")
	if !strings.Contains(got, "new line") || !strings.Contains(got, "old line") {
		t.Errorf("edit expand should show diff lines, got:\n%s", got)
	}
	if strings.Contains(got, "old_string") {
		t.Errorf("edit expand should not show raw args JSON, got:\n%s", got)
	}

	// A tool_result correlates to its Read tool_use → syntax-highlighted body.
	res := strings.Join(cv.expandedBodyLines(cv.snapshot.Turns[3]), "\n")
	if !strings.Contains(res, "main") {
		t.Errorf("result expand should contain the code, got:\n%s", res)
	}
	if !strings.Contains(res, "\x1b[") {
		t.Errorf("result expand should be syntax-highlighted (ANSI), got:\n%s", res)
	}

	// Unknown ref falls back gracefully rather than resolving.
	if _, _, ok := cv.toolUseFor("nope"); ok {
		t.Error("unknown ref should not resolve")
	}
}
