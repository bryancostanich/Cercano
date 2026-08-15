package theme

import "testing"

func TestSelectionBackgroundSGRUsesPaletteToken(t *testing.T) {
	p := Cracker()
	p.SelectionBg = mustHex("#123456")
	if got := SelectionBackgroundSGR(p); got != "\x1b[48;2;18;52;86m" {
		t.Fatalf("SelectionBackgroundSGR = %q", got)
	}
}

func TestActivityAndSpinnerColorsUsePaletteTokens(t *testing.T) {
	p := Cracker()
	p.ActivityBase = mustHex("#010203")
	p.ActivityPeak = mustHex("#111213")
	if got := Hex(ActivityColorAt(p, 10, 0, 4)); got != "#010203" {
		t.Fatalf("ActivityColorAt outside sweep = %s", got)
	}
	if got := Hex(ActivityColorAt(p, 0, 0, 4)); got != "#111213" {
		t.Fatalf("ActivityColorAt at peak = %s", got)
	}

	p.SpinnerBase = mustHex("#202122")
	p.SpinnerPeak = mustHex("#303132")
	if got := Hex(SpinnerColorAt(p, 0)); got != "#202122" {
		t.Fatalf("SpinnerColorAt base = %s", got)
	}
	if got := Hex(SpinnerColorAt(p, 1)); got != "#303132" {
		t.Fatalf("SpinnerColorAt peak = %s", got)
	}
}
