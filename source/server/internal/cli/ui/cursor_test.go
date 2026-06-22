package ui

import "testing"

func TestInputCursorRow(t *testing.T) {
	parts := []string{
		"header",             // 1 line  → row 0
		"────────",           // 1 line  → row 1
		"line a\nline b",     // 2 lines → rows 2-3
		"viewport\nrow\nrow", // 3 lines → rows 4-6
		"input here",         // input   → row 7
	}
	got := inputCursorRow(parts, 4)
	if got != 7 {
		t.Fatalf("inputCursorRow = %d, want 7", got)
	}
}

func TestInputCursorRowFirstEntry(t *testing.T) {
	// inputIdx==0: input appended first in parts → cursor row is 0.
	parts := []string{
		"input here", // input → row 0
	}
	got := inputCursorRow(parts, 0)
	if got != 0 {
		t.Fatalf("inputCursorRow = %d, want 0", got)
	}
}
