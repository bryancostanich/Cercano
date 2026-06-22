package render

import "testing"

// These exercise matchTable's detection rules through SplitBlocks, which is the
// only consumer now that InterceptMarkdownTables is retired.

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
	// Last column is the wrappable one by convention.
	if !tbl.Cols[1].Wrappable {
		t.Fatalf("expected last column wrappable")
	}
}
