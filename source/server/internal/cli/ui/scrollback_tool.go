// Package ui — folded tool-call scrollback entries.
//
// Tool calls in scrollback get a different visual treatment than text turns:
// a single folded line per call with an arrow marker, the tool name, a short
// args summary, and a status emoji. Mirrors Claude Code / Codex UX so the
// user can scan the turn at a glance.
//
// V1 renders folded only; expand/collapse via tab-focus is a follow-up.
package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/server/internal/cli/theme"
)

// ToolStatus enumerates the lifecycle states of a tool call as it appears in
// scrollback. Drives the status glyph + color in renderToolEntry.
type ToolStatus int

const (
	// ToolStatusInProgress — the model has emitted the tool_use block but the
	// server-side execution hasn't completed yet. Renders a faint "…".
	ToolStatusInProgress ToolStatus = iota
	// ToolStatusComplete — the tool finished successfully. Renders a lime ✓
	// with the result summary.
	ToolStatusComplete
	// ToolStatusError — the tool returned an error. Renders a red ⚠ with the
	// error summary.
	ToolStatusError
)

// ToolEntry is one tool-call line in the scrollback. Lives inside an Entry
// alongside the text-turn fields so the renderer can dispatch by Entry.Tool
// being non-nil.
type ToolEntry struct {
	ToolUseID     string // stream id; used to find the entry on stop / complete
	ToolName      string
	ArgsSummary   string // short one-liner shown next to the name in folded view
	FullArgs      string // full JSON; shown when expanded
	FullResult    string // full result body; shown when expanded
	ResultSummary string // short result blurb shown next to ✓ in folded view
	Status        ToolStatus
	Folded        bool // V1: always true; reserved for future expand/collapse
}

// Package-level styles so renderToolEntry doesn't need a palette parameter.
// Initialized from the active palette (Cracker) which matches what New() uses.
var (
	toolEntryFaint    = lipgloss.NewStyle().Faint(true)
	toolEntrySuccess  = lipgloss.NewStyle().Foreground(theme.Cracker().Accent) // lime ✓
	toolEntryError    = lipgloss.NewStyle().Foreground(theme.Cracker().Error)  // red ⚠
)

// renderToolEntry produces the scrollback text for one tool call.
//
// Folded view (one line):
//
//	▸ <tool>  <args summary>     <status glyph> <result summary>
//
// Expanded view (multi-line):
//
//	▾ <tool>  <args summary>     <status glyph> <result summary>
//	  args: <full args>
//	  <full result, indented>
//
// width is reserved for future wrapping; V1 does not wrap because the args
// summary + result summary are pre-trimmed by the caller.
//
// focused renders a left-margin caret in the accent color so the user can see
// which entry the up/down nav cursor is currently on. When false, a two-space
// gutter holds the slot so toggling fold doesn't shift the body horizontally.
func renderToolEntry(e ToolEntry, width int, focused bool) string {
	_ = width // reserved for follow-up: wrap long lines to terminal width

	marker := "▸"
	if !e.Folded {
		marker = "▾"
	}

	gutter := "  "
	if focused {
		gutter = lipgloss.NewStyle().Foreground(theme.Cracker().Accent).Render("▶ ")
	}

	var statusBit string
	switch e.Status {
	case ToolStatusInProgress:
		statusBit = toolEntryFaint.Render("…")
	case ToolStatusComplete:
		statusBit = toolEntrySuccess.Render("✓ " + flattenSummary(e.ResultSummary))
	case ToolStatusError:
		statusBit = toolEntryError.Render("⚠ " + flattenSummary(e.ResultSummary))
	}

	line := fmt.Sprintf("%s%s %s %s   %s",
		gutter, marker, e.ToolName, toolEntryFaint.Render(flattenSummary(e.ArgsSummary)), statusBit)

	if e.Folded {
		return line
	}

	body := []string{line}
	if e.FullArgs != "" {
		body = append(body, toolEntryFaint.Render("    args: "+e.FullArgs))
	}
	if e.FullResult != "" {
		body = append(body, "    "+indentToolBody(e.FullResult, "    "))
	}
	return strings.Join(body, "\n")
}

// flattenSummary collapses a tool summary to a single line: newlines, tabs and
// runs of whitespace become single spaces. The folded tool entry is one line,
// so an embedded newline (e.g. a Bash result's "$ cmd\n[exit=...]") would
// otherwise leak a second, un-indented fragment into scrollback.
func flattenSummary(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// indentToolBody prefixes every line after the first in s with prefix.
// Distinct name from the existing indentBlock helper in model.go to avoid
// any future confusion — that one prefixes ALL lines (including the first).
func indentToolBody(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}
