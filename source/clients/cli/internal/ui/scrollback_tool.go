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
	StartedAt     time.Time     // exec-start wall clock; result blurb times against it
	Duration      time.Duration // exec-start → exec-complete; set at the same time ResultSummary is. Used to aggregate group timings.
	Folded        bool          // V1: always true; reserved for future expand/collapse
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

	// Status glyph and summary text are rendered separately so only the glyph
	// carries the success/error color — the result blurb reads as faint
	// secondary text, like the args. Block-coloring the whole status bit gave
	// a noisy "highlighted ✓ <text>" effect that fought the rest of the line.
	var statusBit string
	switch e.Status {
	case ToolStatusInProgress:
		statusBit = toolEntryFaint.Render("…")
	case ToolStatusComplete:
		statusBit = toolEntrySuccess.Render("✓") + toolEntryFaint.Render(" "+flattenSummary(e.ResultSummary))
	case ToolStatusError:
		statusBit = toolEntryError.Render("⚠") + toolEntryFaint.Render(" "+flattenSummary(e.ResultSummary))
	}

	// While the call is in progress, render the tool name in active-voice
	// present-participle form ("Reading" instead of "Read"). The verb form is
	// 1–2 chars wider than the noun, so the col padding floats accordingly —
	// in-progress entries only appear one at a time below the rolling group
	// summary, so misalignment versus completed entries (which use the noun
	// form) isn't a concern.
	displayName := e.ToolName
	if e.Status == ToolStatusInProgress {
		displayName = verbForInProgress(e.ToolName)
	}
	// Pad short tool names to a fixed column so the args lines up down the list.
	// Longer names (git_commit, git_reset_hard) overflow the column rather than
	// widening it for everyone — they're comparatively rare.
	const nameCol = 6
	name := displayName
	if pad := nameCol - lipgloss.Width(name); pad > 0 {
		name += strings.Repeat(" ", pad)
	}
	prefix := fmt.Sprintf("%s%s %s ", gutter, marker, name)
	// Width-aware args elision: if the full args summary won't leave room for
	// the right-aligned status, segment-elide (paths) or middle-elide so the
	// line fits one row. width<=0 disables this (no budget known).
	argsText := flattenSummary(e.ArgsSummary)
	if width > 0 {
		budget := width - lipgloss.Width(prefix) - lipgloss.Width(statusBit) - 3 // 3 = inter-column gap
		argsText = elideArgs(argsText, budget)
	}
	argsRender := toolEntryFaint.Render(argsText)
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

// renderToolGroup renders a contiguous run of ToolEntries as a "rolling
// consumption" group. Completed entries are summarized into a single line at
// the top; the in-progress entry (at most one — the model only fires one tool
// at a time today) renders standalone below the summary. The whole block
// counts as one entry in the scrollback's vertical rhythm.
//
// Layout examples:
//
//	1 completed, none in progress:
//	  ▸ 1 tool call (Read)                                          8ms ✓
//
//	3 completed, 1 in progress:
//	  ▸ 3 tool calls (2 Read, Edit)                                23ms ✓
//	  ▸ Editing  internal/server/server.go                         …
//
//	0 completed, 1 in progress:
//	  ▸ Reading  internal/meridian/manager.go                      …
//
// width is the same column budget renderToolEntry uses. styles + md are
// passed through to the per-call renderer for the active entry.
func renderToolGroup(entries []ToolEntry, width int, styles theme.Styles, md *render.Markdown) string {
	// Single-entry runs render directly — no summary, full per-entry detail.
	// Rolling-consumption folding only adds value when there's a meaningful
	// "many" to summarise; a "1 tool call" summary just hides the args and
	// result blurb without compressing anything.
	if len(entries) == 1 {
		e := entries[0]
		e.Folded = true
		return renderToolEntry(e, width, false, styles, md)
	}
	var completed []ToolEntry
	var active []ToolEntry
	for _, e := range entries {
		if e.Status == ToolStatusInProgress {
			active = append(active, e)
		} else {
			completed = append(completed, e)
		}
	}
	var lines []string
	if len(completed) > 0 {
		lines = append(lines, renderGroupSummary(completed, width, styles))
	}
	for _, e := range active {
		// In-progress entries always render expanded — they are the live row.
		// focused=false; group focus is Phase C.
		e.Folded = true
		lines = append(lines, renderToolEntry(e, width, false, styles, md))
	}
	return strings.Join(lines, "\n")
}

// renderGroupSummary builds the one-line summary for the completed members of
// a tool group. Format mirrors a per-call line so the group reads as a member
// of the same visual family:
//
//	▸ <count_label>  <breakdown>                       <timing> <glyph>
//
// where:
//   - count_label is "N tool call" / "N tool calls" (singular handled)
//   - breakdown lists tool types in first-seen order with prefix counts:
//     "(3 Read, 2 Edit, Bash, Write)". Tools that appeared once carry no
//     prefix count.
//   - glyph is ✓ when all completed are Complete, ⚠ when any errored.
//   - The label/breakdown gets faint styling matching the args column; the
//     timing is faint too; only the glyph carries the success/error color.
//
// width drives right-alignment of the timing column; an over-budget summary
// falls back to inline rendering on a single line.
func renderGroupSummary(completed []ToolEntry, width int, styles theme.Styles) string {
	if len(completed) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := []string{}
	var total time.Duration
	anyErr := false
	for _, e := range completed {
		if e.Status == ToolStatusError {
			anyErr = true
		}
		total += e.Duration
		name := groupBreakdownName(e.ToolName)
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, n := range order {
		if counts[n] > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[n], n))
		} else {
			parts = append(parts, n)
		}
	}
	label := fmt.Sprintf("%d tool calls", len(completed))
	if len(completed) == 1 {
		label = "1 tool call"
	}
	breakdown := ""
	if len(parts) > 0 {
		breakdown = " (" + strings.Join(parts, ", ") + ")"
	}

	toolEntryFaint := lipgloss.NewStyle().Faint(true)
	glyph := "✓"
	glyphStyle := styles.ToolSuccess
	if anyErr {
		glyph = "⚠"
		glyphStyle = styles.ToolError
	}
	// 2-space gutter matches renderToolEntry's unfocused gutter, so summary
	// and per-call lines share the same left margin.
	gutter := "  "
	left := gutter + "▸ " + label + toolEntryFaint.Render(breakdown)
	timing := formatDur(total)
	rightPlain := timing + " " + glyph
	statusStyled := toolEntryFaint.Render(timing+" ") + glyphStyle.Render(glyph)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(rightPlain)
	if width > 0 && leftW+1+rightW <= width {
		pad := strings.Repeat(" ", width-leftW-rightW)
		return left + pad + statusStyled
	}
	return left + "   " + statusStyled
}

// groupBreakdownName humanizes a tool name for the group breakdown ("Read",
// "Edit", "Bash"). Falls back to the original name for unknown / non-builtin
// tools so MCP-supplied tools surface intact.
func groupBreakdownName(s string) string {
	switch strings.ToLower(s) {
	case "read":
		return "Read"
	case "write":
		return "Write"
	case "edit":
		return "Edit"
	case "bash":
		return "Bash"
	case "grep":
		return "Grep"
	case "glob":
		return "Glob"
	case "ls":
		return "LS"
	}
	return s
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
