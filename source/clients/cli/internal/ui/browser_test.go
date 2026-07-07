package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestHyperlinkEmitsOSC8(t *testing.T) {
	got := hyperlink("https://x/y", "label")
	want := "\x1b]8;;https://x/y\x1b\\label\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("hyperlink:\n got %q\nwant %q", got, want)
	}
}

func TestWaitingModalURLIsClickableAndOffersBrowser(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newChatGPTLoginModal("chatgpt", "")
	mo.setCode("https://auth.openai.com/codex/device", "AB-12")
	out := mo.View(styles, pal, 80, 24)
	if !strings.Contains(out, "\x1b]8;;https://auth.openai.com/codex/device") {
		t.Error("waiting modal URL should be wrapped in an OSC 8 hyperlink")
	}
	if !strings.Contains(out, "open browser") {
		t.Error("waiting modal should advertise the [o] open-browser key")
	}
}
