package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
)

// resolveGrantName maps a caller-supplied tool name to a registered tool.
// Exact match wins; only on miss does it strip a leading `mcp__<server>__`
// segment and try again. This lets callers who accidentally emit host-prefixed
// names (e.g. an MCP host that presents Cercano's tools as `mcp__oc__Read` to
// the model, then passes the same string as data into `workflow.tools`) still
// find the underlying tool — without shadowing a legitimately hosted MCP tool
// that happens to be registered under its literal fully-qualified name.
//
// Returns the resolved name and true on success, or ("", false) on miss.
func (s *Server) resolveGrantName(requested string) (string, bool) {
	if _, ok := s.toolRegistry.Get(requested); ok {
		return requested, true
	}
	if rest, ok := strings.CutPrefix(requested, "mcp__"); ok {
		if idx := strings.Index(rest, "__"); idx >= 0 {
			stripped := rest[idx+2:]
			if stripped != "" {
				if _, ok := s.toolRegistry.Get(stripped); ok {
					return stripped, true
				}
			}
		}
	}
	return "", false
}

// grantedRegistry builds the least-privilege tool registry for an agentic
// dispatch. With no requested tools, it grants read-only tools. With requested
// tools, it grants the named subset — but bounded by the parent permission
// mode: a non-interactive dispatch under a non-bypass mode cannot wield W/X
// tools (no human to confirm), so those are dropped (and logged).
//
// Returns an error whenever the resulting catalog would be empty. Spawning a
// sub-agent with no tools is never what the caller intended, and the resulting
// loop (model improvises tool calls with no schema, gets errors, hits the
// 3-consecutive-error abort) is far worse than a clear error naming the
// offending inputs and the registered tools available.
func (s *Server) grantedRegistry(tools []string, mode agent.PermissionMode) (*agenttools.Registry, error) {
	var candidate *agenttools.Registry
	if len(tools) > 0 {
		candidate = agenttools.NewRegistry()
		var unknown []string
		for _, name := range tools {
			resolved, ok := s.resolveGrantName(name)
			if !ok {
				unknown = append(unknown, name)
				continue
			}
			t, _ := s.toolRegistry.Get(resolved)
			_ = candidate.Register(t)
		}
		if len(unknown) > 0 {
			log.Printf("[dispatch] subagent grant: ignored unknown tool names %v", unknown)
		}
		if len(candidate.All()) == 0 {
			return nil, fmt.Errorf(
				"dispatch: none of the requested tools are registered: %v; %s",
				tools, s.availableToolsHint(mode),
			)
		}
	} else {
		candidate = agenttools.NewRegistry()
		for _, t := range s.toolRegistry.Filter(agenttools.PermR) {
			_ = candidate.Register(t)
		}
		if len(candidate.All()) == 0 {
			return nil, fmt.Errorf(
				"dispatch: no read-only tools available in the registry to grant as the default sub-agent catalog",
			)
		}
	}
	if mode == agent.ModeBypass {
		return candidate, nil
	}
	bounded := agenttools.NewRegistry()
	var dropped []string
	for _, t := range candidate.All() {
		if t.Permission() == agenttools.PermR {
			_ = bounded.Register(t)
		} else {
			dropped = append(dropped, t.Name())
		}
	}
	if len(dropped) > 0 {
		log.Printf("[dispatch] subagent grant bounded by parent permission mode %q: dropped non-read tools %v", mode, dropped)
	}
	if len(bounded.All()) == 0 {
		return nil, fmt.Errorf(
			"dispatch: every requested tool %v was dropped by permission mode %q (only read-tier tools survive a non-bypass mode); %s",
			tools, mode, s.availableToolsHint(mode),
		)
	}
	return bounded, nil
}

// availableToolsHint returns a comma-separated list of registered tool names
// appropriate for the given permission mode, truncated so a pathological
// registry doesn't blow up the error message. R-tier only under strict/
// permissive (those are the only tools a sub-agent can actually wield);
// everything under bypass.
func (s *Server) availableToolsHint(mode agent.PermissionMode) string {
	const maxNames = 30
	var pool []agenttools.Tool
	if mode == agent.ModeBypass {
		pool = s.toolRegistry.All()
	} else {
		pool = s.toolRegistry.Filter(agenttools.PermR)
	}
	if len(pool) == 0 {
		return "no tools are registered in the sub-agent's catalog"
	}
	names := make([]string, 0, len(pool))
	for _, t := range pool {
		names = append(names, t.Name())
	}
	suffix := ""
	if len(names) > maxNames {
		names = names[:maxNames]
		suffix = fmt.Sprintf(" (+%d more)", len(pool)-maxNames)
	}
	return fmt.Sprintf("available tools: %s%s", strings.Join(names, ", "), suffix)
}

// runAgenticDispatch implements dispatch.AgenticRunner. It is wired onto the
// dispatch.Engine via SetDispatchEngine so that internal/dispatch need not
// import internal/agent (which would create an import cycle).
//
// It builds a least-privilege registry, assembles a system prompt, and runs
// agent.RunToolLoop, returning the final text and token counts.
func (s *Server) runAgenticDispatch(ctx context.Context, spec dispatch.Spec, sel dispatch.Selection, model string) (dispatch.Result, error) {
	// 1. Build the least-privilege tool registry, bounded by parent permission mode.
	// A non-interactive dispatch cannot prompt a human, so W/X tools are
	// dropped unless the parent mode is bypass (which explicitly disables all gating).
	mode := agent.ModePermissive
	if s.permStore != nil {
		mode = s.permStore.Mode()
	}
	reg, err := s.grantedRegistry(spec.Tools, mode)
	if err != nil {
		return dispatch.Result{}, err
	}

	// 2. Build system prompt (env grounding + steering block + project context).
	system := s.buildSystemPrompt(spec.WorkDir)

	// 3. Run the bounded tool loop.
	var buf strings.Builder
	res, err := agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:      sel.Provider,
		Model:         model,
		System:        system,
		Registry:      reg,
		Permissions:   s.permStore, // parent store; R-tier never gates regardless
		UserInput:     spec.Task,
		MaxIterations: spec.MaxIterations,
		OnTextDelta:   func(t string) { buf.WriteString(t) },
		// EventSink: nil — non-interactive; PermissionRequester: nil — R-tier won't gate.
	})
	if err != nil {
		return dispatch.Result{}, err
	}

	// 4. Assemble result. Prefer ToolLoopResult.FinalText (the last assistant
	// text block from the loop); fall back to the streamed buf if it's empty
	// (should not happen in practice, but defensive).
	text := res.FinalText
	if text == "" {
		text = buf.String()
	}

	return dispatch.Result{
		Text:         text,
		Model:        model,
		IsCloud:      sel.IsCloud,
		InputTokens:  res.InputTokens,
		OutputTokens: res.OutputTokens,
	}, nil
}
