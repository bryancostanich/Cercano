package watchdog

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/llm"
)

// Action represents an agent action that a Check may inspect.
type Action struct {
	Kind       string // "tool_call" | "turn_end"
	ToolName   string
	ToolArgs   json.RawMessage
	Text       string
	Transcript []llm.Message
}

// OneShotFunc sends a single prompt to the LLM and returns the text response.
type OneShotFunc func(ctx context.Context, prompt string) (string, error)

// Check is the interface implemented by every watchdog rule.
type Check interface {
	// Name returns a stable identifier for this check.
	Name() string
	// Applies reports whether this check should run for the given action.
	Applies(a Action) bool
	// Evaluate runs the check and returns a Verdict (and any error).
	Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error)
}
