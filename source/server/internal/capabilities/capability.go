// Package capabilities defines the single Capability interface and registry
// that both the standalone agent loop (via agentadapter) and the MCP plugin
// surface (via mcpadapter + the InvokeCapability RPC) consume. One capability
// is implemented once and exposed on both surfaces — no duplicated logic.
package capabilities

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// Tier tags how risky a capability's effects are; drives standalone confirm gating.
type Tier string

const (
	TierR Tier = "R" // read-only, runs silently
	TierW Tier = "W" // writes, confirm before applying
	TierX Tier = "X" // destructive, always confirm
)

// ToPermission maps a Tier to the agenttools.Permission used by the loop's gate.
func (t Tier) ToPermission() agenttools.Permission {
	switch t {
	case TierW:
		return agenttools.PermW
	case TierX:
		return agenttools.PermX
	default:
		return agenttools.PermR
	}
}

// Surface is a bitmask of the places a capability is exposed.
type Surface uint8

const (
	SurfaceAgent Surface = 1 << iota // standalone agent loop
	SurfaceMCP                       // MCP plugin
)

// Has reports whether s includes every bit in want.
func (s Surface) Has(want Surface) bool { return s&want == want }

// Schema is a JSON Schema document describing a capability's parameters.
type Schema = json.RawMessage

// ResultType tells clients how to render a Result.
type ResultType string

const (
	ResultRows ResultType = "rows"
	ResultText ResultType = "text"
	ResultJSON ResultType = "json"
)

// Result is the canonical output shape. Mirrors agenttools.Result so the agent
// adapter converts by field copy.
type Result struct {
	Type      ResultType       `json:"type"`
	Rows      []map[string]any `json:"rows,omitempty"`
	Text      string           `json:"text,omitempty"`
	JSON      json.RawMessage  `json:"json,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	Note      string           `json:"note,omitempty"`
	Detail    string           `json:"detail,omitempty"`
	// StartLine is the 1-based line in the target file where a file-mutating
	// capability's change begins (edit_file: first line of the replaced span,
	// computed against the pre-edit file; write_file: 1). 0 = not applicable.
	StartLine int `json:"start_line,omitempty"`
	// Images carries any image content the capability wants to place in front
	// of the model. Each entry is a BlockImage-typed llm.Block with MediaType +
	// base64 ImageData populated (view_image is the built-in producer). The
	// agent adapter copies these onto agenttools.Result.Images, and the tool
	// loop appends them as sibling blocks after the tool_result when the active
	// model reports vision support — the same path MCP image results travel.
	Images []llm.Block `json:"images,omitempty"`
}

// LLMContent renders the result as the text the model receives.
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

// NewRowsResult applies the shared row truncation policy (row count, per-value
// size, and total serialized bytes). The policy lives in agenttools so this
// type and agenttools.Result cannot drift apart; see agenttools.TruncateRows.
func NewRowsResult(rows []map[string]any) *Result {
	t := agenttools.TruncateRows(rows)
	return &Result{
		Type:      ResultRows,
		Rows:      t.Rows,
		Truncated: t.Truncated,
		Note:      t.Note,
	}
}

// NewTextResult applies the 32 KiB byte-cap truncation policy.
func NewTextResult(text string) *Result {
	r := &Result{Type: ResultText}
	if len(text) > agenttools.MaxResultBytes {
		r.Text = agenttools.TruncateUTF8(text, agenttools.MaxResultBytes) + "\n… (truncated)"
		r.Truncated = true
		r.Note = "showed first 32 KiB; refine to get more"
		return r
	}
	r.Text = text
	return r
}

// Call is the per-invocation context an adapter constructs for each call.
type Call struct {
	Args           json.RawMessage
	WorkDir        string
	ConversationID string
	// RequestPermission lets a capability ask for confirmation mid-execute.
	// The agent surface wires it to the loop gate; the MCP surface passes an
	// allow-all (the host gates). Most capabilities never call it.
	RequestPermission func(ctx context.Context, reason string) (bool, error)
	// Emit streams human-readable progress back to the surface. Nil-safe —
	// capabilities must tolerate a nil Emit.
	Emit func(note string)
	// EmitProgress streams structured progress back to the agent loop. It is used
	// by capabilities such as dispatch that need child-agent events to survive
	// through the parent stream without string scraping.
	EmitProgress func(agenttools.ProgressEvent)
	// Svc gives the capability access to shared services; set by the adapter/handler that invokes it.
	Svc Services
}

// Capability is the single implementation surface for a thing Cercano can do.
type Capability interface {
	Name() string // canonical snake_case id
	Description() string
	Tier() Tier
	Schema() Schema
	Surfaces() Surface
	Execute(ctx context.Context, call *Call) (*Result, error)
}

// ContextAware is implemented by capabilities that want the project-context
// digest prepended to their dispatched prompt. Capabilities that don't
// implement it default to no project context (e.g. fetch).
type ContextAware interface{ WantsProjectContext() bool }

// ArgsTiered is implemented by capabilities whose risk tier depends on the
// call's arguments. Adapters that gate on tier consult it before the static
// Tier(). Motivating case: dispatch escalates to TierX when its grant
// includes write-capable tools, so one human confirm covers the sub-agent run.
type ArgsTiered interface {
	TierFor(args json.RawMessage) Tier
}
