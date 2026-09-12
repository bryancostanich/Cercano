package dispatch

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

func TestMissingClassIgnoresLegacyRoleAndSource(t *testing.T) {
	for _, role := range []Role{0, RoleMain, RoleCoproc} {
		for _, source := range []string{"", "review", "summarize", "local"} {
			e, p, s, l := taxonomyEngine()
			spec := Spec{Role: role, Source: source, Prompt: "looks like research but is unclassified"}
			target, err := e.PreparedTarget(context.Background(), spec)
			if err != nil || target.Model != "secondary-everyday" {
				t.Fatalf("prepared target %+v %v", target, err)
			}
			_, err = e.Dispatch(context.Background(), spec)
			if err != nil || len(s.calls) != 1 || len(p.calls)+len(l.calls) != 0 {
				t.Fatalf("role=%v source=%q err=%v", role, source, err)
			}
			e.SetAgenticRunner(func(_ context.Context, got Spec, sel inference.Selection, model string) (Result, error) {
				if got.RoutingTask != config.TaskDispatch || got.Tier != config.TierEveryday || sel.Destination != config.DestinationSecondary || model != "secondary-everyday" {
					t.Errorf("agentic spec=%+v sel=%+v model=%s", got, sel, model)
				}
				return Result{Model: model}, nil
			})
			spec.Mode = Agentic
			if _, err = e.Dispatch(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestLocalOffloadCannotCarryTaskAssignment(t *testing.T) {
	e, _, _, _ := taxonomyEngine()
	spec := Spec{LocalOffload: true, RoutingTask: config.TaskDispatch}
	if _, err := e.Target(spec); err == nil {
		t.Fatal("conflicting intent accepted")
	}
	if _, err := e.Dispatch(context.Background(), spec); err == nil {
		t.Fatal("conflicting intent executed")
	}
}

func TestCandidateSnapshotOwnsModeAndModel(t *testing.T) {
	e, _, _, _ := taxonomyEngine()
	original := e.providersFn
	e.providersFn = func() inference.Tiers {
		c := original()
		c.Mode = locus.CloudOnly
		c.ModelFor = func(sel inference.Selection, tier config.Tier) string { return "captured-" + string(tier) }
		return c
	}
	e.modeFn = func() locus.Mode { t.Fatal("independent live mode lookup"); return locus.OpenOnly }
	e.SetDestinationModelFor(func(inference.Selection, config.Tier) string {
		t.Fatal("independent live model lookup")
		return "wrong"
	})
	spec := Spec{Prompt: "fixture"}
	target, err := e.Target(spec)
	if err != nil || target.Model != "captured-everyday" {
		t.Fatalf("Target=%+v err=%v", target, err)
	}
	if target.Profile != "secondary" || target.Destination != "secondary" {
		t.Fatalf("missing selected route metadata: %+v", target)
	}
	target, err = e.PreparedTarget(context.Background(), spec)
	if err != nil || target.Model != "captured-everyday" {
		t.Fatalf("PreparedTarget=%+v err=%v", target, err)
	}
	result, err := e.Dispatch(context.Background(), spec)
	if err != nil || result.Model != "captured-everyday" {
		t.Fatalf("Dispatch=%+v err=%v", result, err)
	}
}

func TestAllTaskClassesQualityAndAgenticRouting(t *testing.T) {
	for _, def := range config.TaskDefinitions() {
		for _, tier := range []config.Tier{"", config.TierFastLight, config.TierEveryday, config.TierMostCapable} {
			e, _, _, _ := taxonomyEngine()
			wantTier := tier
			if wantTier == "" {
				wantTier = def.Default.Quality.CapabilityTier()
				if def.Task == config.TaskDispatch {
					wantTier = config.TierEveryday
				}
			}
			wantDestination := def.Default.Destination
			e.SetAgenticRunner(func(_ context.Context, spec Spec, sel inference.Selection, model string) (Result, error) {
				if spec.RoutingTask != def.Task || spec.Tier != wantTier || sel.Destination != wantDestination {
					t.Errorf("class=%s tier=%s got class=%s tier=%s destination=%s", def.Task, tier, spec.RoutingTask, spec.Tier, sel.Destination)
				}
				return Result{Model: model}, nil
			})
			spec := Spec{RoutingTask: def.Task, Tier: tier, Mode: Agentic, Task: "fixture"}
			target, err := e.Target(spec)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := e.PreparedTarget(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Dispatch(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if result.Model != target.Model || prepared.Model != target.Model || target.Tier != string(wantTier) {
				t.Fatalf("class=%s target=%+v prepared=%+v result=%+v", def.Task, target, prepared, result)
			}
		}
	}
}
