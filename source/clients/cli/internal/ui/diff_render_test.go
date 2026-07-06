package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestRenderToolArgsDiff_EditShowsPlusMinusWithContext(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"a.go","old_string":"foo\nbar","new_string":"foo\nBAZ"}`
	lines := renderToolArgsDiff("Edit", args, 100, styles)
	if lines == nil {
		t.Fatal("Edit args should produce a diff")
	}
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "a.go") {
		t.Errorf("diff should show the path, got:\n%s", joined)
	}
	if !strings.Contains(joined, "- bar") || !strings.Contains(joined, "+ BAZ") {
		t.Errorf("diff should show -bar / +BAZ, got:\n%s", joined)
	}
	if !strings.Contains(joined, "  foo") {
		t.Errorf("the shared line should stay as context, got:\n%s", joined)
	}
}

func TestRenderToolArgsDiff_WriteIsAllAdded(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"new.go","content":"package main\n\nfunc main() {}"}`
	lines := renderToolArgsDiff("Write", args, 100, styles)
	if lines == nil {
		t.Fatal("Write args should produce a diff")
	}
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "+ package main") {
		t.Errorf("write content should render as additions, got:\n%s", joined)
	}
	if strings.Contains(joined, "- ") {
		t.Errorf("a fresh write should have no deletions, got:\n%s", joined)
	}
}

func TestRenderToolArgsDiff_NonEditAndMalformedReturnNil(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	if renderToolArgsDiff("Bash", `{"command":"ls"}`, 100, styles) != nil {
		t.Error("non-edit/write tools should not produce a diff")
	}
	if renderToolArgsDiff("Edit", `not json`, 100, styles) != nil {
		t.Error("malformed edit args should return nil (fall back to raw args)")
	}
}
