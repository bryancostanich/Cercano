package ui

import (
	"regexp"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

// ansiRE strips SGR escape sequences so assertions can match the visible text.
// Glamour styles each word/token separately, inserting escapes between words.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// newMdChatView returns a chatView sized to zero (only used for md rendering).
func newMdChatView() *chatView {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 0, 0)
	return &cv
}

// Renders an assistant entry the way renderEntry does for committed blocks,
// proving prose is Glamour-formatted and tables go through render.Table.
func TestAssistantMarkdown_FormatsProseAndTable(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{
		Role:    RoleAssistant,
		Content: "# Header\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\ntrailing prose\n",
	}
	out := cv.renderAssistantMarkdown(e, 60)
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
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "# Title\n\n## Subtitle\n\nbody\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 60))
	if strings.Contains(vis, "#") {
		t.Fatalf("expected no '#' markers in rendered headings: %q", vis)
	}
	if !strings.Contains(vis, "Title") || !strings.Contains(vis, "Subtitle") {
		t.Fatalf("heading text missing: %q", vis)
	}
}

func TestAssistantMarkdown_BlankLineBeforeHeadings(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "intro paragraph\n\n## Section\n\nbody\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 60))

	// A blank line should separate the intro paragraph from the heading.
	if !strings.Contains(vis, "intro paragraph") {
		t.Fatalf("intro missing: %q", vis)
	}
	idx := strings.Index(vis, "Section")
	if idx < 0 {
		t.Fatalf("heading missing: %q", vis)
	}
	// The two lines immediately before the heading line should include a blank one.
	before := vis[:idx]
	if !strings.Contains(before, "\n\n") {
		t.Fatalf("expected a blank line before the heading: %q", vis)
	}
}

func TestAssistantMarkdown_FirstHeadingHasNoLeadingBlank(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "# Title\n\nbody\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 60))
	if strings.HasPrefix(vis, "\n") {
		t.Fatalf("first heading should not start with a blank line: %q", vis)
	}
}

func TestAssistantMarkdown_BlankLineAfterHeadings(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "# Title\n\nbody after title\n\n## Section\n\nbody after section\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 60))

	// Each heading must be separated from the paragraph it introduces by a
	// blank line — SplitBlocks makes the heading its own block, so without an
	// explicit trailing blank the body would sit on the very next line.
	for _, tc := range []struct{ heading, body string }{
		{"Title", "body after title"},
		{"Section", "body after section"},
	} {
		hi := strings.Index(vis, tc.heading)
		bi := strings.Index(vis, tc.body)
		if hi < 0 || bi < 0 || bi <= hi {
			t.Fatalf("heading %q / body %q not both present in order: %q", tc.heading, tc.body, vis)
		}
		// The span between the end of the heading text and the start of the
		// body must contain a blank line (two consecutive newlines).
		between := vis[hi:bi]
		if !strings.Contains(between, "\n\n") {
			t.Fatalf("expected a blank line after heading %q before %q: %q", tc.heading, tc.body, vis)
		}
	}
}

func TestAssistantMarkdown_FinalHeadingHasNoTrailingBlank(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n## Trailing Heading\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 60))
	// A heading as the last committed block must not leave the reply ending on
	// a blank line — the trailing breathing-room newline is trimmed when no
	// body (or streaming tail) follows.
	if strings.HasSuffix(vis, "\n\n") || strings.HasSuffix(vis, "\n") && strings.TrimSpace(lastLine(vis)) == "" {
		t.Fatalf("final heading should not end on a blank line: %q", vis)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestAssistantMarkdown_CodeBlockHasLabeledRules(t *testing.T) {
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n```go\nx := 1\n```\n\nouttro\n"}
	vis := plain(cv.renderAssistantMarkdown(e, 40))

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
	cv := newMdChatView()
	e := &Entry{Role: RoleAssistant, Content: "intro\n\n```go\nx := 1"}
	out := plain(cv.renderAssistantMarkdown(e, 60))
	if !strings.Contains(out, "x := 1") {
		t.Fatalf("open-fence tail code missing: %q", out)
	}
}
