package server

import "testing"

// sanitizeSuggestion normalises whatever the coproc emits into a single-line,
// 80-char, unquoted prompt suggestion. These tests lock in the shape.
func TestSanitizeSuggestion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"passthrough", "run the tests", "run the tests"},
		{"trim_whitespace", "   commit the fix  ", "commit the fix"},
		{"strip_double_quotes", `"add a regression test"`, "add a regression test"},
		{"strip_single_quotes", `'explain the loop'`, "explain the loop"},
		{"strip_backticks", "`refactor this`", "refactor this"},
		{"strip_nested_quotes", `""actually run it""`, "actually run it"},
		{"first_line_only", "look into it\nmore commentary", "look into it"},
		{"first_line_only_crlf", "commit now\r\nnothing here", "commit now"},
		{"strip_bullet", "- run tests", "run tests"},
		{"strip_asterisk", "* run tests", "run tests"},
		{"strip_arrow", "> run tests", "run tests"},
		{"cap_length", string(bytesRepeat('x', 100)), string(bytesRepeat('x', 80))},
		{"empty_stays_empty", "", ""},
		{"whitespace_only_becomes_empty", "   \n  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSuggestion(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeSuggestion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// bytesRepeat avoids pulling strings.Repeat into the test file just for the
// length-cap case.
func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
