package builtins

import (
	"context"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/pkg/config"
)

func TestDispatchModelCallerBudgetUsesDispatchTarget(t *testing.T) {
	var got dispatch.Spec
	call := &capabilities.Call{Svc: capabilities.Services{
		DispatchTarget: func(_ context.Context, spec dispatch.Spec) (modelbudget.Target, error) {
			got = spec
			return modelbudget.Target{
				Provider:           "llama_server",
				Model:              "llama_server:catalog:glm-4.5-air-q4_k_m",
				Tier:               string(config.TierFastLightText),
				ContextWindow:      16384,
				ContextWindowKnown: true,
			}, nil
		},
	}}
	caller := &dispatchModelCaller{call: call, source: "research", tier: config.TierFastLightText}

	budget, err := caller.Budget(context.Background(), 2048)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Target.Provider != "llama_server" || budget.Target.Model == "" {
		t.Fatalf("unexpected budget target: %+v", budget.Target)
	}
	if budget.InputTokens != 16384-2048-modelbudget.DefaultOverheadReserve {
		t.Fatalf("InputTokens = %d, want %d", budget.InputTokens, 16384-2048-modelbudget.DefaultOverheadReserve)
	}
	if got.Source != "research" || got.Tier != config.TierFastLightText || got.Role != dispatch.RoleCoproc || got.Mode != dispatch.OneShot {
		t.Fatalf("dispatch target spec = %+v", got)
	}
}
