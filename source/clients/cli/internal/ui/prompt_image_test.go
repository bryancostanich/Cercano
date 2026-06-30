package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestAddImageInsertsMarkerAndRegisters(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1, 2, 3}, "image/png", "/tmp/a.png")
	if !strings.Contains(p.Value(), "[image 1]") {
		t.Fatalf("marker not inserted: %q", p.Value())
	}
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 1 || att[0].mediaType != "image/png" {
		t.Fatalf("attachment not registered: %+v", att)
	}
}

func TestBackspaceDeletesWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.InsertString("hi ")
	p.AddImage([]byte{1}, "image/png", "")
	// cursor is right after "[image 1]". One backspace removes the whole chip.
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if strings.Contains(p.Value(), "image") {
		t.Fatalf("chip not deleted atomically: %q", p.Value())
	}
	if p.Value() != "hi " {
		t.Fatalf("want \"hi \" after deleting chip, got %q", p.Value())
	}
	if len(p.Attachments()) != 0 {
		t.Fatalf("attachment should drop out once its marker is gone: %+v", p.Attachments())
	}
}

func TestDeleteForwardDeletesWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")
	p.InsertString(" tail")
	p.CursorStart() // cursor before "[image 1]"
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
	if p.Value() != " tail" {
		t.Fatalf("delete-forward should remove whole chip, got %q", p.Value())
	}
}

func TestAttachmentsFollowMarkers(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")  // [image 1]
	p.AddImage([]byte{2}, "image/gif", "")  // [image 2]
	if len(p.Attachments()) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(p.Attachments()))
	}
	// Backspace removes the trailing chip ([image 2]); attachment 2 drops out.
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	att := p.Attachments()
	if len(att) != 1 || att[0].id != 1 {
		t.Fatalf("Attachments must follow surviving markers, got %+v", att)
	}
}

func TestResetClearsAttachments(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")
	p.Reset()
	if len(p.Attachments()) != 0 || len(p.attachments) != 0 {
		t.Fatalf("Reset must clear attachments")
	}
}

func TestCursorSkipsChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.InsertString("ab")
	p.AddImage([]byte{1}, "image/png", "") // "ab[image 1]"
	p.CursorStart()
	// move right past 'a','b', then one more right should jump the whole chip.
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	before := p.cursor
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if p.cursor-before <= 1 {
		t.Fatalf("right arrow should jump over the whole chip, moved %d", p.cursor-before)
	}
	if p.cursor != len([]rune(p.Value())) {
		t.Fatalf("cursor should land at end of chip, got %d of %d", p.cursor, len([]rune(p.Value())))
	}
}

func TestSelectionExpandsToWholeChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "") // "[image 1]"
	p.cursor = 0
	p.selectionAnchor = 3 // anchor inside the chip
	start, end, ok := p.selectionRange()
	if !ok || start != 0 || end != len([]rune(p.Value())) {
		t.Fatalf("selection touching a chip must swallow it whole: start=%d end=%d ok=%v", start, end, ok)
	}
}
