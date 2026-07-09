package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/pkg/config"
)

type dispatchCap struct{}

// Dispatch constructs the dispatch capability.
func Dispatch() capabilities.Capability { return dispatchCap{} }

func (dispatchCap) Name() string            { return "dispatch" }
func (dispatchCap) Tier() capabilities.Tier { return capabilities.TierW }
func (dispatchCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (dispatchCap) Description() string {
	return "Run a sub-agent: hand off an open-ended task to a bounded tool-use loop over a granted set of tools (default: read-only tools). Include a concise human-facing `intent` when asking for approval if it clarifies why the delegation is needed. Returns the sub-agent's final result. Tool names passed in `tools` must be the plain registered names (e.g. \"Read\", \"Glob\") — do NOT include any host/MCP prefix like \"mcp__oc__\". Granting write-capable tools (Edit, Write, Bash, git_*) escalates this call to a confirm prompt; one approval authorizes the sub-agent's whole toolset for the run."
}
func (dispatchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":            {"type": "string", "description": "Open-ended instruction for the sub-agent tool loop."},
			"tools":           {"type": "array", "items": {"type": "string"}, "description": "Tool or capability names to grant, using the plain registered names (e.g. \"Read\", \"Glob\", \"Grep\", \"Bash\") — no host or MCP prefix. Omit to default to read-only tools."},
			"intent":          {"type": "string", "description": "Optional concise human-facing reason for the delegation, shown in permission prompts."},
			"conversation_id": {"type": "string", "description": "Optional conversation ID to associate with this dispatch."}
		}
	}`)
}

type dispatchArgs struct {
	Task           string   `json:"task"`
	Tools          []string `json:"tools"`
	Intent         string   `json:"intent"`
	ConversationID string   `json:"conversation_id"`
}

func (dispatchCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a dispatchArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("dispatch: parse args: %w", err)
	}
	if a.Task == "" {
		return nil, errors.New("dispatch: 'task' is required")
	}
	if call.Svc.Dispatch == nil {
		return nil, errors.New("dispatch: engine not available")
	}
	// Parent linkage: the surface-injected conversation id wins over the
	// model-supplied argument (the model rarely knows its own id).
	convID := call.ConversationID
	if convID == "" {
		convID = a.ConversationID
	}
	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:           dispatch.Agentic,
		Role:           dispatch.RoleMain,
		Tier:           config.TierEveryday,
		Task:           a.Task,
		Tools:          a.Tools,
		WorkDir:        call.WorkDir,
		ConversationID: convID,
		Interactive:    false,
		Emit: func(ev agenttools.ProgressEvent) {
			if call.EmitProgress != nil {
				call.EmitProgress(ev)
			}
			if call.Emit != nil {
				if ev.Text != "" {
					call.Emit(ev.Text)
				} else if ev.Summary != "" {
					call.Emit(ev.Summary)
				}
			}
		},
	})
	if err != nil {
		return nil, err
	}
	// Lead with the sub-agent's actual toolset so a mis-granted run is
	// visible to the caller immediately (a read-only sub-agent once burned
	// 62 turns discovering it couldn't edit).
	header := ""
	if len(res.GrantedTools) > 0 {
		header = "[sub-agent tools: " + strings.Join(res.GrantedTools, ", ")
		if len(res.IgnoredTools) > 0 {
			header += " — ignored unknown: " + strings.Join(res.IgnoredTools, ", ")
		}
		header += "]\n\n"
	}
	return capabilities.NewTextResult(header + res.Text), nil
}

// agentGrantTiers maps every agent-surface tool name (display aliases and
// synonyms included) to its tier, for dispatch's dynamic permission. Built
// once — the built-in catalog is static.
var (
	grantTiersOnce sync.Once
	grantTiers     map[string]capabilities.Tier
)

func agentGrantTiers() map[string]capabilities.Tier {
	grantTiersOnce.Do(func() {
		grantTiers = map[string]capabilities.Tier{}
		reg := capabilities.NewRegistry(capabilities.Services{})
		Register(reg)
		aliases := AgentAliases()
		syns := CapabilitySynonyms()
		for _, c := range reg.ForSurface(capabilities.SurfaceAgent) {
			display := c.Name()
			if d, ok := aliases[c.Name()]; ok && d != "" {
				display = d
			}
			grantTiers[display] = c.Tier()
			for _, s := range syns[c.Name()] {
				grantTiers[s] = c.Tier()
			}
		}
	})
	return grantTiers
}

// stripHostPrefix removes a leading mcp__<host>__ segment, mirroring the
// server's grant-name normalization.
func stripHostPrefix(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if idx := strings.Index(rest, "__"); idx >= 0 && rest[idx+2:] != "" {
			return rest[idx+2:]
		}
	}
	return name
}

// TierFor implements capabilities.ArgsTiered. A dispatch whose grant is all
// read-only built-ins stays W (silent under permissive, like today); any
// write-capable or unknown grant (MCP tools, typos) escalates the dispatch
// call itself to X so a human confirms ONCE — that approval authorizes the
// sub-agent's whole granted toolset for the run.
func (dispatchCap) TierFor(args json.RawMessage) capabilities.Tier {
	var a dispatchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return capabilities.TierW
	}
	if len(a.Tools) == 0 {
		return capabilities.TierW // least-privilege default grant: read-only
	}
	tiers := agentGrantTiers()
	for _, name := range a.Tools {
		t, ok := tiers[name]
		if !ok {
			t, ok = tiers[stripHostPrefix(name)]
		}
		if !ok || t != capabilities.TierR {
			return capabilities.TierX
		}
	}
	return capabilities.TierW
}
