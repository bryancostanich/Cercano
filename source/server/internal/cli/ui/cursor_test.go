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
