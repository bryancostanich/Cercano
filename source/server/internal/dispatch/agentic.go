package dispatch

import "context"

// AgenticRunner runs a bounded tool loop for an Agentic dispatch. Implemented
// outside this package (in internal/server) to avoid an import cycle with
// internal/agent (which owns RunToolLoop). Given the resolved provider/model,
// it runs the loop and returns the assembled Result.
type AgenticRunner func(ctx context.Context, spec Spec, sel Selection, model string) (Result, error)

// SetAgenticRunner installs the runner that handles Agentic dispatches.
// Must be called before any Agentic Spec is dispatched; Dispatch returns an
// error if the runner is nil when an Agentic spec arrives.
func (e *Engine) SetAgenticRunner(r AgenticRunner) {
	e.agenticRunner = r
}
