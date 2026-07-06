package render

import "strings"

// DiffOp is the kind of a diff line.
type DiffOp int

const (
	DiffEqual  DiffOp = iota // unchanged (context)
	DiffDelete               // present in old, removed
	DiffInsert               // present in new, added
)

// DiffLine is one line of a computed line-level diff.
type DiffLine struct {
	Op   DiffOp
	Text string
}

// LineDiff computes a line-level diff of old → new via a longest-common-
// subsequence, returning the ordered equal/delete/insert operations. It is
// pure (no styling) and suited to small edit snippets (edit_file old_string →
// new_string, or a write_file body against ""). Line endings are normalized to
// \n; a trailing newline yields a trailing empty line, matched like any other.
func LineDiff(oldText, newText string) []DiffLine {
	a := splitDiffLines(oldText)
	b := splitDiffLines(newText)
	m, n := len(a), len(b)

	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]DiffLine, 0, m+n)
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{DiffEqual, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{DiffDelete, a[i]})
			i++
		default:
			out = append(out, DiffLine{DiffInsert, b[j]})
			j++
		}
	}
	for ; i < m; i++ {
		out = append(out, DiffLine{DiffDelete, a[i]})
	}
	for ; j < n; j++ {
		out = append(out, DiffLine{DiffInsert, b[j]})
	}
	return out
}

// splitDiffLines splits on \n after normalizing \r\n. Empty input yields no
// lines (so a diff against "" of a body is all inserts).
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
