package server

import (
	"context"
	"strings"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
)

// runAgenticDispatch implements dispatch.AgenticRunner. It is wired onto the
// dispatch.Engine via SetDispatchEngine so that internal/dispatch need not
// import internal/agent (which would create an import cycle).
//
// It builds a least-privilege registry, assembles a system prompt, and runs
// agent.RunToolLoop, returning the final text and token counts.
func (s *Server) runAgenticDispatch(ctx context.Context, spec dispatch.Spec, sel dispatch.Selection, model string) (dispatch.Result, error) {
	// 1. Build the least-privilege tool registry.
	var reg *agenttools.Registry
	if len(spec.Tools) > 0 {
		// Caller supplied an explicit allowlist — use it verbatim.
		reg = s.toolRegistry.Subset(spec.Tools)
	} else {
		// Default: R-tier only. R-tier tools never gate (run silently),
		// making them safe for unattended agentic sub-tasks.
		reg = agenttools.NewRegistry()
		for _, t := range s.toolRegistry.Filter(agenttools.PermR) {
			_ = reg.Register(t) // can't duplicate; Filter returns each tool once
		}
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
