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
	// Drop order when the table is too narrow (lowest Priority drops first):
	//   - The FIRST column is the key/identifier — usually the most important —
	//     so it gets the highest Priority and is dropped last (only when it's
	//     the sole survivor, after which the table transposes to key:value).
	//   - The LAST column is the wrap-OK explanatory field (LLMs put the long
	//     text there); keep it above the middle columns.
	//   - MIDDLE columns drop first.
	last := len(mt.Headers) - 1
	for i, h := range mt.Headers {
		prio := i // middle columns: low priority, drop first
		switch {
		case i == 0:
			prio = len(mt.Headers) + 1 // key column: drop last
		case i == last:
			prio = len(mt.Headers) // description column: keep over middles
		}
		cols[i] = Column{
			Name:      h,
			Priority:  prio,
			Wrappable: i == last,
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
