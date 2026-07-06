package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/banner"
)

func testBannerMeta() banner.Meta {
	return banner.Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		Model:   "qwen3-coder",
	}
}

// TestChatView_BannerEntryWide checks that a banner entry renders the full
// fixed-width wordmark block when the viewport is wide enough to hold it.
func TestChatView_BannerEntryWide(t *testing.T) {
	c := newTestChatView(80, 12)
	meta := testBannerMeta()
	c.SetEntries([]*Entry{{Banner: &meta}})
	visible := plain(c.View())
	if !strings.Contains(visible, "╔") {
		t.Errorf("wide banner entry should render the boxed chrome; got:\n%s", visible)
	}
	if !strings.Contains(visible, "local-first ai coprocessor") {
		t.Errorf("wide banner entry should render the tagline; got:\n%s", visible)
	}
}

// TestChatView_BannerEntryNarrowFallback checks that a banner entry degrades
// to the compact one-liner (no boxed chrome) below the banner's fixed width.
func TestChatView_BannerEntryNarrowFallback(t *testing.T) {
	c := newTestChatView(40, 12)
	meta := testBannerMeta()
	c.SetEntries([]*Entry{{Banner: &meta}})
	visible := plain(c.View())
	if strings.Contains(visible, "╔") {
		t.Errorf("narrow banner entry must not render the boxed chrome; got:\n%s", visible)
	}
	if !strings.Contains(visible, "CERCANO") {
		t.Errorf("narrow banner entry should render the compact wordmark; got:\n%s", visible)
	}
}

// TestChatView_PrependBannerIdempotent checks that PrependBanner inserts the
// banner as entry zero exactly once, ahead of any existing entries.
func TestChatView_PrependBannerIdempotent(t *testing.T) {
	c := newTestChatView(80, 12)
	c.AppendEntry(&Entry{Role: RoleUser, Content: "hello"})
	c.PrependBanner(testBannerMeta(), time.Now())
	c.PrependBanner(testBannerMeta(), time.Now())
	entries := c.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (banner + user)", len(entries))
	}
	if entries[0].Banner == nil {
		t.Errorf("entry zero should be the banner")
	}
	if entries[1].Role != RoleUser || entries[1].Content != "hello" {
		t.Errorf("existing entry should be pushed down intact; got %+v", entries[1])
	}
}
