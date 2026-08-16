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
	return "Run a sub-agent: hand off an open-ended task to a bounded tool-use loop over a granted set of tools (default: read-only tools). Include a concise human-facing `intent` when asking for approval if it clarifies why the delegation is needed. Returns the sub-agent's final result. Tool names passed in `tools` must be the plain registered names (e.g. \"Read\", \"Glob\") — do NOT include any host/MCP prefix like \"mcp__oc__\". Prefer scoped tools such as git_info, git_status, git_diff_stat, git_push, and github_issue_close over Bash inside delegated workflows. For a direct user request to push/publish the current branch, prefer calling git_push directly so the confirmation prompt authorizes the actual push attempt. Granting write-capable tools (Edit, Write, Bash, git_*, github_issue_close) escalates this call to a confirm prompt; one approval authorizes the sub-agent's whole toolset for the run."
}
func (dispatchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":            {"type": "string", "description": "Open-ended instruction for the sub-agent tool loop."},
			"tools":           {"type": "array", "items": {"type": "string"}, "description": "Tool or capability names to grant, using the plain registered names (e.g. \"Read\", \"Glob\", \"Grep\", \"Bash\") — no host or MCP prefix. Omit to default to read-only tools."},
			"tier":            {"type": "string", "enum": ["light", "standard", "deep"], "description": "How much reasoning the task needs — NOT where it runs (locus config decides that). \"light\" (default): recon/tracing/extraction the co-processor tier handles well. \"standard\": everyday coding judgment. \"deep\": hard reasoning that warrants the most capable model. Prefer \"light\" for delegated grunt work so it offloads off the frontier tier."},
			"cwd":             {"type": "string", "description": "Optional absolute project working directory for the sub-agent. Use this for git/GitHub workflows so scoped tools run in the intended repository."},
			"path":            {"type": "string", "description": "Alias for cwd."},
			"intent":          {"type": "string", "description": "Optional concise human-facing reason for the delegation, shown in permission prompts."},
			"conversation_id": {"type": "string", "description": "Optional conversation ID to associate with this dispatch."}
		}
	}`)
}

type dispatchArgs struct {
	Task           string   `json:"task"`
	Tools          []string `json:"tools"`
	Tier           string   `json:"tier"`
	Cwd            string   `json:"cwd"`
	Path           string   `json:"path"`
	Intent         string   `json:"intent"`
	ConversationID string   `json:"conversation_id"`
}

// tierForDispatch maps the model-facing "how much brain" knob onto a taxonomy
// tier. It expresses reasoning demand only — never location. Where the tier
// resolves (open vs cloud) is decided downstream by the user's locus mode via
// RoleCoproc, not here. Unknown/empty defaults to the lightest tier so that
// delegated grunt work offloads off the frontier tier by default.
func tierForDispatch(s string) config.Tier {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "deep":
		return config.TierMostCapable
	case "standard":
		return config.TierEveryday
	default: // "light" and anything unrecognized
		return config.TierFastLight
	}
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
	workDir := strings.TrimSpace(a.Cwd)
	if workDir == "" {
		workDir = strings.TrimSpace(a.Path)
	}
	if workDir == "" {
		workDir = call.WorkDir
	}
	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode: dispatch.Agentic,
		// RoleCoproc, not RoleMain: a delegated sub-agent is offloadable work,
		// so its location must resolve like co-processor work — the user's locus
		// mode decides open vs cloud. RoleMain forced it to resolve like the main
		// thread (cloud under cloud_primary), which defeated the whole point of
		// delegating recon off the frontier tier.
		Role:           dispatch.RoleCoproc,
		Tier:           tierForDispatch(a.Tier),
		Task:           a.Task,
		Tools:          a.Tools,
		WorkDir:        workDir,
		ConversationID: convID,
		Interactive:    false,
		Emit: func(ev agenttools.ProgressEvent) {
			// Structured progress is the primary channel: it carries SubAgentID so
			// the client routes each event to its sub-agent tab. Plain-text Emit is
			// a fallback ONLY when structured progress is unavailable — emitting
			// both duplicated every line and leaked sub-agent activity into the
			// parent transcript.
			if call.EmitProgress != nil {
				call.EmitProgress(ev)
				return
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
	// Lead with route metadata and the sub-agent's actual toolset so callers can
	// see which model/runtime handled the delegated work and whether a mis-grant
	// quietly changed the sub-agent's capabilities.
	header := ""
	// A suspected no-op leads the header: the sub-agent reported completion but
	// its tool record contradicts the claim (e.g. granted Edit/Bash, called
	// neither). Surface it first so the parent does not blindly trust res.Text.
	if res.Suspicious {
		reason := res.SuspicionReason
		if reason == "" {
			reason = "the delegated work likely did not happen"
		}
		header += "[sub-agent warning: " + reason + "]\n"
	}
	if route := dispatchRouteHeader(res); route != "" {
		header += route + "\n"
	}
	if len(res.GrantedTools) > 0 {
		header += "[sub-agent tools: " + strings.Join(res.GrantedTools, ", ")
		if len(res.IgnoredTools) > 0 {
			header += " — ignored unknown: " + strings.Join(res.IgnoredTools, ", ")
		}
		header += "]\n"
	}
	if header != "" {
		header += "\n"
	}
	return capabilities.NewTextResult(header + res.Text), nil
}

func dispatchRouteHeader(res dispatch.Result) string {
	if res.Model == "" && res.Provider == "" && res.Tier == "" {
		return ""
	}
	location := "open"
	if res.IsCloud {
		location = "cloud"
	}
	parts := []string{"[sub-agent route: " + location}
	if res.Provider != "" {
		parts = append(parts, "provider="+res.Provider)
	}
	if res.Model != "" {
		parts = append(parts, "model="+res.Model)
	}
	if res.Tier != "" {
		parts = append(parts, "tier="+res.Tier)
	}
	return strings.Join(parts, " ") + "]"
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
