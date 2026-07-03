// Package agentadapter exposes capabilities to the standalone agent loop by
// wrapping each Capability as an agenttools.Tool, so internal/agent/toolloop.go
// is untouched.
package agentadapter

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

// AliasMap maps a capability's canonical name to its standalone display name
// (e.g. "read_file" -> "Read"). Missing entries default to the canonical name.
type AliasMap map[string]string

func (m AliasMap) display(canonical string) string {
	if d, ok := m[canonical]; ok && d != "" {
		return d
	}
	return canonical
}

// capTool adapts a Capability to agenttools.Tool.
type capTool struct {
	cap     capabilities.Capability
	display string
}

// AsTool wraps cap as an agenttools.Tool using the given display name.
func AsTool(cap capabilities.Capability, display string) agenttools.Tool {
	if display == "" {
		display = cap.Name()
	}
	return capTool{cap: cap, display: display}
}

func (t capTool) Name() string                 { return t.display }
func (t capTool) Description() string          { return t.cap.Description() }
func (t capTool) Permission() agenttools.Permission { return t.cap.Tier().ToPermission() }
func (t capTool) Schema() json.RawMessage      { return json.RawMessage(t.cap.Schema()) }

func (t capTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	call := &capabilities.Call{
		Args: args,
		// The loop has already gated W/X before calling Execute, and emits its
		// own events around execution, so allow-all + no-op here is correct and
		// behavior-preserving for the migrated tools.
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
	}
	res, err := t.cap.Execute(ctx, call)
	if err != nil {
		return nil, err
	}
	return toAgentResult(res), nil
}

func toAgentResult(r *capabilities.Result) *agenttools.Result {
	return &agenttools.Result{
		Type:      agenttools.ResultType(r.Type),
		Rows:      r.Rows,
		Text:      r.Text,
		JSON:      r.JSON,
		Truncated: r.Truncated,
		Note:      r.Note,
		Detail:    r.Detail,
	}
}

// SynonymMap maps a capability's canonical name to alternate names that
// should ALSO be registered as tools pointing at the same capability. Unlike
// AliasMap (a pure rename), synonyms are additive.
type SynonymMap map[string][]string

// BuildAgentRegistry constructs an agenttools.Registry from the agent-surface
// capabilities in reg. The primary tool for each capability is registered
// under its display name (from aliases, defaulting to canonical). Any
// synonyms are registered as additional tools pointing at the same
// capability, skipping any synonym that would collide with the primary name.
func BuildAgentRegistry(reg *capabilities.Registry, aliases AliasMap, synonyms SynonymMap) *agenttools.Registry {
	ar := agenttools.NewRegistry()
	for _, c := range reg.ForSurface(capabilities.SurfaceAgent) {
		primary := aliases.display(c.Name())
		ar.MustRegister(AsTool(c, primary))
		for _, syn := range synonyms[c.Name()] {
			if syn == "" || syn == primary {
				continue
			}
			ar.MustRegister(AsTool(c, syn))
		}
	}
	return ar
}
