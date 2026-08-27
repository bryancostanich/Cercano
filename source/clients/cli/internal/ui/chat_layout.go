package ui

import (
	"sort"
	"strings"
)

var blankRenderLine = []string{""}

type renderUnitKind int

const (
	unitEntry renderUnitKind = iota
	unitToolGroup
	unitSeparator
	unitTrailingActivity
)

type renderUnit struct {
	kind       renderUnitKind
	startEntry int
	endEntry   int
	startLine  int
	lineCount  int
	lines      []string
	dynamic    bool
	arrowRows  []arrowRow
}

type transcriptLayout struct {
	width      int
	stylesGen  int
	units      []renderUnit
	totalLines int
}

func (l transcriptLayout) flattenedContent() string {
	lines := l.flattenedLines()
	return strings.Join(lines, "\n")
}

func (l transcriptLayout) flattenedLines() []string {
	if len(l.units) == 0 {
		return nil
	}
	lines := make([]string, 0, l.totalLines)
	for _, u := range l.units {
		lines = append(lines, u.lines...)
	}
	return lines
}

func (l transcriptLayout) absoluteArrowRows() []arrowRow {
	var rows []arrowRow
	for _, u := range l.units {
		for _, r := range u.arrowRows {
			r.line += u.startLine
			rows = append(rows, r)
		}
	}
	return rows
}

func (l transcriptLayout) linesRange(top, height int) []string {
	if height <= 0 {
		return nil
	}
	out := make([]string, height)
	if len(l.units) == 0 || top >= l.totalLines {
		return out
	}
	if top < 0 {
		top = 0
	}
	bottom := top + height
	unitIdx := l.unitIndexAtLine(top)
	for unitIdx >= 0 && unitIdx < len(l.units) {
		u := l.units[unitIdx]
		if u.startLine >= bottom {
			break
		}
		unitEnd := u.startLine + u.lineCount
		if unitEnd <= top {
			unitIdx++
			continue
		}
		from := maxInt(top, u.startLine)
		to := minInt(bottom, unitEnd)
		copy(out[from-top:to-top], u.lines[from-u.startLine:to-u.startLine])
		unitIdx++
	}
	return out
}

func (l transcriptLayout) lineAt(line int) (string, bool) {
	idx := l.unitIndexAtLine(line)
	if idx < 0 {
		return "", false
	}
	u := l.units[idx]
	local := line - u.startLine
	if local < 0 || local >= len(u.lines) {
		return "", false
	}
	return u.lines[local], true
}

func (l transcriptLayout) arrowRowAt(line, x int) (arrowRow, bool) {
	idx := l.unitIndexAtLine(line)
	if idx < 0 {
		return arrowRow{}, false
	}
	u := l.units[idx]
	localLine := line - u.startLine
	var full arrowRow
	haveFull := false
	for _, r := range u.arrowRows {
		if r.line != localLine {
			continue
		}
		r.line += u.startLine
		if r.railMax > 0 {
			if x >= r.railMin && x < r.railMax {
				return r, true
			}
		} else {
			full = r
			haveFull = true
		}
	}
	if haveFull {
		return full, true
	}
	return arrowRow{}, false
}

func (l transcriptLayout) unitIndexAtLine(line int) int {
	if len(l.units) == 0 || line < 0 || line >= l.totalLines {
		return -1
	}
	idx := sort.Search(len(l.units), func(i int) bool {
		return l.units[i].startLine+l.units[i].lineCount > line
	})
	if idx >= len(l.units) {
		return -1
	}
	return idx
}

func splitRenderLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (c *chatView) rebuildTranscriptLayout(entries []*Entry) transcriptLayout {
	layout := transcriptLayout{width: c.Width(), stylesGen: c.stylesGen}
	if len(entries) > 0 {
		// Worst case alternates entry/separator for every entry plus an optional
		// trailing-activity separator. Preallocating avoids repeated growth while
		// rebuilding long cached transcripts.
		layout.units = make([]renderUnit, 0, len(entries)*2)
	}
	appendUnit := func(kind renderUnitKind, startEntry, endEntry int, lines []string, dynamic bool, rows []arrowRow) {
		if len(lines) == 0 {
			return
		}
		startLine := layout.totalLines
		var unitRows []arrowRow
		if len(rows) > 0 {
			unitRows = make([]arrowRow, len(rows))
			copy(unitRows, rows)
		}
		layout.units = append(layout.units, renderUnit{
			kind:       kind,
			startEntry: startEntry,
			endEntry:   endEntry,
			startLine:  startLine,
			lineCount:  len(lines),
			lines:      lines,
			dynamic:    dynamic,
			arrowRows:  unitRows,
		})
		layout.totalLines += len(lines)
	}
	appendSeparator := func() {
		// splitRenderLines intentionally returns nil for an empty block so empty
		// transcripts do not gain a phantom unit. Separators are real blank rows,
		// so append them explicitly.
		startLine := layout.totalLines
		layout.units = append(layout.units, renderUnit{kind: unitSeparator, startEntry: -1, endEntry: -1, startLine: startLine, lineCount: 1, lines: blankRenderLine})
		layout.totalLines++
	}

	first := true
	for i := 0; i < len(entries); {
		if !first {
			appendSeparator()
		}
		if entries[i].Tool != nil {
			j := i + 1
			for j < len(entries) && entries[j].Tool != nil {
				j++
			}
			_, lines, toolRows := c.renderToolGroupCachedLines(entries[i:j], i)
			rows := make([]arrowRow, 0, len(toolRows))
			for _, r := range toolRows {
				rows = append(rows, arrowRow{line: r.Line, entry: i + r.Entry, group: r.Group, railMin: r.RailMin, railMax: r.RailMax})
			}
			appendUnit(unitToolGroup, i, j, lines, groupIsDynamic(entries[i:j]), rows)
			i = j
		} else {
			seg, lines := c.renderEntryCachedLines(entries[i], i)
			rows := supersededArrowRows(entries[i], i, seg)
			dynamic := entries[i].Banner != nil || entries[i].Streaming
			appendUnit(unitEntry, i, i+1, lines, dynamic, rows)
			i++
		}
		first = false
	}
	naturalRows := layout.totalLines
	if !c.streaming {
		c.tailReserve = 0
		c.tailReserveBaseRows = 0
	} else if c.IsBetweenPhases() {
		wrapW := c.Width()
		if wrapW < 10 {
			wrapW = 10
		}
		textW := wrapW - entryIndent
		if textW < 8 {
			textW = 8
		}
		pad := strings.Repeat(" ", entryIndent)
		rows := 0
		if layout.totalLines > 0 {
			appendSeparator()
			rows++
		}
		lines := splitRenderLines(indentBlock(pad, c.renderTrailingActivity(textW)))
		rows += len(lines)
		c.tailReserve = rows
		c.tailReserveBaseRows = naturalRows
		appendUnit(unitTrailingActivity, -1, -1, lines, true, nil)
	} else if c.tailReserve > 0 {
		remaining := c.tailReserve - (naturalRows - c.tailReserveBaseRows)
		if remaining > 0 {
			startLine := layout.totalLines
			lines := make([]string, remaining)
			layout.units = append(layout.units, renderUnit{kind: unitSeparator, startEntry: -1, endEntry: -1, startLine: startLine, lineCount: remaining, lines: lines})
			layout.totalLines += remaining
		}
	}
	return layout
}

func supersededArrowRows(e *Entry, idx int, rendered string) []arrowRow {
	if e == nil || !e.Superseded {
		return nil
	}
	rows := []arrowRow{{line: 0, entry: idx}}
	if e.SupersededOpen {
		for ln := 1; ln <= strings.Count(rendered, "\n"); ln++ {
			rows = append(rows, arrowRow{line: ln, entry: idx, railMin: 0, railMax: toolRailContentCol})
		}
	}
	return rows
}
