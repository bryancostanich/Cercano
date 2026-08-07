package render

import (
	"strings"
	"testing"
)

// Regression: a table whose separator line has fewer dash groups than the
// header has columns must still be recognized. Weak local models routinely
// miscount the separator (e.g. `|---|---|---|` under a 4-column header). Before
// the tolerant-separator fix, matchTable rejected this outright and the table
// fell through to the prose renderer, producing run-together `| ... |` soup in
// the TUI. See the LUNIE conversation table incident.
func TestMatchTable_TolerantSeparatorColumnCount(t *testing.T) {
	// 4-column header, 3-group separator (the exact defect shape), 4-column rows.
	lines := []string{
		"| Axis | A | B | C |",
		"|---|---|---|",
		"| Fidelity | High | Medium | High |",
		"| Cost | Low | High | Medium |",
	}
	mt, consumed := matchTable(lines, 0)
	if consumed == 0 {
		t.Fatal("matchTable rejected a table with a miscounted separator; header count must drive")
	}
	if got := len(mt.Headers); got != 4 {
		t.Fatalf("headers = %d, want 4", got)
	}
	if got := len(mt.Rows); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	// Every row is normalized to the header column count.
	for i, row := range mt.Rows {
		if len(row) != 4 {
			t.Fatalf("row[%d] width = %d, want 4 (normalized to header)", i, len(row))
		}
	}
	if consumed != len(lines) {
		t.Fatalf("consumed = %d, want %d", consumed, len(lines))
	}
}

// The tolerant separator must not turn genuine prose into a table. A pipe-y
// prose line under a non-separator line stays prose.
func TestMatchTable_RejectsNonSeparator(t *testing.T) {
	lines := []string{
		"| this looks | pipe-ish |",
		"| but this row | is not dashes |",
	}
	if _, consumed := matchTable(lines, 0); consumed != 0 {
		t.Fatal("matchTable matched prose that has no valid separator line")
	}
}

// End-to-end: SplitBlocks must emit an MdTable for the miscounted-separator
// shape, not a prose block.
func TestSplitBlocks_MiscountedSeparatorBecomesMdTable(t *testing.T) {
	doc := strings.Join([]string{
		"Here is the comparison:",
		"",
		"| Axis | A | B | C |",
		"|---|---|---|",
		"| Fidelity | High | Medium | High |",
		"| Cost | Low | High | Medium |",
		"",
		"That is the summary.",
	}, "\n")
	blocks, _ := SplitBlocks(doc)
	var gotTable bool
	for _, blk := range blocks {
		if blk.Kind == MdTable {
			gotTable = true
		}
	}
	if !gotTable {
		t.Fatal("SplitBlocks produced no MdTable block for a miscounted-separator table")
	}
}
