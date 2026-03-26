package document

import (
	"fmt"
	"strings"
)

// Style controls the verbosity of generated doc comments.
type Style string

const (
	// StyleMinimal represents a minimal UI style configuration for the agent interface.
	StyleMinimal Style = "minimal"
	// StyleDetailed specifies the detailed style for code formatting and documentation generation.
	StyleDetailed Style = "detailed"
)

// BuildPrompt creates the prompt for the local model to generate a doc comment
// for the given symbol.
func BuildPrompt(sym Symbol, style Style) string {
	styleInstr := "Be concise — one or two sentences."
	if style == StyleDetailed {
		styleInstr = "Write a thorough description. Include parameter descriptions and return value documentation where applicable."
	}

	return fmt.Sprintf(`You are a Go documentation writer. Write a GoDoc comment for the following symbol.
Rules:
- Start with the symbol name (e.g. "%s does...")
- %s
- Do not repeat the function signature
- Do not add code examples
- Return ONLY the comment text, without // or /* */ markers

Symbol:
%s`, sym.Name, styleInstr, sym.Body)
}

// FormatAsGoDoc takes raw comment text from the model and formats it as a
// valid Go doc comment block (// prefixed lines).
func FormatAsGoDoc(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Strip any // or /* */ markers the model might have added
	text = stripCommentMarkers(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			lines = append(lines, "//")
		} else {
			lines = append(lines, "// "+line)
		}
	}
	return strings.Join(lines, "\n")
}

// stripCommentMarkers removes Go comment markers that the model may include.
func stripCommentMarkers(text string) string {
	// Remove /* */ block comment markers
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")

	// Remove // prefix from each line
	lines := strings.Split(text, "\n")
	stripped := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// ") {
			lines[i] = strings.TrimPrefix(trimmed, "// ")
			stripped = true
		} else if strings.HasPrefix(trimmed, "//") {
			lines[i] = strings.TrimPrefix(trimmed, "//")
			stripped = true
		}
	}
	if stripped {
		return strings.Join(lines, "\n")
	}
	return text
}
