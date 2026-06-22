package render

import "testing"

func TestSplit_ParagraphsSplitOnBlankLine(t *testing.T) {
	blocks, tail := SplitBlocks("para one\n\npara two")
	if len(blocks) != 1 || blocks[0].Kind != MdProse || blocks[0].Raw != "para one" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if tail != "para two" {
		t.Fatalf("tail = %q, want %q", tail, "para two")
	}
}

func TestSplit_TrailingNewlineIsNotASeparator(t *testing.T) {
	blocks, tail := SplitBlocks("just one line\n")
	if len(blocks) != 0 {
		t.Fatalf("expected no committed blocks, got %#v", blocks)
	}
	if tail != "just one line" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_CodeFenceIsCodeBlockWithLang(t *testing.T) {
	in := "```go\nfunc main() {\n\n}\n```\n\nafter"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 1 || blocks[0].Kind != MdCode {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[0].Lang != "go" {
		t.Fatalf("lang = %q, want go", blocks[0].Lang)
	}
	if blocks[0].Raw != "```go\nfunc main() {\n\n}\n```" {
		t.Fatalf("fence block raw = %q", blocks[0].Raw)
	}
	if tail != "after" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_CodeFenceSeparatesFromSurroundingProse(t *testing.T) {
	in := "intro para\n\n```json\n{}\n```\n\noutro para\n"
	blocks, _ := SplitBlocks(in)
	if len(blocks) != 2 {
		t.Fatalf("expected prose + code blocks, got %#v", blocks)
	}
	if blocks[0].Kind != MdProse || blocks[0].Raw != "intro para" {
		t.Fatalf("block 0 = %#v", blocks[0])
	}
	if blocks[1].Kind != MdCode || blocks[1].Lang != "json" {
		t.Fatalf("block 1 = %#v", blocks[1])
	}
}

func TestSplit_OpenFenceIsTail(t *testing.T) {
	blocks, tail := SplitBlocks("intro\n\n```go\nfunc main() {")
	if len(blocks) != 1 || blocks[0].Raw != "intro" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if tail != "```go\nfunc main() {" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_TerminatedTableIsTableBlock(t *testing.T) {
	in := "| A | B |\n| --- | --- |\n| 1 | 2 |\n\nnext"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 1 || blocks[0].Kind != MdTable || blocks[0].Table == nil {
		t.Fatalf("blocks = %#v", blocks)
	}
	if len(blocks[0].Table.Cols) != 2 || len(blocks[0].Table.Rows) != 1 {
		t.Fatalf("table parsed wrong: %#v", blocks[0].Table)
	}
	if tail != "next" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestSplit_UnterminatedTableFallsToTail(t *testing.T) {
	// No trailing newline, nothing after — table is still streaming.
	in := "| A | B |\n| --- | --- |\n| 1 | 2 |"
	blocks, tail := SplitBlocks(in)
	if len(blocks) != 0 {
		t.Fatalf("expected table to stay in tail, got blocks %#v", blocks)
	}
	if tail != in {
		t.Fatalf("tail = %q", tail)
	}
}
