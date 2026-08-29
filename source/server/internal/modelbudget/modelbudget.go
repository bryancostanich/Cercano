package modelbudget

import (
	"context"
	"fmt"
)

// Target describes the concrete model route a local one-shot prompt will use.
// It is intentionally prompt-free so callers can ask for budgeting metadata
// before constructing large source-heavy prompts.
type Target struct {
	Provider           string
	Model              string
	Tier               string
	IsCloud            bool
	ContextWindow      int
	ContextWindowKnown bool
}

// Budget is the usable input-token envelope for one model call after reserving
// room for the response and protocol/chat overhead.
type Budget struct {
	Target          Target
	OutputReserve   int
	OverheadReserve int
	InputTokens     int
}

const (
	DefaultOutputReserve   = 4096
	DefaultOverheadReserve = 1024
	MinimumInputBudget     = 512
)

// ForTarget computes a conservative input budget for target.
func ForTarget(target Target, outputReserve, overheadReserve int) (Budget, error) {
	if outputReserve <= 0 {
		outputReserve = DefaultOutputReserve
	}
	if overheadReserve <= 0 {
		overheadReserve = DefaultOverheadReserve
	}
	if !target.ContextWindowKnown || target.ContextWindow <= 0 {
		return Budget{Target: target, OutputReserve: outputReserve, OverheadReserve: overheadReserve}, fmt.Errorf("research model budget unavailable for provider=%q model=%q: unknown context window", target.Provider, target.Model)
	}
	input := target.ContextWindow - outputReserve - overheadReserve
	if input < MinimumInputBudget {
		return Budget{Target: target, OutputReserve: outputReserve, OverheadReserve: overheadReserve, InputTokens: input}, fmt.Errorf("research model budget too small for provider=%q model=%q: context_window=%d output_reserve=%d overhead_reserve=%d input_budget=%d", target.Provider, target.Model, target.ContextWindow, outputReserve, overheadReserve, input)
	}
	return Budget{Target: target, OutputReserve: outputReserve, OverheadReserve: overheadReserve, InputTokens: input}, nil
}

// Budgeter is the optional interface research prompt builders use when a model
// caller can expose concrete target metadata before Call.
type Budgeter interface {
	Budget(ctx context.Context, outputReserve int) (Budget, error)
}
