package ui

import "testing"

func TestVirtualScrollClampsOffsets(t *testing.T) {
	s := newVirtualScroll(80, 10)
	s.SetTotalLineCount(100)

	s.SetYOffset(50)
	if got, want := s.YOffset(), 50; got != want {
		t.Fatalf("YOffset after in-range SetYOffset = %d, want %d", got, want)
	}

	s.SetYOffset(-20)
	if got, want := s.YOffset(), 0; got != want {
		t.Fatalf("negative SetYOffset should clamp to %d, got %d", want, got)
	}

	s.SetYOffset(999)
	if got, want := s.YOffset(), 90; got != want {
		t.Fatalf("oversized SetYOffset should clamp to max %d, got %d", want, got)
	}
}

func TestVirtualScrollBottomSemantics(t *testing.T) {
	s := newVirtualScroll(80, 10)
	s.SetTotalLineCount(8)
	if !s.AtBottom() {
		t.Fatal("short content should be at bottom")
	}
	if got := s.YOffset(); got != 0 {
		t.Fatalf("short content offset = %d, want 0", got)
	}

	s.SetTotalLineCount(25)
	if s.AtBottom() {
		t.Fatal("overflowing content should not be at bottom before GotoBottom")
	}
	s.GotoBottom()
	if !s.AtBottom() {
		t.Fatal("GotoBottom should place scroll at bottom")
	}
	if got, want := s.YOffset(), 15; got != want {
		t.Fatalf("bottom offset = %d, want %d", got, want)
	}
}

func TestVirtualScrollScrollUpDown(t *testing.T) {
	s := newVirtualScroll(80, 10)
	s.SetTotalLineCount(30)

	s.ScrollDown(7)
	if got, want := s.YOffset(), 7; got != want {
		t.Fatalf("ScrollDown offset = %d, want %d", got, want)
	}
	s.ScrollUp(3)
	if got, want := s.YOffset(), 4; got != want {
		t.Fatalf("ScrollUp offset = %d, want %d", got, want)
	}
	s.ScrollDown(100)
	if got, want := s.YOffset(), 20; got != want {
		t.Fatalf("ScrollDown should clamp to %d, got %d", want, got)
	}
	s.ScrollUp(100)
	if got, want := s.YOffset(), 0; got != want {
		t.Fatalf("ScrollUp should clamp to %d, got %d", want, got)
	}
}

func TestVirtualScrollResizeClampsToNewSurface(t *testing.T) {
	s := newVirtualScroll(80, 10)
	s.SetTotalLineCount(100)
	s.GotoBottom()
	if got, want := s.YOffset(), 90; got != want {
		t.Fatalf("initial bottom offset = %d, want %d", got, want)
	}

	s.SetSize(80, 40)
	if got, want := s.YOffset(), 60; got != want {
		t.Fatalf("resize taller should clamp bottom offset to %d, got %d", want, got)
	}

	s.SetTotalLineCount(20)
	if got, want := s.YOffset(), 0; got != want {
		t.Fatalf("shorter content should clamp offset to %d, got %d", want, got)
	}
}

func TestVirtualScrollNegativeSizesBecomeZero(t *testing.T) {
	s := newVirtualScroll(-1, -2)
	if got := s.Width(); got != 0 {
		t.Fatalf("negative width should normalize to 0, got %d", got)
	}
	if got := s.Height(); got != 0 {
		t.Fatalf("negative height should normalize to 0, got %d", got)
	}

	s.SetTotalLineCount(5)
	s.GotoBottom()
	if got, want := s.YOffset(), 0; got != want {
		t.Fatalf("zero-height scroll surface should keep offset %d, got %d", want, got)
	}
}
