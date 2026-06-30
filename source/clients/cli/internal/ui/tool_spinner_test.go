package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// hasInProgressTool drives the spinner animation tick loop. False when no
// entries; true when any entry has InProgress status; false again once they
// all complete.
func TestChatView_HasInProgressTool(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	if c.hasInProgressTool() {
		t.Errorf("empty scrollback must report no in-progress tools")
	}

	c.SetEntriesSlice([]*Entry{
		{Role: RoleUser, Content: "hi"},
		{Tool: &ToolEntry{ToolName: "Read", Status: ToolStatusComplete}},
	})
	if c.hasInProgressTool() {
		t.Errorf("only-completed tools must report no in-progress")
	}

	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{ToolName: "Read", Status: ToolStatusComplete}},
		{Tool: &ToolEntry{ToolName: "Bash", Status: ToolStatusInProgress}},
	})
	if !c.hasInProgressTool() {
		t.Errorf("any InProgress entry should report true")
	}
}

// In-progress tool entries render a braille spinner glyph instead of the
// static ellipsis. The exact frame depends on wall clock, so we accept ANY
// of the 10 spinner glyphs.
func TestRenderToolEntry_InProgressUsesAnimatedSpinner(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	e := ToolEntry{
		ToolName:    "Read",
		ArgsSummary: "a.go",
		Status:      ToolStatusInProgress,
		Folded:      true,
	}
	got := stripAnsiCSI(renderToolEntry(e, 80, false, styles, md))
	if strings.Contains(got, "…") {
		t.Errorf("in-progress entry must not use static '…', got: %q", got)
	}
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	hit := false
	for _, frame := range spinnerFrames {
		if strings.Contains(got, frame) {
			hit = true
			break
		}
	}
	if !hit {
		t.Errorf("expected a braille spinner glyph in in-progress entry, got: %q", got)
	}
}

// Elapsed time appears only once the tool has been running >= 1s. A fresh
// in-progress entry (zero StartedAt offset) shows the spinner alone.
func TestRenderToolEntry_InProgressShowsElapsedAfterOneSecond(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	fresh := ToolEntry{
		ToolName: "Bash", ArgsSummary: "go test", Status: ToolStatusInProgress,
		StartedAt: time.Now(), Folded: true,
	}
	freshOut := stripAnsiCSI(renderToolEntry(fresh, 80, false, styles, md))
	if strings.Contains(freshOut, " · ") {
		t.Errorf("fresh in-progress should not show elapsed yet, got: %q", freshOut)
	}

	long := ToolEntry{
		ToolName: "Bash", ArgsSummary: "go test", Status: ToolStatusInProgress,
		StartedAt: time.Now().Add(-3 * time.Second), Folded: true,
	}
	longOut := stripAnsiCSI(renderToolEntry(long, 80, false, styles, md))
	if !strings.Contains(longOut, "3s") {
		t.Errorf("long-running entry should show elapsed '3s', got: %q", longOut)
	}
}

// The spinner emitter is wall-clock-driven; consecutive calls within a
// frame window return the same glyph; calls across frame boundaries differ.
// We test the simpler property: any call produces SOMETHING from the frame
// set, in faint styling (ANSI escape present).
func TestAnimateToolSpinner_ProducesFaintBrailleGlyph(t *testing.T) {
	got := animateToolSpinner()
	if got == "" {
		t.Fatal("animateToolSpinner returned empty string")
	}
	// Stripped string should be exactly one of the spinner glyphs.
	stripped := stripAnsiCSI(got)
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	found := false
	for _, f := range spinnerFrames {
		if stripped == f {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("stripped spinner output %q not in expected frame set", stripped)
	}
	// Raw string must contain at least one ANSI escape (faint styling).
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI styling on spinner glyph, got plain: %q", got)
	}
}
