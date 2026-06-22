package ui

import (
	"regexp"
	"strings"
	"testing"

	"cercano/source/server/internal/cli/render"
	"cercano/source/server/internal/cli/theme"
)

// ansiRE strips SGR escape sequences so assertions can match the visible text.
// Glamour styles each word/token separately, inserting escapes between words.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// Renders an assistant entry the way renderEntry does for committed blocks,
// proving prose is Glamour-formatted and tables go through render.Table.
func TestAssistantMarkdown_FormatsProseAndTable(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{
		Role:    RoleAssistant,
		Content: "# Header\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\ntrailing prose\n",
	}
	out := m.renderAssistantMarkdown(e, 60)
	vis := plain(out)

	// Prose went through Glamour: ANSI styling applied and the heading line is
	// reflowed/padded to width (raw text would not be).
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected Glamour ANSI styling on prose: %q", out)
	}
	if !strings.Contains(vis, "Header") {
		t.Fatalf("heading text missing: %q", vis)
	}
	// Table routed through render.Table → box-drawing border present.
	if !strings.Contains(out, "│") {
		t.Fatalf("expected table border from render.Table: %q", vis)
	}
	if !strings.Contains(vis, "trailing prose") {
		t.Fatalf("trailing prose missing: %q", vis)
	}
}

func TestAssistantMarkdown_HeadingsDropHashMarker(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{Role: RoleAssistant, Content: "# Title\n\n## Subtitle\n\nbody\n"}
	vis := plain(m.renderAssistantMarkdown(e, 60))
	if strings.Contains(vis, "#") {
		t.Fatalf("expected no '#' markers in rendered headings: %q", vis)
	}
	if !strings.Contains(vis, "Title") || !strings.Contains(vis, "Subtitle") {
		t.Fatalf("heading text missing: %q", vis)
	}
}

func TestAssistantMarkdown_CodeBlockHasLabeledRules(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n```go\nx := 1\n```\n\nouttro\n"}
	vis := plain(m.renderAssistantMarkdown(e, 40))

	// Language label present on the opening rule.
	if !strings.Contains(vis, "─── go ") {
		t.Fatalf("expected labeled opening rule, got: %q", vis)
	}
	// Two rule lines (open + close), each spanning the width.
	if strings.Count(vis, "────────") < 2 {
		t.Fatalf("expected opening and closing rules, got: %q", vis)
	}
	if !strings.Contains(vis, "x := 1") {
		t.Fatalf("code body missing: %q", vis)
	}
}

func TestAssistantMarkdown_OpenFenceTailRenders(t *testing.T) {
	m := &Model{
		styles: theme.NewStyles(theme.Cracker()),
		md:     render.NewMarkdown(theme.CrackerMarkdownStyle()),
	}
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n```go\nx := 1"}
	out := plain(m.renderAssistantMarkdown(e, 60))
	if !strings.Contains(out, "x := 1") {
		t.Fatalf("open-fence tail code missing: %q", out)
	}
}
