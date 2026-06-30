package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"cercano/source/clients/cli/internal/theme"
)

// renderDivider centers the label between two stretches of `─` filling the
// available width, with a single space on each side of the label.
func TestRenderDivider_FillsAvailableWidthWithCenteredLabel(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	got := stripAnsiCSI(renderDivider("hello", 40, styles))
	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("expected width 40, got %d (line: %q)", w, got)
	}
	if !strings.Contains(got, " hello ") {
		t.Errorf("expected label surrounded by spaces, got: %q", got)
	}
	// Should start and end with `─`.
	if !strings.HasPrefix(got, "─") || !strings.HasSuffix(got, "─") {
		t.Errorf("expected `─` at both ends, got: %q", got)
	}
}

// When the label is wider than the available width, the renderer falls back
// to plain muted label with no rule (rather than producing a wrapped mess).
func TestRenderDivider_OverflowFallsBackToBareLabel(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	label := strings.Repeat("x", 40)
	got := stripAnsiCSI(renderDivider(label, 20, styles))
	if strings.Contains(got, "─") {
		t.Errorf("overflow should fall back to bare label, got: %q", got)
	}
	if !strings.Contains(got, label) {
		t.Errorf("expected the full label in output, got: %q", got)
	}
}

// Through SetEntries: an Entry with RoleDivider renders the centered-rule
// treatment (not the muted text RoleSystem produces).
func TestChatView_DividerEntryRendersAsRule(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 80, 20)
	c.SetEntries([]*Entry{
		{Role: RoleUser, Content: "first"},
		{Role: RoleDivider, Content: "⟲ resumed 47 turns"},
		{Role: RoleAssistant, Content: "back"},
	})
	got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
	if !strings.Contains(got, "⟲ resumed 47 turns") {
		t.Errorf("expected divider label visible, got:\n%s", got)
	}
	if !strings.Contains(got, "──") {
		t.Errorf("expected divider rule (`─`) in output, got:\n%s", got)
	}
}
