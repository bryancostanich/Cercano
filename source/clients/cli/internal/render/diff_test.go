package render

import "testing"

func opString(ops []DiffLine) string {
	b := make([]byte, 0, len(ops))
	for _, o := range ops {
		switch o.Op {
		case DiffEqual:
			b = append(b, '=')
		case DiffDelete:
			b = append(b, '-')
		case DiffInsert:
			b = append(b, '+')
		}
	}
	return string(b)
}

func TestLineDiff_SingleLineChangeIsMinimal(t *testing.T) {
	ops := LineDiff("a\nb\nc", "a\nB\nc")
	// The shared a and c stay context; only b→B is a delete+insert.
	if got := opString(ops); got != "=-+=" {
		t.Fatalf("op sequence = %q, want =-+=", got)
	}
	// Verify the changed lines carry the right text.
	if ops[1].Text != "b" || ops[2].Text != "B" {
		t.Errorf("changed lines = %q/%q, want b/B", ops[1].Text, ops[2].Text)
	}
}

func TestLineDiff_AgainstEmptyIsAllInserts(t *testing.T) {
	ops := LineDiff("", "x\ny\nz")
	if got := opString(ops); got != "+++" {
		t.Errorf("op sequence = %q, want +++", got)
	}
}

func TestLineDiff_PureInsertKeepsContext(t *testing.T) {
	// Insert a line in the middle: both originals stay context.
	ops := LineDiff("one\ntwo", "one\nMID\ntwo")
	if got := opString(ops); got != "=+=" {
		t.Errorf("op sequence = %q, want =+=", got)
	}
}

func TestLineDiff_IdenticalIsAllEqual(t *testing.T) {
	ops := LineDiff("same\nlines", "same\nlines")
	if got := opString(ops); got != "==" {
		t.Errorf("op sequence = %q, want ==", got)
	}
}
