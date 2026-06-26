package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// When a tool entry is expanded, its FullResult renders by type through the same
// path /c uses: JSON pretty-printed, markdown rendered, raw verbatim.
func TestRenderToolEntry_ExpandedResultRendersByType(t *testing.T) {
	md := render.NewMarkdown(theme.CrackerMarkdownStyle())

	jsonResult := plain(renderToolEntry(ToolEntry{
		ToolName: "x", Status: ToolStatusComplete, Folded: false, FullResult: `{"a":1,"b":2}`,
	}, 80, false, md))
	if !strings.Contains(jsonResult, `"a": 1`) {
		t.Errorf("expanded JSON result should be pretty-printed, got:\n%s", jsonResult)
	}

	mdResult := plain(renderToolEntry(ToolEntry{
		ToolName: "x", Status: ToolStatusComplete, Folded: false, FullResult: "# Heading\n\nbody text",
	}, 80, false, md))
	if strings.Contains(mdResult, "# Heading") {
		t.Errorf("markdown heading should render (no literal '#'), got:\n%s", mdResult)
	}
	if !strings.Contains(mdResult, "Heading") || !strings.Contains(mdResult, "body text") {
		t.Errorf("rendered markdown should keep the text, got:\n%s", mdResult)
	}

	// A folded entry shows no result body (regression guard for the expand gate).
	folded := plain(renderToolEntry(ToolEntry{
		ToolName: "x", Status: ToolStatusComplete, Folded: true, FullResult: `{"a":1}`,
	}, 80, false, md))
	if strings.Contains(folded, `"a": 1`) {
		t.Errorf("folded entry must not render the full result, got:\n%s", folded)
	}
}
