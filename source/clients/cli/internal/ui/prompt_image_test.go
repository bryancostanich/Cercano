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
