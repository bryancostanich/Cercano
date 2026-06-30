package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestVisionNoticeShownWhenUnsupportedAndImageAttached(t *testing.T) {
	m := newTestModelWithPrompt()
	m.styles = theme.NewStyles(theme.Cracker())
	m.supportsVision = false
	m.input.AddImage([]byte{1}, "image/png", "")
	if m.visionNotice() == "" {
		t.Fatal("expected a vision notice when an image is attached and vision is unsupported")
	}
}

func TestVisionNoticeHiddenWhenSupported(t *testing.T) {
	m := newTestModelWithPrompt()
	m.styles = theme.NewStyles(theme.Cracker())
	m.supportsVision = true
	m.input.AddImage([]byte{1}, "image/png", "")
	if m.visionNotice() != "" {
		t.Fatal("no notice when the model supports vision")
	}
}

func TestVisionNoticeHiddenWhenNoImage(t *testing.T) {
	m := newTestModelWithPrompt()
	m.styles = theme.NewStyles(theme.Cracker())
	m.supportsVision = false
	if n := m.visionNotice(); n != "" {
		t.Fatalf("no notice without an attached image, got %q", n)
	}
	_ = strings.TrimSpace
}
