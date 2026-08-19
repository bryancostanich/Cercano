// Package agenttools defines the general-purpose Tool interface and
// registry the agent uses to inspect code, run commands, and edit files.
// Distinct from internal/tools which holds the LoopAgent's per-language
// CODE VALIDATORS — those are a different concept (validate AI-generated
// code; this package is "let the agent do things").
//
// Design choices (per docs/features/cli/README.md, "Design principles"):
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

	"cercano/source/server/internal/llm"
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

// ArgsPermissioner is optionally implemented by tools whose effective
// permission tier depends on the call's arguments. The loop consults it when
// partitioning calls, falling back to Permission() for tools that don't
// implement it. Motivating case: dispatch — a read-only grant is W, but a
// grant containing W/X tools escalates the dispatch call itself to X so a
// human confirms once for the sub-agent's whole toolset.
type ArgsPermissioner interface {
	PermissionFor(args json.RawMessage) Permission
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
	Type      ResultType       `json:"type"`
	Rows      []map[string]any `json:"rows,omitempty"`
	Text      string           `json:"text,omitempty"`
	JSON      json.RawMessage  `json:"json,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	// Note is an optional one-line addendum surfaced to the user — used to
	// explain truncation ("220/1500 matches shown — refine the query") or
	// note that a tool fell back to a slower implementation (e.g. grep
	// instead of rg).
	Note string `json:"note,omitempty"`
	// Detail is a clean, timing-free, content-free outcome token ("480 lines",
	// "12 matches", "+3 −1") shown by clients next to the status glyph. Unlike
	// the derived summary, it never carries result content.
	Detail string `json:"detail,omitempty"`
	// StartLine is the 1-based line in the target file where a file-mutating
	// tool's change begins (edit_file: first line of the replaced span,
	// computed against the pre-edit file; write_file: 1). 0 = not applicable.
	// Clients use it to number the lines of the args diff they render.
	StartLine int `json:"start_line,omitempty"`
	// Images carries any image content a tool returns (MCP tools whose result
	// includes image parts). Each entry is a BlockImage-typed llm.Block with
	// MediaType + ImageData populated. The tool loop appends these as sibling
	// image blocks in the same user turn, immediately after the tool_result —
	// but only when the active model reports SupportsVision; otherwise the
	// tool_result text carries a stub and the images are dropped.
	Images []llm.Block `json:"images,omitempty"`
}

// LLMContent renders the result as the text the model receives as the tool
// result. Text results pass through; rows/JSON results serialize so the model
// actually sees the output — an empty result makes the model re-call the tool
// and loop to the iteration cap. A truncation/fallback Note is appended so the
// model knows the view was capped.
func (r *Result) LLMContent() string {
	var body string
	switch r.Type {
	case ResultRows:
		if b, err := json.Marshal(r.Rows); err == nil {
			body = string(b)
		}
	case ResultJSON:
		body = string(r.JSON)
	default:
		body = r.Text
	}
	if r.Note != "" {
		if body != "" {
			body += "\n"
		}
		body += "(" + r.Note + ")"
	}
	return body
}

// NewRowsResult constructs a rows-shaped Result, applying the shared row
// truncation policy: row count, per-value size, and total serialized bytes.
// See agenttools.TruncateRows for why all three ceilings are needed.
func NewRowsResult(rows []map[string]any) *Result {
	t := TruncateRows(rows)
	return &Result{
		Type:      ResultRows,
		Rows:      t.Rows,
		Truncated: t.Truncated,
		Note:      t.Note,
	}
}

// NewTextResult constructs a text-shaped Result with byte-cap truncation
// (32 KiB) so the agent doesn't ingest megabytes of file content.
func NewTextResult(text string) *Result {
	r := &Result{Type: ResultText}
	if len(text) > MaxResultBytes {
		// Truncate at codepoint boundary so the rendered string stays
		// valid UTF-8.
		r.Text = TruncateUTF8(text, MaxResultBytes) + "\n… (truncated)"
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

// Origin tags where a Tool came from. Built-ins are first-party; MCP tools are
// third-party code hosted from an external server and are gated more strictly.
type Origin string

const (
	OriginBuiltin Origin = "builtin"
	OriginMCP     Origin = "mcp"
)

// Originer is an optional interface a Tool may implement to declare its origin.
// Tools that do not implement it are treated as built-in, so the 15 first-party
// tools need no changes.
type Originer interface {
	Origin() Origin
}

// OriginOf returns a tool's origin, defaulting to OriginBuiltin when the tool
// does not implement Originer.
func OriginOf(t Tool) Origin {
	if o, ok := t.(Originer); ok {
		return o.Origin()
	}
	return OriginBuiltin
}

// Destructiver is an optional interface a Tool may implement to report that it
// performs destructive updates (per the MCP destructiveHint). This is a
// display-only signal — clients surface a ⚠ marker — and never changes gating.
type Destructiver interface {
	Destructive() bool
}

// IsDestructive reports whether a tool advertises itself as destructive,
// defaulting to false when the tool does not implement Destructiver.
func IsDestructive(t Tool) bool {
	if d, ok := t.(Destructiver); ok {
		return d.Destructive()
	}
	return false
}
