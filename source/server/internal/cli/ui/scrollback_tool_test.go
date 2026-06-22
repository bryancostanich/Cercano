package ui

import (
	"strings"
	"testing"
)

// stripAnsiCSI is defined in confirm_test.go (same package). Reused here.

func TestToolEntry_FoldedRender(t *testing.T) {
	e := ToolEntry{
		ToolName:      "read_file",
		ArgsSummary:   `path="main.go"`,
		Status:        ToolStatusComplete,
		ResultSummary: "32 lines",
		Folded:        true,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80, false))
	if !strings.Contains(s, "▸ read_file") {
		t.Errorf("expected fold marker + name, got: %q", s)
	}
	if !strings.Contains(s, "32 lines") {
		t.Errorf("expected result summary, got: %q", s)
	}
	if strings.Count(s, "\n") > 0 {
		t.Errorf("folded should be one line, got newlines in: %q", s)
	}
}

func TestToolEntry_ExpandedRender(t *testing.T) {
	e := ToolEntry{
		ToolName:    "read_file",
		ArgsSummary: `path="main.go"`,
		FullArgs:    `{"path":"main.go"}`,
		FullResult:  "package main\n\nimport ...",
		Status:      ToolStatusComplete,
		Folded:      false,
	}
	s := stripAnsiCSI(renderToolEntry(e, 80, false))
	if !strings.Contains(s, "▾ read_file") {
		t.Errorf("expected unfold marker, got: %q", s)
	}
	if !strings.Contains(s, `"path":"main.go"`) {
		t.Errorf("expanded should show full args, got: %q", s)
	}
}

func TestToolEntry_InProgress(t *testing.T) {
	e := ToolEntry{ToolName: "grep", Status: ToolStatusInProgress, Folded: true}
	s := stripAnsiCSI(renderToolEntry(e, 80, false))
	if !strings.Contains(s, "grep") {
		t.Errorf("name missing: %q", s)
	}
}

func TestToolEntry_FocusedRender(t *testing.T) {
	e := ToolEntry{ToolName: "list_dir", Status: ToolStatusComplete, Folded: true}
	s := stripAnsiCSI(renderToolEntry(e, 80, true))
	if !strings.Contains(s, "▶") {
		t.Errorf("focused render should have a ▶ focus indicator, got: %q", s)
	}
}

func TestToolEntry_UnfocusedRenderDiffers(t *testing.T) {
	e := ToolEntry{ToolName: "list_dir", Status: ToolStatusComplete, Folded: true}
	sFocused := stripAnsiCSI(renderToolEntry(e, 80, true))
	sUnfocused := stripAnsiCSI(renderToolEntry(e, 80, false))
	if sFocused == sUnfocused {
		t.Errorf("focused and unfocused should differ visually; both = %q", sFocused)
	}
}
