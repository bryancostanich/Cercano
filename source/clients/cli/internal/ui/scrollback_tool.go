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
	"encoding/json"
	"fmt"
	"strconv"
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
	// StartLine is the 1-based line in the target file where an edit/write
	// began — recorded agent-side at execute time and carried on the
	// tool_exec_complete event / GetToolCall detail. Numbers the expanded
	// args diff. 0 = unknown (in-flight, non-file tools, pre-upgrade
	// history) → the diff renders unnumbered.
	StartLine  int
	Status     ToolStatus
	StartedAt  time.Time     // exec-start wall clock; result blurb times against it
	Duration   time.Duration // exec-start → exec-complete; set at the same time ResultSummary is. Used to aggregate group timings.
	Folded     bool          // one-line folded view vs. expanded args+result body
	Loading    bool          // a lazy GetToolCall fetch for the expanded body is in flight
	SubAgentID string        // child conversation spawned by dispatch/workflow, if any
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
	toolEntrySubtle := styles.Muted
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
		// Animated braille spinner makes "this is running" unambiguous — a
		// static `…` reads as "stalled" once you've seen tool entries flash
		// past for a few turns. Faint-styled to stay in the same visual
		// register as the args column. Elapsed time appears alongside once
		// the call has been running >= 1s; instant tools never expose a
		// noisy "0s".
		spin := animateToolSpinnerWithStyle(toolEntrySubtle)
		extra := ""
		if !e.StartedAt.IsZero() {
			if d := time.Since(e.StartedAt); d >= time.Second {
				extra = toolEntrySubtle.Render(" · " + d.Round(time.Second).String())
			}
		}
		statusBit = spin + extra
	case ToolStatusComplete:
		statusBit = toolEntrySuccess.Render("✓") + toolEntrySubtle.Render(" "+flattenSummary(e.ResultSummary))
	case ToolStatusError:
		statusBit = toolEntryError.Render("⚠") + toolEntrySubtle.Render(" "+flattenSummary(e.ResultSummary))
	}
	if e.SubAgentID != "" {
		// "open tab" is an interactive affordance (click to open the sub-agent
		// tab), so color the words in the accent — the same color as the nav
		// caret — to set them apart from the faint result text. The separator
		// stays faint so only the actionable words pop.
		statusBit += toolEntrySubtle.Render(" · ") + styles.Accent.Render("open tab")
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
	prefix := gutter + styles.Primary.Render(fmt.Sprintf("%s %s ", marker, name))
	// Width-aware args elision: if the full args summary won't leave room for
	// the right-aligned status, segment-elide (paths) or middle-elide so the
	// line fits one row. width<=0 disables this (no budget known).
	argsText := flattenSummary(e.ArgsSummary)
	if width > 0 {
		budget := width - lipgloss.Width(prefix) - lipgloss.Width(statusBit) - 3 // 3 = inter-column gap
		argsText = elideArgs(argsText, budget)
	}
	argsRender := toolEntrySubtle.Render(argsText)
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
	// Lazy fetch in flight: show an animated spinner so the expand has feedback
	// even if the store read stalls. The wall-clock spinner animates as long as
	// the host keeps ticking (hasLoadingTool gates that).
	if e.Loading {
		// Keep the "    " indent PLAIN (outside the style) so the rail overlay,
		// which replaces the byte at offset 2, finds a real space there.
		body = append(body, "    "+toolEntrySubtle.Render(animateToolSpinnerWithStyle(toolEntrySubtle)+" loading…"))
		railBody(body, styles)
		return strings.Join(body, "\n")
	}
	if e.FullArgs != "" {
		// Edits/writes render as a formatted +/- diff; other tools show the
		// raw args JSON.
		if diff := renderToolArgsDiff(e.ToolName, e.FullArgs, e.StartLine, width, styles); diff != nil {
			body = append(body, diff...)
		} else {
			// Plain indent + styled text (see the loading branch) so the rail
			// overlay lands on a real space at offset 2. The args JSON can be
			// arbitrarily long, so wrap it (ANSI-aware, hard-breaking long
			// tokens) to the body width — an unwrapped line would overflow and
			// wrap at column 0, left of the rail.
			aw := width - 4
			if aw < 8 {
				aw = 8
			}
			argsBody := toolEntrySubtle.Render("args: " + expandTabs(e.FullArgs))
			for _, l := range strings.Split(ansi.Wrap(argsBody, aw, ""), "\n") {
				body = append(body, "    "+l)
			}
		}
	}
	if e.FullResult != "" {
		// File contents get syntax-highlighted (language inferred from the read
		// path); other results fall back to JSON/markdown/plain sniffing.
		bw := width - 4
		if bw < 8 {
			bw = 8
		}
		for _, l := range renderToolResultBody(e.ToolName, e.FullArgs, e.FullResult, md, bw) {
			body = append(body, "    "+l)
		}
	}
	// Fetched but nothing recorded (or the fetch failed): say so rather than
	// render a bare header with an empty body.
	if e.FullArgs == "" && e.FullResult == "" {
		body = append(body, "    "+toolEntrySubtle.Render("(no details)"))
	}
	railBody(body, styles)
	return strings.Join(body, "\n")
}

// renderToolArgsDiff renders a formatted +/- diff for edit/write tool args,
// parsed from the tool_use input JSON. Returns nil for tools that aren't
// edits/writes, so the caller falls back to the raw args line. startLine is
// the agent-recorded 1-based line where the change begins (0 = unknown →
// unnumbered diff).
func renderToolArgsDiff(toolName, argsJSON string, startLine, width int, styles theme.Styles) []string {
	switch strings.ToLower(toolName) {
	case "edit", "edit_file":
		var a struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Path == "" {
			return nil
		}
		return renderDiffBlock(a.Path, render.LineDiff(a.OldString, a.NewString), startLine, width, styles)
	case "write", "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Path == "" {
			return nil
		}
		return renderDiffBlock(a.Path, render.LineDiff("", a.Content), startLine, width, styles)
	}
	return nil
}

// renderDiffBlock renders a diff (a faint path header plus colored +/- and
// context lines) under a tool entry. Lines are truncated to the width budget.
// startLine >= 1 adds a faint right-aligned line-number gutter: deleted lines
// carry old-file numbers, inserted and context lines carry new-file numbers,
// both counters seeded at startLine. startLine == 0 renders unnumbered.
func renderDiffBlock(path string, ops []render.DiffLine, startLine, width int, styles theme.Styles) []string {
	faint := styles.Muted
	const indent = "    "
	gutterW := 0
	if startLine > 0 {
		// Widest number the block can print: every op advances at most one
		// of the counters, so startLine+len(ops) bounds both.
		gutterW = len(strconv.Itoa(startLine + len(ops)))
	}
	budget := width - 6 - gutterW // indent (4) + "+ " / "- " prefix (2) + gutter
	if gutterW > 0 {
		budget-- // space between number and prefix
	}
	if budget < 8 {
		budget = 8
	}
	out := make([]string, 0, len(ops)+1)
	out = append(out, indent+faint.Render(path))
	oldN, newN := startLine, startLine
	for _, op := range ops {
		var prefix string
		var st lipgloss.Style
		var n int
		switch op.Op {
		case render.DiffInsert:
			prefix, st = "+ ", styles.ToolSuccess
			n = newN
			newN++
		case render.DiffDelete:
			prefix, st = "- ", styles.ToolError
			n = oldN
			oldN++
		default:
			prefix, st = "  ", faint
			n = newN
			oldN++
			newN++
		}
		gutter := ""
		if gutterW > 0 {
			gutter = faint.Render(fmt.Sprintf("%*d ", gutterW, n))
		}
		out = append(out, indent+gutter+st.Render(prefix+ansi.Truncate(expandTabs(op.Text), budget, "…")))
	}
	return out
}

// groupRenderOpts controls renderToolGroup's behavior. The zero value renders
// a collapsed multi-entry group with no focus.
type groupRenderOpts struct {
	// Expanded forces per-entry rendering even when len(entries) > 1: each
	// entry shows as its own per-call line (respecting that entry's Folded
	// state), instead of being folded into the rolling-consumption summary.
	Expanded bool
	// Focused draws the accent-colored ▶ on the summary line's gutter when
	// the group is collapsed (a sibling of renderToolEntry's focused gutter).
	Focused bool
	// FocusedIdx is the index within entries of the focused entry, used when
	// rendering per-entry (single-entry "group" or Expanded). -1 = no focus.
	FocusedIdx int
}

// renderToolGroup renders a contiguous run of ToolEntries as a "rolling
// consumption" group, or as per-entry per-call lines when opts.Expanded (or
// when entries is a single-entry run, since folding a single entry compresses
// nothing).
//
// Layout examples (collapsed multi-entry):
//
//	3 completed, 0 in progress:
//	  ▸ 3 tool calls (2 Read, Edit)                                23ms ✓
//
//	2 completed, 1 in progress:
//	  ▸ 2 tool calls (2 Read)                                      12ms ✓
//	  ▸ Editing  internal/server/server.go                         …
//
//	0 completed, 1 in progress:
//	  ▸ Reading  internal/meridian/manager.go                      …
//
// width is the same column budget renderToolEntry uses.
func renderToolGroup(entries []ToolEntry, width int, styles theme.Styles, md *render.Markdown, opts groupRenderOpts) string {
	s, _ := renderToolGroupSpans(entries, width, styles, md, opts)
	return s
}

// toolArrowRow marks one clickable fold-toggle row inside a rendered tool
// group block. Line is relative to the block's first line; Entry indexes the
// entries slice passed in. Rows are recorded while the lines are emitted, so
// the mapping is correct by construction — click handling must never
// re-derive (or glyph-sniff) the rendered layout after the fact.
// railBody overlays the collapse rail on an expanded entry's body: a faint │
// under the arrow (byte offset 2, inside the "    " indent) on every line after
// the header, turning into a ╰ hook on the last line. The arrow at the top plus
// this rail bracket the expanded region, so it can be collapsed by clicking the
// rail anywhere down its length (see MouseToggleFold) — not just the far-up
// arrow. No-op on lines whose offset-2 byte isn't the expected plain space.
func railBody(body []string, styles theme.Styles) {
	for k := 1; k < len(body); k++ {
		g := "│"
		if k == len(body)-1 {
			g = "╰"
		}
		body[k] = overlayRail(body[k], 2, g, styles)
	}
}

// overlayRail replaces the plain space at byte offset col of line with a faint
// rail glyph, leaving the rest intact. No-op when that byte isn't a space, so it
// never corrupts styled content — used to draw the collapse rail (both the
// per-entry and the outer group rail) without re-plumbing the render functions.
func overlayRail(line string, col int, glyph string, styles theme.Styles) string {
	if len(line) > col && line[col] == ' ' {
		return line[:col] + styles.Muted.Render(glyph) + line[col+1:]
	}
	return line
}

type toolArrowRow struct {
	Line  int
	Entry int
	Group bool // true = collapse/expand the whole run; false = toggle this entry's body
	// When RailMax > 0 this row is a rail: it claims a click only within
	// [RailMin, RailMax) (a bounded left-gutter zone). RailMax == 0 marks a
	// full-width toggle row (the arrow/header) that claims any x on its line.
	RailMin int
	RailMax int
}

// Absolute click columns for tool collapse rails, = block columns plus the
// entryIndent pad renderToolGroupBlock applies. A standalone entry's rail (and
// a group's outer rail) claims the left gutter [0, toolRailContentCol); a nested
// entry's own rail claims [toolRailContentCol, toolRailNestedContentCol).
const (
	toolRailContentCol       = 6
	toolRailNestedContentCol = 8
)

// railRows emits rail toolArrowRows for the body lines of seg (every line after
// its header), targeting entry (or the whole group when group is true) and
// claiming clicks in [railMin, railMax). startLine is seg's first line within
// the block.
func railRows(seg string, startLine, entry int, group bool, railMin, railMax int) []toolArrowRow {
	n := strings.Count(seg, "\n") + 1
	rows := make([]toolArrowRow, 0, n-1)
	for ln := 1; ln < n; ln++ {
		rows = append(rows, toolArrowRow{Line: startLine + ln, Entry: entry, Group: group, RailMin: railMin, RailMax: railMax})
	}
	return rows
}

// renderToolGroupSpans is renderToolGroup plus the arrow-row map.
func renderToolGroupSpans(entries []ToolEntry, width int, styles theme.Styles, md *render.Markdown, opts groupRenderOpts) (string, []toolArrowRow) {
	// Single-entry "group": folding compresses nothing, so the one call renders
	// as its own per-call line with an entry-level toggle. There is no group
	// header — nothing to collapse.
	if len(entries) == 1 {
		seg := renderToolEntry(entries[0], width, opts.FocusedIdx == 0, styles, md)
		rows := []toolArrowRow{{Line: 0, Entry: 0, Group: false}}
		if !entries[0].Folded {
			// Standalone entry: the whole left gutter collapses it.
			rows = append(rows, railRows(seg, 0, 0, false, 0, toolRailContentCol)...)
		}
		return seg, rows
	}

	// Expanded multi-entry run: a clickable summary header (▾) that collapses
	// the whole run, then each call nested one level in with its own
	// entry-level toggle (▸/▾) for that call's args+result body.
	if opts.Expanded {
		lines := make([]string, 0, len(entries)+1)
		rows := make([]toolArrowRow, 0, len(entries)+1)
		header := renderGroupSummary(entries, width, styles, opts.Focused, true)
		lines = append(lines, header)
		rows = append(rows, toolArrowRow{Line: 0, Entry: 0, Group: true})
		cursor := strings.Count(header, "\n") + 1
		for i, e := range entries {
			seg := indentBlock("  ", renderToolEntry(e, width-2, i == opts.FocusedIdx, styles, md))
			rows = append(rows, toolArrowRow{Line: cursor, Entry: i, Group: false})
			if !e.Folded {
				// Nested entry: its own rail is one level in, [content, nested).
				rows = append(rows, railRows(seg, cursor, i, false, toolRailContentCol, toolRailNestedContentCol)...)
			}
			lines = append(lines, seg)
			cursor += strings.Count(seg, "\n") + 1
		}
		// Outer group rail: a │ down every line below the header (block col 2,
		// aligned under the summary ▾), ╰ hook on the last line, with a
		// group-collapse click zone in the far-left gutter [0, content).
		blockLines := strings.Split(strings.Join(lines, "\n"), "\n")
		last := len(blockLines) - 1
		for ln := 1; ln <= last; ln++ {
			g := "│"
			if ln == last {
				g = "╰"
			}
			blockLines[ln] = overlayRail(blockLines[ln], 2, g, styles)
			rows = append(rows, toolArrowRow{Line: ln, Entry: 0, Group: true, RailMin: 0, RailMax: toolRailContentCol})
		}
		return strings.Join(blockLines, "\n"), rows
	}

	// Collapsed multi-entry run: rolling-consumption summary (completed calls
	// folded into one line) plus any in-progress live rows below it.
	var completed []ToolEntry
	var activeIdx []int
	for i, e := range entries {
		if e.Status == ToolStatusInProgress {
			activeIdx = append(activeIdx, i)
		} else {
			completed = append(completed, e)
		}
	}
	var lines []string
	var rows []toolArrowRow
	if len(completed) > 0 {
		// The summary header (Group row) toggles the whole run open. Count the
		// whole run — completed plus any in-flight call rendered live below —
		// so the header doesn't under-report ("1 tool call" while a second is
		// still running). renderGroupSummary shows a faint "…" instead of ✓
		// while any member is in progress, so the count doesn't imply success.
		rows = append(rows, toolArrowRow{Line: 0, Entry: 0, Group: true})
		lines = append(lines, renderGroupSummary(entries, width, styles, opts.Focused, false))
	}
	for _, i := range activeIdx {
		e := entries[i]
		// In-progress entries always render folded — they are the live row.
		// No per-entry focus in collapsed view; focus belongs to the group.
		e.Folded = true
		rows = append(rows, toolArrowRow{Line: len(lines), Entry: i, Group: false})
		lines = append(lines, renderToolEntry(e, width, false, styles, md))
	}
	return strings.Join(lines, "\n"), rows
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
func renderGroupSummary(completed []ToolEntry, width int, styles theme.Styles, focused, expanded bool) string {
	if len(completed) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := []string{}
	var total time.Duration
	anyErr := false
	anyActive := false
	for _, e := range completed {
		switch e.Status {
		case ToolStatusError:
			anyErr = true
		case ToolStatusInProgress:
			anyActive = true
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

	toolEntryFaint := styles.Muted
	glyph := "✓"
	glyphStyle := styles.ToolSuccess
	switch {
	case anyErr:
		glyph = "⚠"
		glyphStyle = styles.ToolError
	case anyActive:
		// A member is still running — don't claim success. Faint "…" reads as
		// "in progress"; the live row below carries the animated spinner.
		glyph = "…"
		glyphStyle = toolEntryFaint
	}
	// 2-space gutter matches renderToolEntry's unfocused gutter, so summary
	// and per-call lines share the same left margin. When the group is
	// focused (keyboard nav landed in it), swap for the accent ▶ marker —
	// same scheme renderToolEntry uses for per-entry focus.
	gutter := "  "
	if focused {
		gutter = styles.ToolFocus.Render("▶ ")
	}
	// ▾ when the run is expanded (header collapses it on click), ▸ when the
	// run is collapsed (summary expands it on click).
	marker := "▸"
	if expanded {
		marker = "▾"
	}
	left := gutter + styles.Primary.Render(marker+" "+label) + toolEntryFaint.Render(breakdown)
	timing := formatDur(total)
	rightPlain := timing + " " + glyph
	statusStyled := toolEntryFaint.Render(timing+" ") + glyphStyle.Render(glyph)
	if anyActive {
		// The run isn't finished — a timing total would read as final. Show
		// only the in-progress glyph on the right edge.
		rightPlain = glyph
		statusStyled = glyphStyle.Render(glyph)
	}

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

// animateToolSpinner renders the in-progress glyph for a tool entry: a faint
// braille spinner that cycles wall-clock-driven (so phase stays smooth across
// re-renders without per-entry state). 80ms/frame matches the model-thinking
// spinner's tempo; the choice of braille (vs. the amber rolling-block used
// for the assistant placeholder) is deliberate — tools are routine, the
// indicator should be present-but-quiet, not eye-catching.
func animateToolSpinner() string {
	return animateToolSpinnerWithStyle(theme.NewStyles(theme.Cracker()).Muted)
}

func animateToolSpinnerWithStyle(style lipgloss.Style) string {
	const frames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	const frameMs = 80
	runes := []rune(frames)
	glyph := string(runes[int(time.Now().UnixMilli()/frameMs)%len(runes)])
	return style.Render(glyph)
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
