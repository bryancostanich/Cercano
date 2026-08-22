package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestChatView_ProgressAfterToolStartIsDurableSystemLine(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 80, 20)
	c.Apply(toolEntryStartMsg{id: "tool-1", name: "Bash"})
	c.Apply(chatProgressMsg{note: "running Bash"})

	if len(c.entries) != 2 {
		t.Fatalf("entries=%d, want tool row + progress line", len(c.entries))
	}
	if c.entries[0].Tool == nil || c.entries[0].Tool.ToolName != "Bash" {
		t.Fatalf("first entry should be Bash tool row, got %+v", c.entries[0])
	}
	if c.entries[1].Role != RoleSystem || !strings.Contains(c.entries[1].Content, "running Bash") {
		t.Fatalf("second entry should be durable progress, got %+v", c.entries[1])
	}
}
