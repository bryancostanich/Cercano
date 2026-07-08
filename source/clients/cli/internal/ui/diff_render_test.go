package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestRenderToolArgsDiff_EditShowsPlusMinusWithContext(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"a.go","old_string":"foo\nbar","new_string":"foo\nBAZ"}`
	lines := renderToolArgsDiff("Edit", args, 0, 100, styles)
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
	lines := renderToolArgsDiff("Write", args, 1, 100, styles)
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
	if renderToolArgsDiff("Bash", `{"command":"ls"}`, 0, 100, styles) != nil {
		t.Error("non-edit/write tools should not produce a diff")
	}
	if renderToolArgsDiff("Edit", `not json`, 0, 100, styles) != nil {
		t.Error("malformed edit args should return nil (fall back to raw args)")
	}
}

func TestRenderToolArgsDiff_StartLineNumbersGutter(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"a.go","old_string":"foo\nbar","new_string":"foo\nBAZ"}`
	lines := renderToolArgsDiff("Edit", args, 41, 100, styles)
	if lines == nil {
		t.Fatal("Edit args should produce a diff")
	}
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	// Context carries the new-file number, the delete the old-file number,
	// the insert the new-file number — all seeded at startLine 41.
	if !strings.Contains(joined, "41   foo") {
		t.Errorf("context line should carry line number 41, got:\n%s", joined)
	}
	if !strings.Contains(joined, "42 - bar") {
		t.Errorf("deleted line should carry old-file number 42, got:\n%s", joined)
	}
	if !strings.Contains(joined, "42 + BAZ") {
		t.Errorf("inserted line should carry new-file number 42, got:\n%s", joined)
	}
}

func TestRenderToolArgsDiff_WriteNumbersFromOne(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"new.go","content":"package main\n\nfunc main() {}"}`
	lines := renderToolArgsDiff("Write", args, 1, 100, styles)
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "1 + package main") {
		t.Errorf("first written line should be numbered 1, got:\n%s", joined)
	}
	if !strings.Contains(joined, "3 + func main() {}") {
		t.Errorf("third written line should be numbered 3, got:\n%s", joined)
	}
}

func TestRenderToolArgsDiff_ZeroStartLineIsUnnumbered(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	args := `{"path":"a.go","old_string":"foo\nbar","new_string":"foo\nBAZ"}`
	lines := renderToolArgsDiff("Edit", args, 0, 100, styles)
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	// No gutter: the +/- prefix sits directly after the 4-space indent.
	if !strings.Contains(joined, "    - bar") || !strings.Contains(joined, "    + BAZ") {
		t.Errorf("startLine 0 should render the unnumbered layout, got:\n%s", joined)
	}
}
