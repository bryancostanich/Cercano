package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

func TestLangFromExt(t *testing.T) {
	cases := map[string]string{
		"main.go":   "go",
		"app.py":    "python",
		"x.ts":      "typescript",
		"y.js":      "javascript",
		"z.rs":      "rust",
		"cell.rkt":  "scheme",
		"data.json": "json",
		"readme.md": "markdown",
		"notes.txt": "",
		"noext":     "",
	}
	for path, want := range cases {
		if got := langFromExt(path); got != want {
			t.Errorf("langFromExt(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCodeLangForToolArgs(t *testing.T) {
	if got := codeLangForToolArgs("Read", `{"path":"main.go"}`); got != "go" {
		t.Errorf("Read main.go = %q, want go", got)
	}
	if got := codeLangForToolArgs("Read", `{"path":"notes.txt"}`); got != "" {
		t.Errorf("Read notes.txt = %q, want empty", got)
	}
	if got := codeLangForToolArgs("Bash", `{"command":"ls"}`); got != "" {
		t.Errorf("Bash = %q, want empty (not a file read)", got)
	}
	if got := codeLangForToolArgs("Read", `not json`); got != "" {
		t.Errorf("malformed args = %q, want empty", got)
	}
}

func TestCodeLangForToolArgs_FilenameFallback(t *testing.T) {
	// A bash heredoc writing a Go file → go, via the filename fallback.
	if got := codeLangForToolArgs("Bash", `{"cmd":["bash","-lc","cd x && cat > source.go <<EOF"]}`); got != "go" {
		t.Errorf("bash heredoc to source.go = %q, want go", got)
	}
	// A python heredoc.
	if got := codeLangForToolArgs("Bash", `{"cmd":"cat > app.py <<EOF"}`); got != "python" {
		t.Errorf("cat app.py = %q, want python", got)
	}
	// A bash command with no code filename → no highlighting.
	if got := codeLangForToolArgs("Bash", `{"cmd":["bash","-lc","go test ./internal/x"]}`); got != "" {
		t.Errorf("bash go test = %q, want empty", got)
	}
	// A read of a non-code file still yields nothing (path case falls through).
	if got := codeLangForToolArgs("Read", `{"path":"notes.txt"}`); got != "" {
		t.Errorf("read notes.txt = %q, want empty", got)
	}
}

func TestRenderToolResultBody_ReadRendersCodeContent(t *testing.T) {
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	lines := renderToolResultBody("Read", `{"path":"main.go"}`, "package main\n\nfunc main() {}", md, 80)
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "package main") || !strings.Contains(joined, "func main") {
		t.Errorf("read code content should appear in the highlighted body, got:\n%s", joined)
	}
}

// A non-code read (or non-read tool) still renders via the plain/JSON path.
func TestRenderToolResultBody_NonCodeFallsBack(t *testing.T) {
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	lines := renderToolResultBody("Bash", `{"command":"echo hi"}`, "hi\nthere", md, 80)
	joined := stripAnsiCSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "hi") || !strings.Contains(joined, "there") {
		t.Errorf("plain result should render verbatim, got:\n%s", joined)
	}
}
