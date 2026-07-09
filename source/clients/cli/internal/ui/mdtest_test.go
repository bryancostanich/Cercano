package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestSeedAssistantMarkdown_SeedsRenderedEntry(t *testing.T) {
	p := theme.Cracker()
	m := Model{
		styles: theme.NewStyles(p),
	}
	m.setMainChat(newChatView(theme.NewStyles(p), p, "", "", 0, 0))
	m = m.SeedAssistantMarkdown("# Hi\n\n**bold**\n")

	entries := m.mainChat().Entries()
	if len(entries) != 1 || entries[0].Role != RoleAssistant {
		t.Fatalf("expected one assistant entry, got %#v", entries)
	}
	if entries[0].Streaming {
		t.Fatalf("seeded entry should be a finished (non-streaming) reply")
	}
	if m.splashShown {
		t.Fatalf("splash should be hidden in mdtest mode")
	}
	vis := plain(m.mainChat().renderAssistantMarkdown(entries[0], 60))
	if !strings.Contains(vis, "Hi") || !strings.Contains(vis, "bold") {
		t.Fatalf("rendered content missing: %q", vis)
	}
}
