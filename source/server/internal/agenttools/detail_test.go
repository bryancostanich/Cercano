package agenttools

import "testing"

func TestCountLabel(t *testing.T) {
	cases := []struct {
		n               int
		sing, plur, want string
	}{
		{1, "line", "lines", "1 line"},
		{0, "line", "lines", "0 lines"},
		{5, "line", "lines", "5 lines"},
		{1, "match", "matches", "1 match"},
		{3, "match", "matches", "3 matches"},
		{1, "entry", "entries", "1 entry"},
	}
	for _, c := range cases {
		if got := countLabel(c.n, c.sing, c.plur); got != c.want {
			t.Errorf("countLabel(%d,%q,%q) = %q, want %q", c.n, c.sing, c.plur, got, c.want)
		}
	}
}

func TestLineCount(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\nb", 2},
		{"a\nb\nc", 3},
	}
	for _, c := range cases {
		if got := lineCount(c.s); got != c.want {
			t.Errorf("lineCount(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestEditDetail(t *testing.T) {
	// old 1 line -> new 3 lines: +3 −1
	if got := editDetail("a", "x\ny\nz"); got != "+3 −1" {
		t.Errorf("editDetail = %q, want %q", got, "+3 −1")
	}
	// 2 lines removed, 1 added
	if got := editDetail("a\nb", "x"); got != "+1 −2" {
		t.Errorf("editDetail = %q, want %q", got, "+1 −2")
	}
}
