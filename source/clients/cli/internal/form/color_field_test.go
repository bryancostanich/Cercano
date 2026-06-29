package form

import (
	"strings"
	"testing"
)

func TestColorFieldEditCommit(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter()) // begin edit
	if !f.Editing() {
		t.Fatal("enter should begin editing an editable color")
	}
	for _, r := range "#123456" {
		f.Update(typ(r))
	}
	_, committed, val := f.Update(enter())
	if !committed || val != "#123456" {
		t.Fatalf("commit = (%v,%q), want (true,#123456)", committed, val)
	}
	if f.Hex() != "#123456" {
		t.Fatalf("Hex() = %q", f.Hex())
	}
}

func TestColorFieldRejectsBadHex(t *testing.T) {
	f := NewColor("accent", "accent", "#bdf000", true)
	f.Update(enter())
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
