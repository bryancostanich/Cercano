package dispatch

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// Every agentic dispatch must resolve to an enforced budget unless the caller
// explicitly opted out with the unlimited sentinel.
func TestResolveTokenBudget(t *testing.T) {
	e := &Engine{}
	e.SetTaskAssignment(func(task config.Task) config.TaskAssignment {
		switch task {
		case config.Task("reconnaissance"):
			return config.TaskAssignment{Quality: config.CostEconomy}
		case config.Task("implementation"):
			return config.TaskAssignment{Quality: config.CostPremium}
		}
		return config.TaskAssignment{Quality: config.CostStandard}
	})
	for _, tc := range []struct {
		name string
		spec Spec
		want int
	}{
		{"economy-task-default", Spec{RoutingTask: config.Task("reconnaissance")}, 300_000},
		{"premium-task-default", Spec{RoutingTask: config.Task("implementation")}, 3_000_000},
		{"standard-task-default", Spec{RoutingTask: config.Task("investigation")}, 1_000_000},
		{"explicit-budget-wins", Spec{RoutingTask: config.Task("implementation"), TokenBudget: 42_000}, 42_000},
		{"explicit-unlimited-disables", Spec{RoutingTask: config.Task("implementation"), TokenBudget: config.UnlimitedDispatchTokenBudget}, 0},
		// No routing task (legacy role dispatch): still bounded, never zero.
		{"taskless-still-bounded", Spec{}, 1_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.resolveTokenBudget(tc.spec); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
	// Without any assignment resolver installed the fallback must still bound.
	bare := &Engine{}
	if got := bare.resolveTokenBudget(Spec{}); got <= 0 {
		t.Fatalf("unresolved engine returned unbounded budget: %d", got)
	}
}

func TestCostTierDispatchTokenBudgets(t *testing.T) {
	// The class defaults are product decisions; pin them so a refactor cannot
	// silently unbound dispatches. Scale: healthy recon bills <100K, the
	// motivating runaway billed ~3.7M.
	for tier, want := range map[config.CostTier]int{
		config.CostEconomy:  300_000,
		config.CostStandard: 1_000_000,
		config.CostPremium:  3_000_000,
		config.CostTier(""): 1_000_000,
	} {
		if got := tier.DispatchTokenBudget(); got != want {
			t.Fatalf("%q: got %d want %d", tier, got, want)
		}
	}
}
