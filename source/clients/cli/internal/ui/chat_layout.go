package ui

import "strings"

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
	arrowRows  []arrowRow
}

type transcriptLayout struct {
	width      int
	stylesGen  int
	contentGen int
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

func splitRenderLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (c *chatView) rebuildTranscriptLayout(entries []*Entry) transcriptLayout {
	layout := transcriptLayout{width: c.vp.Width(), stylesGen: c.stylesGen, contentGen: c.contentGen}
	appendUnit := func(kind renderUnitKind, startEntry, endEntry int, block string, rows []arrowRow) {
		lines := splitRenderLines(block)
		if len(lines) == 0 {
			return
		}
		startLine := layout.totalLines
		unitRows := make([]arrowRow, len(rows))
		copy(unitRows, rows)
		layout.units = append(layout.units, renderUnit{
			kind:       kind,
			startEntry: startEntry,
			endEntry:   endEntry,
			startLine:  startLine,
			lineCount:  len(lines),
			lines:      lines,
			arrowRows:  unitRows,
		})
		layout.totalLines += len(lines)
	}
	appendSeparator := func() {
		// splitRenderLines intentionally returns nil for an empty block so empty
		// transcripts do not gain a phantom unit. Separators are real blank rows,
		// so append them explicitly.
		startLine := layout.totalLines
		layout.units = append(layout.units, renderUnit{kind: unitSeparator, startEntry: -1, endEntry: -1, startLine: startLine, lineCount: 1, lines: []string{""}})
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
			block, toolRows := c.renderToolGroupCached(entries[i:j], i)
			rows := make([]arrowRow, 0, len(toolRows))
			for _, r := range toolRows {
				rows = append(rows, arrowRow{line: r.Line, entry: i + r.Entry, group: r.Group, railMin: r.RailMin, railMax: r.RailMax})
			}
			appendUnit(unitToolGroup, i, j, block, rows)
			i = j
		} else {
			seg := c.renderEntryCached(entries[i], i)
			rows := supersededArrowRows(entries[i], i, seg)
			appendUnit(unitEntry, i, i+1, seg, rows)
			i++
		}
		first = false
	}
	if c.IsBetweenPhases() {
		wrapW := c.vp.Width()
		if wrapW < 10 {
			wrapW = 10
		}
		textW := wrapW - entryIndent
		if textW < 8 {
			textW = 8
		}
		pad := strings.Repeat(" ", entryIndent)
		if layout.totalLines > 0 {
			appendSeparator()
		}
		appendUnit(unitTrailingActivity, -1, -1, indentBlock(pad, c.renderTrailingActivity(textW)), nil)
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
