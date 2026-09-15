package form

import (
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestPasteEditorBoundaries(t *testing.T) {
	field := NewText("text", "Text", "ac", "")
	f := New([]Section{{Fields: []Field{field}}})
	if f.Paste("ignored") {
		t.Fatal("inactive editor accepted paste")
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	f.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !f.Paste("b") || field.currentInput() != "abc" {
		t.Fatal("paste did not insert at cursor")
	}
	if f.Paste("") {
		t.Fatal("empty paste accepted")
	}
	if !f.Paste("\n\r\t") || !field.Editing() {
		t.Fatal("control characters ended editing")
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if field.value != "ac" {
		t.Fatal("cancel changed saved value")
	}
	if New(nil).Paste("ignored") {
		t.Fatal("empty form accepted paste")
	}
	f = New([]Section{{Fields: []Field{NewMasked("key", "Key", false)}}})
	if f.Paste("ignored") {
		t.Fatal("inactive secret accepted paste")
	}
}
