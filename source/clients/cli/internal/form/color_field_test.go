package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestColorFieldEditCommit(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter()) // begin edit — input is now seeded with current hex
	if !f.Editing() {
		t.Fatal("enter should begin editing an editable color")
	}
	// Backspace to clear the seeded value (7 backspaces for 7-char hex)
	for i := 0; i < 7; i++ {
		f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	// Type new hex
	for _, r := range "#abcd12" {
		f.Update(typ(r))
	}
	_, committed, val := f.Update(enter())
	if !committed || val != "#abcd12" {
		t.Fatalf("commit = (%v,%q), want (true,#abcd12)", committed, val)
	}
	if f.Hex() != "#abcd12" {
		t.Fatalf("Hex() = %q", f.Hex())
	}
}

func TestColorFieldRejectsBadHex(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter()) // begin edit — input is seeded with current hex
	// Backspace to clear the seeded value
	for i := 0; i < 7; i++ {
		f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	// Type bad hex value
	for _, r := range "nope" {
		f.Update(typ(r))
	}
	_, committed, _ := f.Update(enter())
	if committed {
		t.Fatal("bad hex must not commit")
	}
	if f.Hex() != "#bdf000" {
		t.Fatalf("Hex() after bad edit = %q, want unchanged", f.Hex())
	}
}

func TestColorFieldReadOnlyInert(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", false)
	_, committed, _ := f.Update(enter())
	if f.Editing() || committed {
		t.Fatal("read-only color field must be inert")
	}
}

func TestColorFieldViewHasSwatch(t *testing.T) {
	_, s := testStyles()
	f := NewColor("accent", "accent", "#bdf000", true)
	if !strings.Contains(f.View(false, 40, s), "#bdf000") {
		t.Fatal("View should show the hex")
	}
}

func TestColorFieldEditSeededWithCurrent(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter())            // begin edit — input should be pre-seeded
	_, committed, val := f.Update(enter()) // commit immediately, no typing
	if !committed || val != "#bdf000" {
		t.Fatalf("commit without typing should yield the seeded value, got committed=%v val=%q", committed, val)
	}
}
