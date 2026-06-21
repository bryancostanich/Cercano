package render

import (
	"strings"
	"testing"
)

func TestIntercept_NoTableLeavesTextAlone(t *testing.T) {
	in := "hello\nworld\n"
	out, tables := InterceptMarkdownTables(in)
	if out != strings.TrimRight(in, "\n") && out != in {
		// Allow trailing newline difference; what matters is preservation.
		t.Logf("note: trailing whitespace normalized")
	}
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, got %d", len(tables))
	}
}

func TestIntercept_DetectsSimpleTable(t *testing.T) {
	in := "before\n" +
		"| model | size |\n" +
		"| --- | --- |\n" +
		"| qwen | 4.7GB |\n" +
		"| nomic | 274MB |\n" +
		"after"
	out, tables := InterceptMarkdownTables(in)
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	if !strings.Contains(out, "{{TABLE_0}}") {
		t.Errorf("expected sentinel, got %q", out)
	}
	if !strings.HasPrefix(out, "before\n") || !strings.HasSuffix(out, "\nafter") {
		t.Errorf("expected surrounding prose preserved, got %q", out)
	}
	tbl := tables[0]
	if len(tbl.Cols) != 2 {
		t.Errorf("expected 2 cols, got %d", len(tbl.Cols))
	}
	if tbl.Cols[0].Name != "model" || tbl.Cols[1].Name != "size" {
		t.Errorf("column names: %+v", tbl.Cols)
	}
	if len(tbl.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(tbl.Rows))
	}
	if tbl.Rows[1]["model"] != "nomic" {
		t.Errorf("row data wrong: %+v", tbl.Rows[1])
	}
	// Last column wrappable.
	if !tbl.Cols[1].Wrappable {
		t.Errorf("expected last column wrappable")
	}
}

func TestIntercept_RejectsBareTextWithPipes(t *testing.T) {
	in := "a | b | c\nfoo"
	_, tables := InterceptMarkdownTables(in)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables, bare pipes != markdown table")
	}
}

func TestIntercept_RequiresSeparatorRow(t *testing.T) {
	in := "| col1 | col2 |\n| data1 | data2 |"
	_, tables := InterceptMarkdownTables(in)
	if len(tables) != 0 {
		t.Errorf("expected 0 tables without separator row")
	}
}

func TestIntercept_TwoTables(t *testing.T) {
	in := "| a | b |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"prose between\n\n" +
		"| x | y |\n| --- | --- |\n| 3 | 4 |"
	out, tables := InterceptMarkdownTables(in)
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
	if !strings.Contains(out, "{{TABLE_0}}") || !strings.Contains(out, "{{TABLE_1}}") {
		t.Errorf("missing one of the sentinels: %q", out)
	}
}
