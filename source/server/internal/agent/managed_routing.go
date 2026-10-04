package agent

import (
	"context"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/managedrouting"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
)

// SetManagedCandidates connects legacy unary requests to the same provider
// binding used by main turns and dispatch. It is installed before serving RPCs.
func (a *Agent) SetManagedCandidates(candidates func() inference.Tiers) {
	if a != nil {
		a.managedCandidates = candidates
	}
}

type managedTurnKey struct{}

// ManagedTurnProvider is the turn's selected managed chain. Coordinators reuse
// it for generation and repair instead of switching to a personal provider.
func ManagedTurnProvider(ctx context.Context) (TurnRunner, bool) {
	p, ok := ctx.Value(managedTurnKey{}).(TurnRunner)
	return p, ok
}

func managedRequest(ctx context.Context) bool {
	_, pinned := managedsettings.FromContext(ctx)
	return pinned || modelpolicy.Managed(ctx)
}

func (a *Agent) selectManaged(ctx context.Context, req *Request) (context.Context, TurnRunner, Intent, bool, error) {
	if !managedRequest(ctx) {
		return ctx, nil, "", false, nil
	}
	if a.managedCandidates == nil {
		return ctx, nil, "", true, modelpolicy.Deny(modelpolicy.Attempt{}, "managed provider binding is unavailable")
	}
	task, intent := config.TaskChat, IntentChat
	if req.WorkDir != "" && req.FileName != "" {
		intent = IntentCoding
	}
	if req.DirectOpen {
		task = config.TaskDispatch
	}
	tiers := a.managedCandidates()
	ctx = managedrouting.PinCandidates(ctx, tiers)
	selected, assignment, model, _, err := managedrouting.Select(ctx, task, tiers, managedrouting.Request{Model: req.ModelOverride, LocalOnly: req.DirectOpen})
	if err != nil {
		return ctx, nil, intent, true, err
	}
	provider := &managedTurnRunner{TurnRunner: InferenceTurnRunner(selected.Provider, model), model: model, tier: string(assignment.Quality.CapabilityTier())}
	return context.WithValue(ctx, managedTurnKey{}, TurnRunner(provider)), provider, intent, true, nil
}

type managedTurnRunner struct {
	TurnRunner
	model, tier string
}

func (p *managedTurnRunner) Process(ctx context.Context, req *Request) (*Response, error) {
	// Selection already validated explicit overrides. Nested generator requests
	// must keep that selection rather than replacing its model or quality tier.
	copy := *req
	copy.ModelOverride, copy.Tier = p.model, p.tier
	return p.TurnRunner.Process(ctx, &copy)
}
