package server

import "encoding/json"

// maxArgFieldRunes caps each string field when summarizing a tool call's args
// for the log trace and the streamed ArgsSummary. The oversized fields on
// Edit/Write are the file content (old_string / new_string / content); capping
// them keeps the trace and the per-call wire message small, while the short
// structural fields (path, cmd, pattern, message) pass through untouched so the
// CLI's humanizeArgs can still parse the summary to render a tool entry.
const maxArgFieldRunes = 200

// summarizeArgs returns a compact, still-valid-JSON rendering of a tool call's
// arguments with any oversized top-level string value truncated. Short args are
// returned byte-for-byte (no re-marshal churn). Unparseable input falls back to
// a rune-capped raw string — never the full blob. The result is always safe to
// json.Unmarshal (the CLI does) and small enough to log and stream every call.
func summarizeArgs(argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		// Not an object (or malformed) — cap the raw text so a huge value can't
		// flood the log / wire even when we can't structure it.
		return truncateRunes(argsJSON, maxArgFieldRunes*4)
	}
	changed := false
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if t := truncateRunes(s, maxArgFieldRunes); t != s {
			m[k] = t
			changed = true
		}
	}
	if !changed {
		return argsJSON // nothing oversized — preserve the exact original
	}
	b, err := json.Marshal(m)
	if err != nil {
		return truncateRunes(argsJSON, maxArgFieldRunes*4)
	}
	return string(b)
}

// truncateRunes caps s to max runes, appending an ellipsis when it cuts. Counts
// runes (not bytes) so it never splits a multibyte character. (Package-local;
// internal/agent has its own copy for the result-summary path.)
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
