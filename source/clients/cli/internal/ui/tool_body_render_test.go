package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

func TestClassifyToolBody(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"json object", `{"a":1,"b":[2,3]}`, "json"},
		{"json array", `[1,2,3]`, "json"},
		{"json with whitespace", "  \n{\"k\":\"v\"}\n", "json"},
		{"brace but invalid json", `{not really json`, "plain"},
		{"markdown heading", "# Title\n\nsome prose here", "markdown"},
		{"markdown fence", "intro\n```go\nfunc main(){}\n```", "markdown"},
		{"markdown table", "| a | b |\n|---|---|\n| 1 | 2 |", "markdown"},
		{"markdown list", "- one\n- two\n- three", "markdown"},
		{"plain prose", "The command completed successfully.", "plain"},
		{"raw log output", "starting\nfile1.txt\nfile2.txt\ndone", "plain"},
		{"single dash not a list", "result - ok", "plain"},
		{"code with star not emphasis", "x = a * b\ny = c * d", "plain"},
		{"empty", "   ", "plain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyToolBody(c.body); got != c.want {
				t.Errorf("classifyToolBody(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestResolveToolBodyKind_DeclaredTypeWins(t *testing.T) {
	// A declared type overrides the sniff (the (c) seam): a body that sniffs as
	// plain is still rendered as markdown when the agent says so.
	if got := resolveToolBodyKind("just plain text", "text/markdown"); got != "markdown" {
		t.Errorf("declared markdown should win, got %q", got)
	}
	if got := resolveToolBodyKind(`{"a":1}`, "text/plain"); got != "plain" {
		t.Errorf("declared plain should win over JSON-looking body, got %q", got)
	}
	// Unknown/empty declared type falls back to sniffing.
	if got := resolveToolBodyKind(`{"a":1}`, ""); got != "json" {
		t.Errorf("empty declared type should sniff JSON, got %q", got)
	}
}

func TestPrettyJSON(t *testing.T) {
	got := prettyJSON(`{"a":1,"b":2}`)
	if !strings.Contains(got, "\n") || !strings.Contains(got, "  \"a\": 1") {
		t.Errorf("prettyJSON should indent, got %q", got)
	}
	if prettyJSON("not json") != "not json" {
		t.Error("prettyJSON should pass through invalid JSON unchanged")
	}
}

func TestRenderToolBody(t *testing.T) {
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	// JSON → fenced + indented (rendered text contains the indented key).
	jsonLines := strings.Join(renderToolBody(`{"a":1,"b":2}`, "", md, 60), "\n")
	if !strings.Contains(plain(jsonLines), `"a": 1`) {
		t.Errorf("JSON body should render indented, got:\n%s", plain(jsonLines))
	}
	// Plain stays verbatim (no markdown structure introduced).
	plainLines := renderToolBody("line one\nline two", "", md, 60)
	if len(plainLines) < 2 {
		t.Errorf("plain body should keep its lines, got %d", len(plainLines))
	}
	// nil engine never mangles → plain.
	nilLines := renderToolBody("# heading", "markdown", nil, 60)
	if !strings.Contains(strings.Join(nilLines, "\n"), "# heading") {
		t.Errorf("nil md engine should fall back to verbatim, got %v", nilLines)
	}
}
