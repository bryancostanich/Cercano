package ui

import "strings"

// renderTranscriptPrefix returns the assembled render for entries[:end]. The
// result is cached because, during streaming, this prefix is immutable while the
// final assistant/tool entry changes many times per second.
func (c *chatView) renderTranscriptPrefix(entries []*Entry, end int) transcriptPrefixCache {
	if end <= 0 {
		return transcriptPrefixCache{width: c.vp.Width(), stylesGen: c.stylesGen, contentGen: c.contentGen}
	}
	if end > len(entries) {
		end = len(entries)
	}
	pc := c.transcriptPrefix
	if pc.end == end && pc.width == c.vp.Width() && pc.stylesGen == c.stylesGen && pc.contentGen == c.contentGen {
		return pc
	}

	var b strings.Builder
	rows := make([]arrowRow, 0, len(c.arrowRows))
	nl := 0
	first := true
	for i := 0; i < end; {
		if !first {
			b.WriteString("\n\n")
			nl += 2
		}
		if entries[i].Tool != nil {
			j := i + 1
			for j < end && entries[j].Tool != nil {
				j++
			}
			block, toolRows := c.renderToolGroupCached(entries[i:j], i)
			for _, r := range toolRows {
				rows = append(rows, arrowRow{line: nl + r.Line, entry: i + r.Entry, group: r.Group, railMin: r.RailMin, railMax: r.RailMax})
			}
			b.WriteString(block)
			nl += strings.Count(block, "\n")
			i = j
		} else {
			seg := c.renderEntryCached(entries[i], i)
			if entries[i].Superseded {
				rows = append(rows, arrowRow{line: nl, entry: i})
				if entries[i].SupersededOpen {
					for ln := 1; ln <= strings.Count(seg, "\n"); ln++ {
						rows = append(rows, arrowRow{line: nl + ln, entry: i, railMin: 0, railMax: toolRailContentCol})
					}
				}
			}
			b.WriteString(seg)
			nl += strings.Count(seg, "\n")
			i++
		}
		first = false
	}
	pc = transcriptPrefixCache{
		width:      c.vp.Width(),
		stylesGen:  c.stylesGen,
		contentGen: c.contentGen,
		end:        end,
		content:    b.String(),
		arrowRows:  rows,
		lineCount:  nl,
	}
	c.transcriptPrefix = pc
	return pc
}
