package dispatch

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

func taxonomyEngine() (*Engine, *destinationProvider, *destinationProvider, *destinationProvider) {
	primary := &destinationProvider{name: "primary"}
	secondary := &destinationProvider{name: "secondary"}
	local := &destinationProvider{name: "local"}
	cfg := config.Config{
		TaskAssignments: map[config.Task]config.TaskAssignment{
			config.TaskDispatch: {
				Destination: config.DestinationSecondary,
				Quality:     config.CostStandard,
			},
		},
	}
	candidates := Providers{
		Cloud: primary,
		Open:  local,
		Destinations: map[config.Destination]inference.Candidate{
			config.DestinationPrimary: {
				Provider: primary,
				Profile:  "primary",
				IsCloud:  true,
			},
			config.DestinationSecondary: {
				Provider: secondary,
				Profile:  "secondary",
				IsCloud:  true,
			},
		},
		TaskFor: cfg.TaskAssignment,
	}
	e := NewEngine(provs(candidates), func() locus.Mode { return locus.CloudPrimary }, nil)
	e.SetModelFor(func(_ bool, _ config.Tier) string {
		return "legacy"
	})
	e.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string {
		return sel.Provider.Name() + "-" + string(tier)
	})
	return e, primary, secondary, local
}

func TestTaskTaxonomyExplicitDefaultDispatchBaseline(t *testing.T) {
	e, primary, secondary, local := taxonomyEngine()
	spec := Spec{RoutingTask: config.TaskDispatch, Mode: OneShot, Task: "explicit default", Role: RoleMain}
	target, err := e.Target(spec)
	if err != nil || target.Model != "secondary-everyday" {
		t.Fatalf("Target = %+v, err = %v", target, err)
	}
	if _, err := e.Dispatch(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if len(secondary.calls) != 1 || secondary.calls[0].Model != "secondary-everyday" || len(primary.calls)+len(local.calls) != 0 {
		t.Fatalf("wrong route: primary=%v secondary=%v local=%v", primary.calls, secondary.calls, local.calls)
	}
}

func TestTaskTaxonomyMissingClassUsesDefaultDispatch(t *testing.T) {
	e, primary, secondary, local := taxonomyEngine()
	spec := Spec{Mode: OneShot, Task: "ordinary dispatch", Role: RoleMain}
	target, err := e.Target(spec)
	if err != nil {
		t.Errorf("Target: %v", err)
	} else if target.Model != "secondary-everyday" {
		t.Errorf("Target model = %q, want secondary-everyday", target.Model)
	}
	if _, err := e.Dispatch(context.Background(), spec); err != nil {
		t.Errorf("Dispatch: %v", err)
	}
	if len(secondary.calls) != 1 {
		t.Errorf("secondary calls = %d, want 1", len(secondary.calls))
	} else if secondary.calls[0].Model != "secondary-everyday" {
		t.Errorf("secondary model = %q, want secondary-everyday", secondary.calls[0].Model)
	}
	if len(primary.calls) != 0 || len(local.calls) != 0 {
		t.Errorf("unexpected calls: primary=%d local=%d", len(primary.calls), len(local.calls))
	}
}

func TestTaskTaxonomyUnknownExplicitClassRejected(t *testing.T) {
	e, primary, secondary, local := taxonomyEngine()
	spec := Spec{RoutingTask: config.Task("unknown-explicit-class"), Mode: OneShot, Role: RoleMain, Task: "must not execute"}
	if _, err := e.Target(spec); err == nil {
		t.Errorf("Target accepted an unknown explicit routing class")
	}
	if _, err := e.Dispatch(context.Background(), spec); err == nil {
		t.Errorf("Dispatch accepted an unknown explicit routing class")
	}
	if len(primary.calls) != 0 || len(secondary.calls) != 0 || len(local.calls) != 0 {
		t.Errorf("unexpected calls: primary=%d secondary=%d local=%d", len(primary.calls), len(secondary.calls), len(local.calls))
	}
}

func TestTaskTaxonomyLegacyCoprocBaselinePreserved(t *testing.T) {
	e, primary, secondary, local := taxonomyEngine()
	if _, err := e.Dispatch(context.Background(), Spec{Mode: OneShot, Role: RoleCoproc, Task: "excluded baseline"}); err != nil {
		t.Errorf("Dispatch: %v", err)
	}
	if len(local.calls) != 1 {
		t.Errorf("local calls = %d, want 1", len(local.calls))
	} else if local.calls[0].Model != "legacy" {
		t.Errorf("local model = %q, want legacy", local.calls[0].Model)
	}
	if len(primary.calls) != 0 || len(secondary.calls) != 0 {
		t.Errorf("unexpected cloud calls: primary=%d secondary=%d", len(primary.calls), len(secondary.calls))
	}
}
