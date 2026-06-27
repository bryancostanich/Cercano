package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func enter() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func esc() tea.KeyPressMsg        { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func arrowDown() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyDown} }
func arrowLeft() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyLeft} }
func arrowRight() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyRight} }
func typ(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestTextFieldEditCommit(t *testing.T) {
	f := NewText("ollama-url", "ollama-url", "http://old", "")
	// Enter activates edit mode; not yet committed.
	_, committed, _ := f.Update(enter())
	if !f.Editing() || committed {
		t.Fatalf("first enter should begin editing, not commit (editing=%v committed=%v)", f.Editing(), committed)
	}
	for _, r := range "x" {
		f.Update(typ(r))
	}
	// Second enter commits the edited value.
	_, committed, val := f.Update(enter())
	if !committed {
		t.Fatal("second enter should commit")
	}
	if f.Editing() {
		t.Fatal("commit should exit editing")
	}
	if val != "http://oldx" {
		t.Fatalf("committed value = %q, want http://oldx (typed char must be appended)", val)
	}
}

func TestTextFieldEscCancels(t *testing.T) {
	f := NewText("k", "k", "orig", "")
	f.Update(enter())
	_, committed, _ := f.Update(esc())
	if committed || f.Editing() {
		t.Fatalf("esc should cancel without commit (committed=%v editing=%v)", committed, f.Editing())
	}
	if f.Display() != "orig" {
		t.Fatalf("Display after cancel = %q, want orig", f.Display())
	}
}

func TestMaskedFieldDisplayAndBlankOnEdit(t *testing.T) {
	f := NewMasked("cloud-api-key", "cloud-api-key", true)
	if f.Display() != "(set)" {
		t.Fatalf("masked Display = %q, want (set)", f.Display())
	}
	f.Update(enter()) // begin edit — input must start blank
	if got := f.currentInput(); got != "" {
		t.Fatalf("masked edit should start blank, got %q", got)
	}
}
