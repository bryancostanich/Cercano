package ui

import (
	"strings"
	"testing"
)

func TestScrollbarColumnNoOverflow(t *testing.T) {
	// total <= height → all blanks, no thumb.
	col := scrollbarColumn(5, 10, 0)
	if len(col) != 10 {
		t.Fatalf("len = %d, want 10", len(col))
	}
	if got := string(col); got != strings.Repeat(" ", 10) {
		t.Fatalf("no-overflow column = %q, want 10 spaces", got)
	}
}

func TestScrollbarColumnTop(t *testing.T) {
	// total=40, height=10, yOffset=0 → thumb at top, size = max(1,round(10*10/40))=3.
	col := scrollbarColumn(40, 10, 0)
	want := "███" + strings.Repeat("░", 7)
	if got := string(col); got != want {
		t.Fatalf("top column = %q, want %q", got, want)
	}
}

func TestScrollbarColumnBottom(t *testing.T) {
	// yOffset = total-height = 30 → thumb flush at bottom.
	col := scrollbarColumn(40, 10, 30)
	want := strings.Repeat("░", 7) + "███"
	if got := string(col); got != want {
		t.Fatalf("bottom column = %q, want %q", got, want)
	}
}

func TestScrollbarThumbMinSize(t *testing.T) {
	// Huge total → thumb clamps to size 1, never 0.
	_, size, ok := scrollbarThumb(100000, 20, 0)
	if !ok || size != 1 {
		t.Fatalf("thumb size = %d ok=%v, want size 1 ok=true", size, ok)
	}
}

func TestScrollOffsetFromClick(t *testing.T) {
	// viewport top=2, height=10, total=40 (max offset 30).
	cases := []struct {
		clickRow, want int
	}{
		{2, 0},    // top row → offset 0
		{11, 30},  // bottom row (top+height-1) → max offset 30
		{2 - 5, 0},  // above viewport → clamp 0
		{2 + 100, 30}, // far below → clamp max
	}
	for _, c := range cases {
		if got := scrollOffsetFromClick(c.clickRow, 2, 10, 40); got != c.want {
			t.Errorf("scrollOffsetFromClick(%d,2,10,40) = %d, want %d", c.clickRow, got, c.want)
		}
	}
}
