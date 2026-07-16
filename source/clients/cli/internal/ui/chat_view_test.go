package ui

import (
	"reflect"
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

func TestTranscriptLayoutFlattenedContentMatchesLegacyContent(t *testing.T) {
	c := newTestChatView(72, 12)
	entries := []*Entry{
		{Role: RoleUser, Content: "please inspect this"},
		{Role: RoleAssistant, Content: "I can help with that.\n\n- first\n- second"},
		{Role: RoleSystem, Content: "notice line"},
	}
	c.SetEntries(entries)

	if got, want := c.layout.flattenedContent(), c.content; got != want {
		t.Fatalf("virtual layout flattened content mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestTranscriptLayoutToolRowsMatchLegacyArrowRows(t *testing.T) {
	c := newTestChatView(100, 20)
	c.SetEntries(toolClickEntries())

	if got, want := c.layout.flattenedContent(), c.content; got != want {
		t.Fatalf("virtual layout flattened tool content mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if got, want := c.layout.absoluteArrowRows(), c.arrowRows; !reflect.DeepEqual(got, want) {
		t.Fatalf("virtual layout arrow rows mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
