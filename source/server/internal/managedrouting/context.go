package managedrouting

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/managedsettings"
	"context"
)

type candidatesKey struct{}

// PinCandidates keeps unlocked developer preferences and provider bindings fixed
// through a managed turn and its delegated calls. Current authorization remains
// independent and is checked by each physical transport attempt.
func PinCandidates(ctx context.Context, tiers inference.Tiers) context.Context {
	if _, managed := managedsettings.FromContext(ctx); !managed {
		return ctx
	}
	if _, ok := ctx.Value(candidatesKey{}).(inference.Tiers); ok {
		return ctx
	}
	return context.WithValue(ctx, candidatesKey{}, tiers)
}

func candidatesFor(ctx context.Context, fresh inference.Tiers) inference.Tiers {
	if pinned, ok := ctx.Value(candidatesKey{}).(inference.Tiers); ok {
		return pinned
	}
	return fresh
}
