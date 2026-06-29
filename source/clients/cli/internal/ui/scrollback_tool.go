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
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
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
	StartedAt     time.Time // exec-start wall clock; result blurb times against it
	Folded        bool      // V1: always true; reserved for future expand/collapse
}

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
func renderToolEntry(e ToolEntry, width int, focused bool, styles theme.Styles, md *render.Markdown) string {
	toolEntryFaint := lipgloss.NewStyle().Faint(true)
	toolEntrySuccess := styles.ToolSuccess
	toolEntryError := styles.ToolError

	marker := "▸"
	if !e.Folded {
		marker = "▾"
	}

	gutter := "  "
	if focused {
		gutter = styles.ToolFocus.Render("▶ ")
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

	// Pad short tool names to a fixed column so the args lines up down the list.
	// Longer names (git_commit, git_reset_hard) overflow the column rather than
	// widening it for everyone — they're comparatively rare.
	const nameCol = 6
	name := e.ToolName
	if pad := nameCol - lipgloss.Width(name); pad > 0 {
		name += strings.Repeat(" ", pad)
	}
	prefix := fmt.Sprintf("%s%s %s ", gutter, marker, name)
	argsRender := toolEntryFaint.Render(flattenSummary(e.ArgsSummary))
	left := prefix + argsRender

	// Right-align the status/timing to the right edge when the whole entry fits
	// on one line — name+args read left-to-right, the ✓/timing column lines up
	// down the right margin. Needs at least one space between args and status.
	rightAligned := ""
	if width > 0 {
		leftW := lipgloss.Width(left)
		statusW := lipgloss.Width(statusBit)
		if leftW+1+statusW <= width {
			rightAligned = left + strings.Repeat(" ", width-leftW-statusW) + statusBit
		}
	}

	content := fmt.Sprintf("%s   %s", argsRender, statusBit)
	line := prefix + content

	if e.Folded {
		if rightAligned != "" {
			return rightAligned
		}
		// Too long for one line: wrap the ARGS (ANSI-aware) to the available
		// width, hang-indenting continuation lines under the content (past
		// "▸ <tool> "), then right-align the status on the last line — or a fresh
		// line if it won't fit — so the ✓/timing column stays flush right like the
		// un-wrapped entries. ansi.Wrap breaks on spaces and hard-breaks tokens
		// longer than the limit (paths, JSON).
		hang := lipgloss.Width(prefix)
		avail := width - hang
		if width > 0 && avail >= 8 {
			wrapped := strings.Split(ansi.Wrap(argsRender, avail, ""), "\n")
			for i := range wrapped {
				if i == 0 {
					wrapped[i] = prefix + wrapped[i]
				} else {
					wrapped[i] = strings.Repeat(" ", hang) + wrapped[i]
				}
			}
			statusW := lipgloss.Width(statusBit)
			last := len(wrapped) - 1
			if lastW := lipgloss.Width(wrapped[last]); lastW+1+statusW <= width {
				wrapped[last] += strings.Repeat(" ", width-lastW-statusW) + statusBit
			} else {
				pad := width - statusW
				if pad < hang {
					pad = hang
				}
				wrapped = append(wrapped, strings.Repeat(" ", pad)+statusBit)
			}
			return strings.Join(wrapped, "\n")
		}
		return line
	}

	first := line
	if rightAligned != "" {
		first = rightAligned
	}
	body := []string{first}
	if e.FullArgs != "" {
		body = append(body, toolEntryFaint.Render("    args: "+e.FullArgs))
	}
	if e.FullResult != "" {
		// Render the result by type (JSON pretty-printed, markdown rendered, raw
		// verbatim) through the same path /c uses, indented under the entry.
		bw := width - 4
		if bw < 8 {
			bw = 8
		}
		for _, l := range renderToolBody(e.FullResult, "", md, bw) {
			body = append(body, "    "+l)
		}
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
