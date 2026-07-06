package ui

import (
	"testing"
	"time"
)

// TestChatView_BannerAnimVisible checks the animation gate: on when the
// banner's rows sit inside the visible window of a wide viewport, off once
// scrolled past them, off on narrow viewports (the one-line fallback is
// static), and off when no banner entry exists.
func TestChatView_BannerAnimVisible(t *testing.T) {
	c := newTestChatView(80, 6)
	if c.BannerAnimVisible() {
		t.Fatalf("no banner entry — BannerAnimVisible should be false")
	}
	c.AppendEntry(&Entry{Role: RoleUser, Content: "hello"})
	c.PrependBanner(testBannerMeta(), time.Now())
	// Pad the transcript so there's enough content to scroll the banner off.
	for i := 0; i < 40; i++ {
		c.AppendEntry(&Entry{Role: RoleSystem, Content: "line"})
	}
	c.SetEntries(c.Entries())
	c.SetYOffset(0)
	if !c.BannerAnimVisible() {
		t.Errorf("banner at the top of a wide viewport should report visible")
	}
	c.SetYOffset(bannerRows) // first content line past the banner block
	if c.BannerAnimVisible() {
		t.Errorf("scrolled past the banner — should report not visible")
	}

	n := newTestChatView(40, 6)
	n.PrependBanner(testBannerMeta(), time.Now())
	n.SetEntries(n.Entries())
	n.SetYOffset(0)
	if n.BannerAnimVisible() {
		t.Errorf("narrow viewport renders the static fallback — should report not visible")
	}
}
