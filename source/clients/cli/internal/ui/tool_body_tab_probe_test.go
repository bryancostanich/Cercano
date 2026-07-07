package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// PROBE: does the expanded body emit raw tab characters? lipgloss.Width
// counts \t as 1 col but the terminal renders up to 8, so any raw tab makes a
// line overflow and wrap at column 0 despite passing width assertions.
func TestProbe_ExpandedBodyHasNoRawTabs(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	md := render.NewMarkdown(theme.MarkdownStyle(pal))

	// grep-style output: tab-indented Go code, like the user's screenshot.
	result := "58:\t\t{\"chat+groq backend\", ProfileRef{}},\n59:\t\t{\"chat backendless deepinfra\", \"https://api.deepinfra.com/v1/openai\"},\n"

	for _, tc := range []struct {
		name string
		args string
	}{
		{"highlighted (go file in args)", `{"cmd":["grep","-n","backend","catalog.go"]}`},
		{"plain (no code file)", `{"cmd":["grep","-n","backend","somefile"]}`},
	} {
		e := ToolEntry{
			ToolName:   "Bash",
			Status:     ToolStatusComplete,
			Folded:     false,
			FullArgs:   tc.args,
			FullResult: result,
		}
		out := renderToolEntry(e, 100, false, styles, md)
		if strings.Contains(out, "\t") {
			for i, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "\t") {
					t.Errorf("%s: line %d contains raw tab: %q", tc.name, i, l)
				}
			}
		}
	}
}
