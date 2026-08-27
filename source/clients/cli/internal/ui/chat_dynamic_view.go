package ui

import "strings"

func (c *chatView) visibleLines(top, height int) []string {
	if height <= 0 {
		return nil
	}
	return c.layout.linesRange(top, height)
}

func (c *chatView) RefreshVisibleDynamicUnits() {
	if c.refreshVisibleDynamicUnits(c.YOffset(), c.Height()) {
		// A dynamic unit changed shape (usually because elapsed/status text wrapped
		// differently). Rebuild the layout once so offsets and following unit start
		// lines stay exact, then render the requested window from the rebuilt layout.
		c.rebuild()
	}
}

func (c *chatView) refreshVisibleDynamicUnits(top, height int) bool {
	if height <= 0 || len(c.layout.units) == 0 || top >= c.layout.totalLines {
		return false
	}
	if top < 0 {
		top = 0
	}
	bottom := top + height
	changedShape := false
	for i := range c.layout.units {
		u := &c.layout.units[i]
		if !u.dynamic {
			continue
		}
		if u.startLine >= bottom {
			break
		}
		if u.startLine+u.lineCount <= top {
			continue
		}
		lines, rows := c.renderDynamicUnit(u)
		if len(lines) == 0 {
			continue
		}
		if len(lines) != u.lineCount {
			changedShape = true
			continue
		}
		u.lines = lines
		if rows != nil {
			u.arrowRows = rows
		}
	}
	return changedShape
}

func (c *chatView) renderDynamicUnit(u *renderUnit) ([]string, []arrowRow) {
	switch u.kind {
	case unitEntry:
		if u.startEntry < 0 || u.startEntry >= len(c.entries) {
			return nil, nil
		}
		seg := c.renderEntry(c.entries[u.startEntry], u.startEntry)
		return splitRenderLines(seg), supersededArrowRows(c.entries[u.startEntry], u.startEntry, seg)
	case unitToolGroup:
		if u.startEntry < 0 || u.endEntry > len(c.entries) || u.startEntry >= u.endEntry {
			return nil, nil
		}
		_, lines, toolRows := c.renderToolGroupCachedLines(c.entries[u.startEntry:u.endEntry], u.startEntry)
		rows := make([]arrowRow, 0, len(toolRows))
		for _, r := range toolRows {
			rows = append(rows, arrowRow{line: r.Line, entry: u.startEntry + r.Entry, group: r.Group, railMin: r.RailMin, railMax: r.RailMax})
		}
		return lines, rows
	case unitTrailingActivity:
		wrapW := c.Width()
		if wrapW < 10 {
			wrapW = 10
		}
		textW := wrapW - entryIndent
		if textW < 8 {
			textW = 8
		}
		return splitRenderLines(indentBlock(strings.Repeat(" ", entryIndent), c.renderTrailingActivity(textW))), nil
	}
	return nil, nil
}
