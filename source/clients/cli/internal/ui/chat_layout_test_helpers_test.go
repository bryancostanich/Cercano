package ui

import (
	"fmt"
	"strings"
)

func chatLayoutContent(c *chatView) string {
	return strings.Join(c.layout.linesRange(0, c.TotalLineCount()), "\n")
}

func seedChatViewLines(c *chatView, lines []string) {
	cloned := append([]string(nil), lines...)
	c.layout = transcriptLayout{
		units: []renderUnit{{
			kind:       unitEntry,
			startLine:  0,
			lineCount:  len(cloned),
			startEntry: -1,
			endEntry:   -1,
			lines:      cloned,
		}},
		totalLines: len(cloned),
	}
	c.scroll.SetTotalLineCount(len(cloned))
	c.plainDirty = true
	c.plainLines = nil
	c.linkRows = collectLinkRowsFromLayout(c.layout)
}

func seedScrollableChat(c *chatView, n int) {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("chat %02d", i)
	}
	seedChatViewLines(c, lines)
}
