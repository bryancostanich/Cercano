package ui

import (
	"strings"
	"testing"
)

// TestRegisterImageResolvesExistingMarkerNoDuplicate covers the unstage round-trip
// path: text with an existing "[image N]" marker is restored, then RegisterImage
// re-registers the image WITHOUT inserting a second marker. Attachments() must
// resolve the existing marker, and a subsequent AddImage must not reuse the id.
func TestRegisterImageResolvesExistingMarkerNoDuplicate(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.SetValue("see [image 2] ok") // restored text already carries the marker
	if len(p.Attachments()) != 0 {
		t.Fatalf("no attachments registered yet, marker is inert: %d", len(p.Attachments()))
	}
	p.RegisterImage(2, []byte{9}, "image/gif", "/tmp/x.gif")

	// The existing marker now resolves — no new marker inserted.
	if got := strings.Count(p.Value(), "[image"); got != 1 {
		t.Fatalf("RegisterImage must not insert a duplicate marker; markers=%d in %q", got, p.Value())
	}
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 2 || att[0].mediaType != "image/gif" {
		t.Fatalf("Attachments should resolve the existing marker: %+v", att)
	}

	// A later AddImage must not reuse id 2.
	p.AddImage([]byte{1}, "image/png", "")
	if !strings.Contains(p.Value(), "[image 3]") {
		t.Fatalf("AddImage after RegisterImage(2) should mint id 3, got %q", p.Value())
	}
}
