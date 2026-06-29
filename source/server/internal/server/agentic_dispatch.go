package server

import (
	"context"
	"log"
	"strings"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
)

// grantedRegistry builds the least-privilege tool registry for an agentic
// dispatch. With no requested tools, it grants read-only tools. With requested
// tools, it grants the named subset — but bounded by the parent permission
// mode: a non-interactive dispatch under a non-bypass mode cannot wield W/X
// tools (no human to confirm), so those are dropped (and logged).
func (s *Server) grantedRegistry(tools []string, mode agent.PermissionMode) *agenttools.Registry {
	var candidate *agenttools.Registry
	if len(tools) > 0 {
		candidate = s.toolRegistry.Subset(tools)
	} else {
		candidate = agenttools.NewRegistry()
		for _, t := range s.toolRegistry.Filter(agenttools.PermR) {
			_ = candidate.Register(t)
		}
	}
	if mode == agent.ModeBypass {
		return candidate
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
	return bounded
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
	reg := s.grantedRegistry(spec.Tools, mode)

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
