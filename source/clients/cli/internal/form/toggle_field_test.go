package form

import "testing"

func TestToggleFieldFlips(t *testing.T) {
	f := NewToggle("flag", "flag", false)
	if f.Display() != "off" {
		t.Fatalf("Display = %q, want off", f.Display())
	}
	_, committed, val := f.Update(enter())
	if !committed || val != "true" {
		t.Fatalf("enter should flip+commit to true, got committed=%v val=%q", committed, val)
	}
	if f.Display() != "on" {
		t.Fatalf("Display after flip = %q, want on", f.Display())
	}
	if f.Editing() {
		t.Fatal("toggle never stays in editing mode")
	}
}
