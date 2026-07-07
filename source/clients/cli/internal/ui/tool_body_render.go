package ui

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
)

// tool_body_render.go renders a tool result/args body for both the /c viewer and
// the main-chat tool expand. The render mode is decided by an authoritative
// content type when the agent provides one (the future (c) path), and otherwise
// by sniffing the body — JSON is pretty-printed in a fenced block, prose with
// markdown markers is rendered as markdown, and everything else stays verbatim
// (so code, logs, and raw output are never mangled).

// resolveToolBodyKind maps a declared content type (mime-ish or short form) to a
// render kind, falling back to sniffing the body when the type is unknown/empty.
// declaredType is the seam where the real-content-type (c) feature plugs in.
func resolveToolBodyKind(body, declaredType string) string {
	switch strings.ToLower(strings.TrimSpace(declaredType)) {
	case "json", "application/json":
		return "json"
	case "markdown", "md", "text/markdown":
		return "markdown"
	case "plain", "text", "text/plain":
		return "plain"
	}
	return classifyToolBody(body)
}

// classifyToolBody sniffs a body into "json" | "markdown" | "plain".
func classifyToolBody(body string) string {
	s := strings.TrimSpace(body)
	if s == "" {
		return "plain"
	}
	if (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s)) {
		return "json"
	}
	if hasMarkdownMarkers(s) {
		return "markdown"
	}
	return "plain"
}

var (
	mdHeading    = regexp.MustCompile(`(?m)^#{1,6}\s`)
	mdFence      = regexp.MustCompile("(?m)^```")
	mdBlockquote = regexp.MustCompile(`(?m)^>\s`)
	mdTableSep   = regexp.MustCompile(`(?m)^\s*\|?[\s:|-]*-[\s:|-]*\|[\s:|-]*$`)
	mdListItem   = regexp.MustCompile(`(?m)^\s*([-*+]\s|\d+\.\s)`)
)

// hasMarkdownMarkers reports whether s contains block-level markdown structure.
// Block-level only (headings, fences, blockquotes, tables, ≥2 list items) — inline
// emphasis/backticks/links are deliberately NOT enough, since they false-positive
// on code and logs (a `*` glob, a back-quoted shell token), which we'd rather keep
// verbatim than mangle into italics.
func hasMarkdownMarkers(s string) bool {
	if mdHeading.MatchString(s) || mdFence.MatchString(s) ||
		mdBlockquote.MatchString(s) || mdTableSep.MatchString(s) {
		return true
	}
	return len(mdListItem.FindAllString(s, 2)) >= 2
}

// prettyJSON re-indents a JSON body; returns the input unchanged if it is not
// valid JSON.
func prettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(strings.TrimSpace(s)), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// expandTabs replaces tab characters with 4 spaces for display. A terminal
// renders \t as up to 8 columns while lipgloss.Width counts 1, so any raw tab
// that survives into a rendered line silently overflows the width budget and
// wraps at column 0 — left of the collapse rail.
func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// renderToolBody renders a tool body to display lines at the given width.
// declaredType, when set, picks the render mode; otherwise the body is sniffed.
// JSON → pretty-printed in a ```json fence; markdown → rendered; else verbatim.
func renderToolBody(body, declaredType string, md *render.Markdown, width int) []string {
	if width < 8 {
		width = 8
	}
	body = expandTabs(body)
	kind := resolveToolBodyKind(body, declaredType)
	if md == nil { // no engine → never mangle
		kind = "plain"
	}
	switch kind {
	case "json":
		src := "```json\n" + prettyJSON(strings.TrimSpace(body)) + "\n```"
		return strings.Split(md.Render(src, width), "\n")
	case "markdown":
		return strings.Split(md.Render(strings.TrimSpace(body), width), "\n")
	default:
		return strings.Split(ansi.Wrap(body, width, ""), "\n")
	}
}

// renderToolResultBody renders a tool's result body, syntax-highlighting file
// contents as a fenced code block when the tool read a recognized code file
// (language inferred from the path in its args). Falls back to renderToolBody
// (JSON/markdown/plain sniffing) for everything else.
func renderToolResultBody(toolName, argsJSON, result string, md *render.Markdown, width int) []string {
	if md != nil {
		if lang := codeLangForToolArgs(toolName, argsJSON); lang != "" {
			fenced := "```" + lang + "\n" + expandTabs(result) + "\n```"
			return strings.Split(md.Render(fenced, width), "\n")
		}
	}
	return renderToolBody(result, "", md, width)
}

// codeLangForToolArgs returns a Chroma/Glamour language for a tool that read a
// code file — inferred from the path extension in its args — or "" when the
// tool isn't a file read or the extension isn't a recognized code type.
func codeLangForToolArgs(toolName, argsJSON string) string {
	switch strings.ToLower(toolName) {
	case "read", "read_file":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) == nil {
			if l := langFromExt(a.Path); l != "" {
				return l
			}
		}
	}
	// Fallback: a code-extension filename anywhere in the args (e.g. a bash
	// heredoc `cat > source.go`, or a redirect) picks the highlight language for
	// the whole result body. First recognized extension wins.
	if m := codeFileRe.FindString(argsJSON); m != "" {
		return langFromExt(m)
	}
	return ""
}

// codeFileRe matches a filename bearing a recognized code extension, used to
// infer a highlight language from tool args that reference a file.
var codeFileRe = regexp.MustCompile(`[\w./-]+\.(?:go|py|ts|tsx|js|jsx|mjs|rs|c|h|cpp|cc|cxx|hpp|java|rb|sh|bash|zsh|json|yaml|yml|toml|md|markdown|sql|proto|html|htm|css|rkt)\b`)

// langFromExt maps a file extension to a Chroma language name, or "" for
// unknown / non-code extensions (which then render as plain text).
func langFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md", ".markdown":
		return "markdown"
	case ".sql":
		return "sql"
	case ".proto":
		return "protobuf"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".rkt":
		return "scheme"
	}
	return ""
}
