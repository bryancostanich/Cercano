package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

// newTestChatView returns a *chatView sized to w×h for unit tests.
// newChatView returns a value, so we take its address here.
func newTestChatView(w, h int) *chatView {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", w, h)
	return &cv
}

func setChatTestContent(c *chatView, content string) {
	lines := strings.Split(content, "\n")
	c.layout = transcriptLayout{
		width:     c.Width(),
		stylesGen: c.stylesGen,
		units: []renderUnit{{
			kind:      unitEntry,
			startLine: 0,
			lineCount: len(lines),
			lines:     lines,
		}},
		totalLines: len(lines),
	}
	c.scroll.SetTotalLineCount(len(lines))
	c.plainDirty = true
	c.plainLines = nil
}

// TestChatView_ScrollSurfaceMatchesViewport checks that the scroll surface
// methods (TotalLineCount, AtBottom, SetYOffset, YOffset, GotoBottom) behave
// as expected after loading content.
func TestChatView_ScrollSurfaceMatchesViewport(t *testing.T) {
	c := newTestChatView(40, 5)
	entries := make([]*Entry, 0, 30)
	for i := 0; i < 30; i++ {
		entries = append(entries, &Entry{Role: RoleSystem, Content: "line"})
	}
	c.SetEntries(entries)
	if c.TotalLineCount() < 30 {
		t.Fatalf("TotalLineCount = %d, want >= 30", c.TotalLineCount())
	}
	if !c.AtBottom() {
		t.Fatalf("fresh SetEntries on an at-bottom viewport should auto-follow to bottom")
	}
	c.SetYOffset(0)
	if c.YOffset() != 0 {
		t.Fatalf("SetYOffset(0) → YOffset %d, want 0", c.YOffset())
	}
	c.GotoBottom()
	if !c.AtBottom() {
		t.Fatalf("GotoBottom() should land at bottom")
	}
}

// TestChatView_TurnStatusPlaceholder verifies that an in-flight streaming entry
// (Content=="") renders the activity verb, model name, and tier string from the
// injected turnStatus. This branch is excluded from goldens (time-animated) so
// it needs its own test.
func TestChatView_TurnStatusPlaceholder(t *testing.T) {
	c := newTestChatView(60, 10)
	c.SetTurnStatus(turnStatus{
		activity: "routing",
		start:    time.Now().Add(-3 * time.Second),
		tokOut:   7,
		model:    "opus",
		cloud:    true,
	})
	c.SetEntries([]*Entry{{Role: RoleAssistant, Streaming: true, Content: ""}})
	out := c.View()
	// Strip ANSI so we match visible text.
	visible := plain(out)
	if !strings.Contains(visible, "routing") {
		t.Errorf("placeholder should contain the activity verb; got:\n%s", visible)
	}
	if !strings.Contains(visible, "opus") {
		t.Errorf("placeholder should contain the model name; got:\n%s", visible)
	}
	if !strings.Contains(visible, "cloud") {
		t.Errorf("placeholder should contain the tier (cloud/local); got:\n%s", visible)
	}
}

// TestChatView_ViewIdentityOverlayMatchesNoSelection checks that View with an
// identity selOverlay (pass-through) produces non-empty output for a simple
// user entry.
func TestChatView_ViewIdentityOverlayMatchesNoSelection(t *testing.T) {
	c := newTestChatView(50, 6)
	c.SetEntries([]*Entry{{Role: RoleUser, Content: "hello world"}})
	out := c.View()
	if strings.TrimSpace(out) == "" {
		t.Fatalf("View produced empty output for a user entry")
	}
}

func TestTranscriptLayoutFlattenedContentMaterializesRenderedEntries(t *testing.T) {
	c := newTestChatView(72, 12)
	entries := []*Entry{
		{Role: RoleUser, Content: "please inspect this"},
		{Role: RoleAssistant, Content: "I can help with that.\n\n- first\n- second"},
		{Role: RoleSystem, Content: "notice line"},
	}
	c.SetEntries(entries)

	got := plain(c.layout.flattenedContent())
	for _, want := range []string{"please inspect this", "I can help", "first", "second", "notice line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("virtual layout flattened content missing %q\n--- got ---\n%s", want, got)
		}
	}
	if got := c.layout.totalLines; got <= 0 {
		t.Fatalf("SetEntries should populate the virtual layout, totalLines=%d", got)
	}
}

func TestTranscriptLayoutToolRowsBackChatArrowRows(t *testing.T) {
	c := newTestChatView(100, 20)
	c.SetEntries(toolClickEntries())

	gotContent := plain(c.layout.flattenedContent())
	for _, want := range []string{"do stuff", "tool calls", "prose after"} {
		if !strings.Contains(gotContent, want) {
			t.Fatalf("virtual layout flattened tool content missing %q\n--- got ---\n%s", want, gotContent)
		}
	}
	if got := c.layout.absoluteArrowRows(); len(got) == 0 {
		t.Fatalf("virtual layout should expose tool arrow rows")
	}
}

func TestChatViewViewRendersOnlyVisibleLayoutWindow(t *testing.T) {
	c := newTestChatView(40, 3)
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%03d", i)
	}
	c.layout = transcriptLayout{units: []renderUnit{{kind: unitEntry, lineCount: len(lines), lines: lines}}, totalLines: len(lines)}
	c.scroll.SetTotalLineCount(len(lines))
	c.SetYOffset(500)

	out := plain(c.View())
	for _, want := range []string{"line-500", "line-501", "line-502"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View missing visible line %q\n%s", want, out)
		}
	}
	for _, hidden := range []string{"line-000", "line-499", "line-503", "line-999"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("View rendered offscreen line %q\n%s", hidden, out)
		}
	}
}
