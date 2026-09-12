package inference

import (
	"cercano/source/server/internal/llm"
	"context"
)

// TargetFor describes one concrete attempt without sending a prompt. Providers
// without scoped metadata remain unknown rather than borrowing another route.
func TargetFor(provider Provider, model, tier string) llm.ServingRoute {
	if target, ok := provider.(interface {
		TargetFor(string, string) llm.ServingRoute
	}); ok {
		return target.TargetFor(model, tier)
	}
	name := ""
	if provider != nil {
		name = provider.Name()
	}
	return llm.ServingRoute{Provider: name, Model: model}
}

func TargetForCall(provider Provider, req Call) llm.ServingRoute {
	if target, ok := provider.(interface{ TargetForCall(Call) llm.ServingRoute }); ok {
		return target.TargetForCall(req)
	}
	return TargetFor(provider, req.Model, req.Tier)
}

// TargetForContext includes request-owned route selection (for example an
// explicitly authorized authentication fallback), never shared provider state.
func TargetForContext(ctx context.Context, provider Provider, req Call) llm.ServingRoute {
	if target, ok := provider.(interface {
		TargetForContext(context.Context, Call) llm.ServingRoute
	}); ok {
		return target.TargetForContext(ctx, req)
	}
	return TargetForCall(provider, req)
}
