package ui

import (
	"strings"
	"testing"

	"cercano/source/server/internal/cli/render"
	"cercano/source/server/internal/cli/theme"
)

func TestSeedAssistantMarkdown_SeedsRenderedEntry(t *testing.T) {
	m := Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	m = m.SeedAssistantMarkdown("# Hi\n\n**bold**\n")

	if len(m.entries) != 1 || m.entries[0].Role != RoleAssistant {
		t.Fatalf("expected one assistant entry, got %#v", m.entries)
	}
	if m.entries[0].Streaming {
		t.Fatalf("seeded entry should be a finished (non-streaming) reply")
	}
	if m.splashShown {
		t.Fatalf("splash should be hidden in mdtest mode")
	}
	vis := plain(m.renderAssistantMarkdown(m.entries[0], 60))
	if !strings.Contains(vis, "Hi") || !strings.Contains(vis, "bold") {
		t.Fatalf("rendered content missing: %q", vis)
	}
}
