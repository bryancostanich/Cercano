package dispatch

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
	"context"
	"testing"
)

func TestDestinationRedirectBudgetExecution(t *testing.T) {
	for _, task := range []config.Task{config.TaskDispatch, config.TaskWatchdog} {
		t.Run(string(task), func(t *testing.T) {
			c := config.Config{LocalRedirect: config.DestinationSecondary, SecondaryRedirect: config.DestinationPrimary, TaskAssignments: map[config.Task]config.TaskAssignment{task: {Destination: config.DestinationLocal, Quality: config.CostStandard}}}
			p, s, l := &destinationProvider{name: "primary"}, &destinationProvider{name: "secondary"}, &destinationProvider{name: "local"}
			e := NewEngine(func() inference.Tiers {
				snapshot := c.Clone()
				return inference.Tiers{Cloud: p, Open: l, TaskFor: snapshot.TaskAssignment, ResolveDestination: snapshot.ResolveDestination, Destinations: map[config.Destination]inference.Candidate{config.DestinationPrimary: {Provider: p, Profile: "p", IsCloud: true}, config.DestinationSecondary: {Provider: s, Profile: "s", IsCloud: true}}}
			}, func() locus.Mode { return locus.CloudPrimary }, nil)
			e.SetDestinationModelFor(func(sel inference.Selection, tier config.Tier) string {
				return sel.Provider.Name() + "-" + string(tier)
			})
			check := func(explicit config.Tier, expected string) {
				t.Helper()
				spec := Spec{RoutingTask: task, Mode: OneShot, Task: "fixture", Tier: explicit}
				target, err := e.Target(spec)
				if err != nil || target.Model != expected {
					t.Fatalf("Target=%+v err=%v want %s", target, err, expected)
				}
				prepared, err := e.PreparedTarget(context.Background(), spec)
				if err != nil || prepared.Model != expected {
					t.Fatalf("PreparedTarget=%+v err=%v want %s", prepared, err, expected)
				}
				result, err := e.Dispatch(context.Background(), spec)
				if err != nil || result.Model != expected {
					t.Fatalf("Dispatch=%+v err=%v want %s", result, err, expected)
				}
			}
			check("", "primary-everyday")
			check(config.TierMostCapable, "primary-most_capable")
			if len(p.calls) != 2 || len(s.calls)+len(l.calls) != 0 {
				t.Fatal("wrong chain endpoint")
			}
			c.SecondaryRedirect = ""
			check("", "secondary-everyday")
			c.LocalRedirect = ""
			check("", "local-everyday")
			if len(p.calls) != 2 || len(s.calls) != 1 || len(l.calls) != 1 {
				t.Fatal("redirect settings changes not reflected")
			}
			if a := c.TaskAssignment(task); a.Destination != config.DestinationLocal || a.Quality != config.CostStandard {
				t.Fatalf("saved assignment changed: %+v", a)
			}
		})
	}
}
