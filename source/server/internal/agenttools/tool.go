// Package agenttools defines the general-purpose Tool interface and
// registry the agent uses to inspect code, run commands, and edit files.
// Distinct from internal/tools which holds the LoopAgent's per-language
// CODE VALIDATORS — those are a different concept (validate AI-generated
// code; this package is "let the agent do things").
//
// Design choices (per conductor/tracks/cli/spec.md §8.5):
//
//   - Permission tiers R / W / X drive the CLI's confirm-prompt UI. R-tier
//     read-only tools run silently; W-tier ask before applying; X-tier
//     (destructive) always ask, even under /bypass tiered mode.
//
//   - Structured Result, not raw stdout. Models receive parsed JSON-ish
//     row sets so they can reason about content programmatically and so
//     the CLI can render them via the Table primitive instead of
//     scrambling on wide grep output.
//
//   - Truncation policy baked into the helpers (200 rows / 32 KiB) so a
//     wide-open grep of a monorepo doesn't blow the model's context.
package agenttools

import (
	"context"
	"encoding/json"
)

// Permission tags how risky a Tool's effects are. The CLI uses these to
// decide whether to surface a confirm prompt.
type Permission string

const (
	// PermR is read-only: grep, find, read_file, list_dir, git_status, etc.
	// Run silently; output streams into scrollback.
	PermR Permission = "R"

	// PermW writes: write_file, edit_file, apply_patch, git_add, git_commit,
	// run_command, run_tests, build. Confirm prompt with preview before
	// execution.
	PermW Permission = "W"

	// PermX is destructive: rm_file, mv_file, git_push, git_reset --hard.
	// Always confirms, even under /bypass tiered mode. Never inferred from
	// chat phrasing — user must say it directly or click through.
	PermX Permission = "X"
)

// Tool is the agent's first-class capability surface. Each tool has a name,
// a one-line description (used both for /tools listing and for embedding-
// based dispatch), an args schema (JSON Schema document as bytes), a
// permission tier, and an Execute function returning a structured Result.
type Tool interface {
	Name() string
	Description() string
	Permission() Permission
	Schema() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (*Result, error)
}

// ResultType tells the renderer how to display the Result.
type ResultType string

const (
	// ResultRows is a list of homogeneous maps — grep matches, file lists,
	// git log entries, etc. Renders via the CLI's Table primitive.
	ResultRows ResultType = "rows"

	// ResultText is plain prose / file content. Renders as monospace text
	// in scrollback.
	ResultText ResultType = "text"

	// ResultJSON is a structured payload too irregular for the rows form.
	// Renders as a code-fenced JSON block.
	ResultJSON ResultType = "json"
)

// Result is the canonical output shape every Tool returns. Exactly one of
// Rows / Text / JSON is populated depending on Type. Truncated == true
// signals the upstream output was longer than the agent context budget;
// callers should narrow their query if they need full results.
type Result struct {
	Type      ResultType        `json:"type"`
	Rows      []map[string]any  `json:"rows,omitempty"`
	Text      string            `json:"text,omitempty"`
	JSON      json.RawMessage   `json:"json,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	// Note is an optional one-line addendum surfaced to the user — used to
	// explain truncation ("220/1500 matches shown — refine the query") or
	// note that a tool fell back to a slower implementation (e.g. grep
	// instead of rg).
	Note string `json:"note,omitempty"`
}

// NewRowsResult constructs a rows-shaped Result, applying the standard
// row-count truncation policy (200 rows max).
func NewRowsResult(rows []map[string]any) *Result {
	r := &Result{Type: ResultRows}
	const maxRows = 200
	if len(rows) > maxRows {
		r.Rows = rows[:maxRows]
		r.Truncated = true
		r.Note = "showed first 200 rows; refine the query for more"
		return r
	}
	r.Rows = rows
	return r
}

// NewTextResult constructs a text-shaped Result with byte-cap truncation
// (32 KiB) so the agent doesn't ingest megabytes of file content.
func NewTextResult(text string) *Result {
	r := &Result{Type: ResultText}
	const maxBytes = 32 * 1024
	if len(text) > maxBytes {
		// Truncate at codepoint boundary so the rendered string stays
		// valid UTF-8.
		cut := maxBytes
		for cut > 0 && !isUTF8Boundary(text[cut]) {
			cut--
		}
		r.Text = text[:cut] + "\n… (truncated)"
		r.Truncated = true
		r.Note = "showed first 32 KiB; refine to get more"
		return r
	}
	r.Text = text
	return r
}

func isUTF8Boundary(b byte) bool {
	// Continuation byte: 10xxxxxx (0x80..0xBF). Not a boundary.
	return (b & 0xC0) != 0x80
}
