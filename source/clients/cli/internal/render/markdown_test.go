package render

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

// These exercise matchTable's detection rules through SplitBlocks, which is the
// only consumer now that InterceptMarkdownTables is retired.

func TestTable_NarrowKeepsAllColumns(t *testing.T) {
	in := "| Key | Mid | Desc |\n| --- | --- | --- |\n" +
		"| K1 | M1 | a long explanatory field that forces the table to shrink |\n\nafter"
	blocks, _ := SplitBlocks(in)
	var tbl *Table
	for _, b := range blocks {
		if b.Kind == MdTable {
			tbl = b.Table
		}
	}
	if tbl == nil {
		t.Fatal("expected a table block")
	}
	out := stripAnsi(tbl.Render(22, theme.NewStyles(theme.Cracker())))
	// A narrow render must drop nothing — every header (so every column) and the
	// short cell values survive. (The long Desc value wraps across lines, which
	// is fine — wrapping never loses data.)
	for _, want := range []string{"Key", "Mid", "Desc", "K1", "M1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q preserved at narrow width, got %q", want, out)
		}
	}
	if strings.Contains(out, "dropped") {
		t.Fatalf("no column should be dropped, got %q", out)
	}
}

func TestMatch_RejectsBareTextWithPipes(t *testing.T) {
	// Bare pipes without a separator row are not a markdown table.
	blocks, _ := SplitBlocks("a | b | c\nfoo\n")
	for _, b := range blocks {
		if b.Kind == MdTable {
			t.Fatalf("bare pipes should not parse as a table: %#v", b)
		}
	}
}

func TestMatch_RequiresSeparatorRow(t *testing.T) {
	blocks, tail := SplitBlocks("| col1 | col2 |\n| data1 | data2 |\n")
	for _, b := range blocks {
		if b.Kind == MdTable {
			t.Fatalf("no separator row → not a table: %#v", b)
		}
	}
	_ = tail
}

func TestMatch_ParsesColumnsAndRows(t *testing.T) {
	in := "| model | size |\n| --- | --- |\n| qwen | 4.7GB |\n| nomic | 274MB |\n\nafter"
	blocks, _ := SplitBlocks(in)
	if len(blocks) < 1 || blocks[0].Kind != MdTable || blocks[0].Table == nil {
		t.Fatalf("expected leading table block: %#v", blocks)
	}
	tbl := blocks[0].Table
	if len(tbl.Cols) != 2 || tbl.Cols[0].Name != "model" || tbl.Cols[1].Name != "size" {
		t.Fatalf("columns wrong: %+v", tbl.Cols)
	}
	if len(tbl.Rows) != 2 || tbl.Rows[1]["model"] != "nomic" {
		t.Fatalf("rows wrong: %+v", tbl.Rows)
	}
	// Every column is wrappable so no single fat cell forces a transpose.
	if !tbl.Cols[0].Wrappable || !tbl.Cols[1].Wrappable {
		t.Fatalf("expected all columns wrappable: %+v", tbl.Cols)
	}
}
