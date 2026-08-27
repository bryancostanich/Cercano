package ui

import (
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

func newTestChatViewWithPalette(w, h int, p theme.Palette) *chatView {
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

func TestChatView_ViewAnimatesPlaceholderWithoutRebuildingContent(t *testing.T) {
	c := newTestChatView(60, 10)
	c.SetTurnStatus(turnStatus{
		activity: "thinking",
		start:    time.Unix(10, 0),
		model:    "local",
	})
	c.SetAnimationTime(time.Unix(12, 0))
	c.SetEntries([]*Entry{{Role: RoleAssistant, Streaming: true, Content: ""}})
	if len(c.layout.units) == 0 {
		t.Fatal("precondition: expected rendered layout units")
	}
	firstUnit := &c.layout.units[0]
	first := plain(c.View())

	c.SetAnimationTime(time.Unix(12, 0).Add(100 * time.Millisecond))
	second := plain(c.View())
	if len(c.layout.units) == 0 || &c.layout.units[0] != firstUnit {
		t.Fatal("View animation should not rebuild transcript layout")
	}
	if first == second {
		t.Fatalf("expected placeholder animation to change through View overlay; first=%q second=%q", first, second)
	}
	if !strings.Contains(second, "thinking") {
		t.Fatalf("animated placeholder lost activity text: %q", second)
	}
}

func TestChatView_UserPromptAppliesTextForegroundOnLightThemes(t *testing.T) {
	var daylight theme.Palette
	for _, t := range theme.BuiltinThemes() {
		if t.Name == "daylight" {
			daylight = t.Palette
			break
		}
	}
	c := newTestChatViewWithPalette(50, 6, daylight)
	rendered := c.renderEntry(&Entry{Role: RoleUser, Content: "readable prompt"}, 0)

	if !strings.Contains(rendered, "38;2;122;78;10") {
		t.Fatalf("daylight sent prompt should set BufferUserText foreground (#7A4E0A); got %q", rendered)
	}
	if !strings.Contains(rendered, "48;2;230;215;176") {
		t.Fatalf("daylight sent prompt should keep BufferUserBg background (#E6D7B0); got %q", rendered)
	}
}
