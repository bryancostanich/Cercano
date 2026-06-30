// Package ui — display-time path elision for tool-call args.
//
// Tool-call lines render in a width-constrained scrollback column, and many
// of the most common tools (Read, Edit, Write, Glob, MultiEdit) carry full
// absolute paths in their args summary. Without help, a single Read can blow
// out 80+ columns just on its filename.
//
// prettifyPath applies, in order:
//  1. Workspace-relative — if the path is under cwd, strip the prefix.
//  2. Home-relative — else if under $HOME, replace with "~".
//  3. Segment-aware middle elision — collapse middle path components to
//     "..." until the rendered width fits the budget. The leading component
//     and the last 1–N components are preserved so the file's identity stays
//     readable ("internal/.../manager.go" beats truncating to "manager.go").
//
// Only the args summary for path-using tools should go through this; freeform
// args (Bash commands, Grep patterns) want different elision.
package ui

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

// prettifyPath shortens p for display under the given budget (rendered
// columns). Returns p unchanged when budget <= 0 or p already fits.
//
// cwd and home may be empty — the corresponding transformation is then
// simply skipped.
func prettifyPath(p, cwd, home string, budget int) string {
	if p == "" {
		return p
	}
	p = workspaceRelative(p, cwd)
	p = homeRelative(p, home)
	if budget <= 0 || lipgloss.Width(p) <= budget {
		return p
	}
	return segmentElide(p, budget)
}

// workspaceRelative strips cwd+sep prefix if present. Only applies to
// absolute inputs; relative paths pass through unchanged.
func workspaceRelative(p, cwd string) string {
	if cwd == "" || !filepath.IsAbs(p) {
		return p
	}
	sep := string(filepath.Separator)
	cwd = strings.TrimRight(cwd, sep)
	if rel := strings.TrimPrefix(p, cwd+sep); rel != p {
		return rel
	}
	if p == cwd {
		return "."
	}
	return p
}

// homeRelative replaces a $HOME prefix with "~". Only applies to absolute
// inputs that weren't already workspace-shortened.
func homeRelative(p, home string) string {
	if home == "" || !filepath.IsAbs(p) {
		return p
	}
	sep := string(filepath.Separator)
	home = strings.TrimRight(home, sep)
	if strings.HasPrefix(p, home+sep) {
		return "~" + p[len(home):]
	}
	if p == home {
		return "~"
	}
	return p
}

// elideArgs shortens an already-humanized tool args summary to fit budget.
// Path-like inputs (containing "/") get segment-aware elision so file
// identity is preserved at boundaries; everything else gets plain
// middle-elision. Returns the input unchanged when budget <= 0 or it
// already fits.
func elideArgs(s string, budget int) string {
	if s == "" || budget <= 0 || lipgloss.Width(s) <= budget {
		return s
	}
	if strings.Contains(s, "/") {
		if elided := segmentElide(s, budget); lipgloss.Width(elided) <= budget {
			return elided
		}
	}
	return middleElide(s, budget)
}

// middleElide collapses the middle of s with "…" so the result fits budget
// columns. Splits the kept characters evenly between head and tail.
func middleElide(s string, budget int) string {
	if lipgloss.Width(s) <= budget {
		return s
	}
	if budget < 3 {
		// Can't even hold "…X" — refuse to mangle further; caller pays the
		// overflow.
		return s
	}
	keep := budget - 1 // one column for "…"
	head := keep / 2
	tail := keep - head
	if head+tail > len(s) {
		return s
	}
	return s[:head] + "…" + s[len(s)-tail:]
}

// segmentElide collapses middle path components to "..." until the result
// fits budget. Always preserves the leading segment and at least one trailing
// segment. Falls back to ".../<basename>" if nothing else fits.
func segmentElide(p string, budget int) string {
	segs := strings.Split(p, "/")
	if len(segs) <= 2 {
		// "foo/bar" or "foo" — nothing meaningful to elide.
		return p
	}
	// Try keeping leading + N trailing segments, walking down N until it fits.
	for trailing := len(segs) - 2; trailing >= 1; trailing-- {
		candidate := segs[0] + "/.../" + strings.Join(segs[len(segs)-trailing:], "/")
		if lipgloss.Width(candidate) <= budget {
			return candidate
		}
	}
	// Last resort: ".../basename". May still exceed budget if the basename
	// itself is huge; we accept that rather than mangling the filename.
	return ".../" + segs[len(segs)-1]
}
