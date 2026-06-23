// Package ui — humanized formatting for tool-call scrollback lines.
//
// The agent stream hands the CLI two raw strings per tool call: the verbatim
// tool-call JSON (args) and a server-built one-line summary (result). Dumping
// those directly reads like machine output — `Bash {"cmd":[...]}  ✓ # Cercano`.
// These helpers turn them into a glanceable line: the meaningful argument as
// plain text, and a result blurb of "<detail> · <timing>".
//
// Everything here is CLI-side and pure (no engine/proto changes). Timing is
// measured by the caller (wall clock between exec-start and exec-complete) and
// passed in, because the wire carries no duration. Result detail is limited to
// what the server summary already exposes: tool summaries that are actually
// file/path content (Read, Glob, Grep) are suppressed to timing-only, since
// the CLI can't mine counts the engine never sent.
package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// formatDur renders a duration for the result blurb: "686ms", "1.23s", or
// "<1ms" for anything that rounds below a millisecond (sub-ms timings are noise,
// not signal, and "0s" reads as broken).
func formatDur(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

// relPath makes an absolute path readable: relative to the project root when it
// lives under it, "~/…" when under the home dir, otherwise unchanged. Paths that
// are already relative pass through untouched.
func relPath(p, root, home string) string {
	if !strings.HasPrefix(p, "/") {
		return p
	}
	if p == root {
		return "."
	}
	if root != "" && strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:]
	}
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~/" + p[len(home)+1:]
	}
	return p
}

// humanizeArgs turns a tool's raw call JSON into a one-line argument summary.
// Unknown tools fall back to sorted "key=value" pairs of their scalar args.
func humanizeArgs(tool, argsJSON, root, home string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return strings.Join(strings.Fields(argsJSON), " ")
	}

	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}

	switch tool {
	case "Bash":
		return shellJoin(toStrings(m["cmd"]))
	case "Read", "Write", "Edit", "LS", "stat_file", "rm_file":
		return relPath(str("path"), root, home)
	case "Glob", "Grep":
		pat := str("pattern")
		if p := str("path"); p != "" {
			if rp := relPath(p, root, home); rp != "" && rp != "." {
				return pat + " in " + rp
			}
		}
		return pat
	case "git_commit":
		return str("message")
	case "git_push":
		rb := strings.TrimSpace(str("remote") + " " + str("branch"))
		return rb
	}
	return scalarPairs(m)
}

// humanizeResult composes the result blurb shown after the status glyph as
// "<detail> · <timing>", or just "<timing>" when there's no detail. The detail
// is the server's structured, content-free outcome token (ToolExecComplete.
// detail); on error the (trimmed) error summary takes its place.
func humanizeResult(detail, errSummary string, isErr bool, d time.Duration) string {
	out := strings.TrimSpace(detail)
	if isErr {
		out = truncate(strings.TrimSpace(errSummary), 60)
	}
	timing := formatDur(d)
	if out == "" {
		return timing
	}
	return out + " · " + timing
}

// shellJoin renders an argv slice as a copy-pasteable command line, single-
// quoting only tokens that contain whitespace or are empty.
func shellJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n") {
			out[i] = "'" + a + "'"
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

// toStrings coerces a decoded JSON array into a []string, dropping non-strings.
func toStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// scalarPairs renders an object's scalar fields as sorted "key=value" pairs.
// Stable order keeps the line deterministic across renders.
func scalarPairs(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		switch v.(type) {
		case string, float64, bool:
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
