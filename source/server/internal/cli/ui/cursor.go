package ui

import "strings"

// inputCursorRow returns the 0-based screen row at which parts[inputIdx]
// begins, accounting for embedded newlines in earlier parts. Used to place
// the real terminal cursor at the chat input line.
//
// Coordinate assumption: m.input.Cursor() returns X already including the
// prompt width (the textinput renders prompt + value into its own View string,
// and Cursor().X is relative to that rendered output). The input is rendered
// at column 0 of its line so no left-margin offset is added. Only Y needs
// lifting by inputCursorRow to reach absolute screen coordinates.
func inputCursorRow(parts []string, inputIdx int) int {
	row := 0
	for i := 0; i < inputIdx && i < len(parts); i++ {
		row += strings.Count(parts[i], "\n") + 1
	}
	return row
}
