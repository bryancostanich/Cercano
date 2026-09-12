package dispatch

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

func TestTaskTaxonomyUnknownRejectedBeforeAssignmentCallbacks(t *testing.T) {
	for _, callback := range []string{"candidate", "engine"} {
		for _, entry := range []string{"target", "prepared", "oneshot", "agentic"} {
			t.Run(callback+"/"+entry, func(t *testing.T) {
				p := &destinationProvider{name: "primary"}
				calls := 0
				assignment := func(config.Task) config.TaskAssignment {
					calls++
					return config.TaskAssignment{Destination: config.DestinationPrimary, Quality: config.CostPremium}
				}
				candidates := Providers{Cloud: p, Destinations: map[config.Destination]inference.Candidate{
					config.DestinationPrimary: {Provider: p, Profile: "primary", IsCloud: true},
				}}
				if callback == "candidate" {
					candidates.TaskFor = assignment
				}
				e := NewEngine(provs(candidates), func() locus.Mode { return locus.CloudPrimary }, nil)
				if callback == "engine" {
					e.SetTaskAssignment(assignment)
				}
				e.SetDestinationModelFor(func(inference.Selection, config.Tier) string { return "primary-model" })
				spec := Spec{RoutingTask: "unknown-explicit-class", Mode: OneShot, Task: "must not run"}
				var err error
				switch entry {
				case "target":
					_, err = e.Target(spec)
				case "prepared":
					_, err = e.PreparedTarget(context.Background(), spec)
				case "oneshot":
					_, err = e.Dispatch(context.Background(), spec)
				case "agentic":
					spec.Mode = Agentic
					_, err = e.Dispatch(context.Background(), spec)
				}
				if err == nil || !strings.Contains(err.Error(), "unknown routing task") {
					t.Errorf("want explicit unknown routing task error, got %v", err)
				}
				if calls != 0 || len(p.calls) != 0 {
					t.Errorf("invalid class reached assignment/provider: callbacks=%d provider=%d", calls, len(p.calls))
				}
			})
		}
	}
}
