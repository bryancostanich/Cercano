package dispatch

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type managedDispatchProbe struct {
	echoProvider
	requests []llm.ChatRequest
}

func (p *managedDispatchProbe) Chat(ctx context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	p.requests = append(p.requests, r)
	return p.echoProvider.Chat(ctx, r)
}
func TestManagedDispatchUsesTaskDefaultsAndQualityBudget(t *testing.T) {
	for _, task := range []config.Task{config.TaskDispatch, config.TaskReview, config.TaskResearch, config.TaskWatchdog} {
		snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
		snapshot.Policy.TaskDefaults[0].Task = string(task)
		snapshot.Policy.TaskDefaults[0].Quality = "economy"
		approved, personal := &managedDispatchProbe{}, &managedDispatchProbe{}
		tiers := inference.Tiers{Cloud: personal, Open: personal, ManagedRoute: func(context.Context, v1.Route, config.Destination) (inference.Candidate, error) {
			return inference.Candidate{Provider: approved, IsCloud: true}, nil
		}}
		eng := NewEngine(func() inference.Tiers { return tiers }, func() locus.Mode { return locus.OpenOnly }, nil)
		eng.SetModelFor(func(bool, config.Tier) string { return "personal-model" })
		ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
		if _, err := eng.Dispatch(ctx, Spec{Mode: OneShot, RoutingTask: task, Tier: config.TierMostCapable, Prompt: "Hello"}); err != nil {
			t.Fatal(err)
		}
		if len(personal.requests) != 0 || len(approved.requests) != 1 || approved.requests[0].Model != "approved" {
			t.Fatalf("task=%s personal=%d approved=%+v", task, len(personal.requests), approved.requests)
		}
		eng.SetAgenticRunner(func(_ context.Context, spec Spec, sel inference.Selection, model string) (Result, error) {
			if model != "approved" || spec.TokenBudget != config.CostEconomy.DispatchTokenBudget() || spec.EffectiveTier() != config.TierFastLight {
				t.Fatalf("managed delegation lost quality: %+v model=%s", spec, model)
			}
			return Result{}, nil
		})
		if _, err := eng.Dispatch(ctx, Spec{Mode: Agentic, RoutingTask: task, Prompt: "Review"}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestManagedLocalToolNeverCrossesToExternalDefault(t *testing.T) {
	snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
	snapshot.Policy.TaskDefaults[0].Task = string(config.TaskDispatch)
	ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
	eng := NewEngine(func() inference.Tiers { return inference.Tiers{} }, func() locus.Mode { return locus.CloudPrimary }, nil)
	_, err := eng.Dispatch(ctx, Spec{Mode: OneShot, LocalOffload: true, Prompt: "Local-only task"})
	if !modelpolicy.IsDenial(err) {
		t.Fatalf("local-only request escaped managed restrictions: %v", err)
	}
}
