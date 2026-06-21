package render

import (
	"strings"
)

// markdownTable describes one detected markdown table block within prose.
type markdownTable struct {
	StartLine int // inclusive
	EndLine   int // inclusive
	Headers   []string
	Rows      [][]string
}

// InterceptMarkdownTables scans the text line by line. Markdown table blocks
// (lines like `| a | b |` with a `| --- | --- |` separator row) are extracted
// into Table values and returned alongside the cleaned text with sentinel
// markers (`{{TABLE_N}}`) in their place. The caller substitutes rendered
// tables back in after rendering each Table at its width.
//
// Algorithmic — no markdown library, no LLM call. Handles the canonical
// pipe-delimited form most LLMs emit.
func InterceptMarkdownTables(text string) (cleaned string, tables []Table) {
	lines := strings.Split(text, "\n")
	var out []string

	i := 0
	for i < len(lines) {
		if mt, consumed := matchTable(lines, i); consumed > 0 {
			tables = append(tables, mt.toTable())
			out = append(out, "{{TABLE_"+itoa(len(tables)-1)+"}}")
			i += consumed
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return strings.Join(out, "\n"), tables
}

// matchTable tries to consume a markdown table starting at lines[i]. Returns
// the parsed block and the number of lines consumed (0 if no match).
func matchTable(lines []string, i int) (markdownTable, int) {
	if i+1 >= len(lines) {
		return markdownTable{}, 0
	}
	head := lines[i]
	sep := lines[i+1]
	if !looksLikePipeRow(head) || !looksLikeSeparator(sep) {
		return markdownTable{}, 0
	}
	mt := markdownTable{StartLine: i, Headers: splitPipeRow(head)}
	// Require separator column count to match header column count.
	if len(splitPipeRow(sep)) != len(mt.Headers) {
		return markdownTable{}, 0
	}
	j := i + 2
	for j < len(lines) && looksLikePipeRow(lines[j]) {
		row := splitPipeRow(lines[j])
		// Normalize row width to header count.
		for len(row) < len(mt.Headers) {
			row = append(row, "")
		}
		if len(row) > len(mt.Headers) {
			row = row[:len(mt.Headers)]
		}
		mt.Rows = append(mt.Rows, row)
		j++
	}
	mt.EndLine = j - 1
	if len(mt.Rows) == 0 {
		// Header + separator only — still a valid empty table; emit anyway.
	}
	return mt, j - i
}

func looksLikePipeRow(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") && strings.Count(t, "|") >= 2
}

// looksLikeSeparator matches `| --- | :---: | ... |`.
func looksLikeSeparator(s string) bool {
	if !looksLikePipeRow(s) {
		return false
	}
	cells := splitPipeRow(s)
	for _, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func splitPipeRow(s string) []string {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

func (mt markdownTable) toTable() Table {
	cols := make([]Column, len(mt.Headers))
	// Last column is the wrap-OK column by convention; LLMs usually put the
	// longest explanatory field at the end. Priority increments left-to-right
	// so right-most columns drop first.
	for i, h := range mt.Headers {
		cols[i] = Column{
			Name:      h,
			Priority:  i,
			Wrappable: i == len(mt.Headers)-1,
		}
	}
	rows := make([]map[string]string, len(mt.Rows))
	for i, r := range mt.Rows {
		m := map[string]string{}
		for j, h := range mt.Headers {
			if j < len(r) {
				m[h] = r[j]
			}
		}
		rows[i] = m
	}
	return Table{Cols: cols, Rows: rows}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
