package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestUndoRestoresDeletedChip verifies chip deletion is undoable: deleteSpan runs
// inside deleteBackward, which handleKey invokes through applyEdit, so the undo
// stack captures it. Restoring the marker text re-includes the (append-only)
// attachment.
func TestUndoRestoresDeletedChip(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.InsertString("hi ")
	p.AddImage([]byte{1}, "image/png", "")
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if strings.Contains(p.Value(), "image") {
		t.Fatalf("precondition: chip should be gone, got %q", p.Value())
	}
	p, _ = p.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}) // undo
	if !strings.Contains(p.Value(), "[image 1]") {
		t.Fatalf("undo should restore the chip marker, got %q", p.Value())
	}
	if len(p.Attachments()) != 1 {
		t.Fatalf("undo should re-include the attachment, got %d", len(p.Attachments()))
	}
}

// TestSetValueClearsRegistry verifies SetValue (with non-empty text, not just the
// Reset/"" path) drops all attachments and resets the id counter.
func TestSetValueClearsRegistry(t *testing.T) {
	p := newPromptInput()
	p.Focus()
	p.AddImage([]byte{1}, "image/png", "")
	p.SetValue("a fresh draft")
	if len(p.attachments) != 0 || p.nextImageID != 0 {
		t.Fatalf("SetValue must clear the registry, got %d attachments, nextID=%d", len(p.attachments), p.nextImageID)
	}
	if len(p.Attachments()) != 0 {
		t.Fatalf("no live attachments after SetValue, got %d", len(p.Attachments()))
	}
}
