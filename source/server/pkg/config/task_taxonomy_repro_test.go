package config

import (
	"reflect"
	"testing"
)

// TestTaskTaxonomy captures the approved task defaults and assignment contract.
func TestTaskTaxonomy(t *testing.T) {
	tests := []struct {
		task Task
		want TaskAssignment
	}{
		{Task("reconnaissance"), TaskAssignment{DestinationLocal, CostEconomy}},
		{Task("mechanical_development"), TaskAssignment{DestinationLocal, CostStandard}},
		{Task("investigation"), TaskAssignment{DestinationSecondary, CostPremium}},
		{Task("implementation"), TaskAssignment{DestinationSecondary, CostPremium}},
		{Task("review"), TaskAssignment{DestinationSecondary, CostPremium}},
		{Task("research"), TaskAssignment{DestinationSecondary, CostPremium}},
		{Task("git_land"), TaskAssignment{DestinationLocal, CostPremium}},
	}
	for _, tt := range tests {
		t.Run(string(tt.task), func(t *testing.T) {
			t.Run("defaults", func(t *testing.T) {
				if got := (Config{}).ResolveTask(tt.task, ""); got != tt.want {
					t.Errorf("ResolveTask(%q) = %+v, want %+v", tt.task, got, tt.want)
				}
			})
			t.Run("assignment", func(t *testing.T) {
				c := Config{}
				if err := c.SetTaskAssignment(tt.task, &tt.want); err != nil {
					t.Fatalf("SetTaskAssignment(%q): %v", tt.task, err)
				}
				if got, ok := c.TaskAssignments[tt.task]; !ok || got != tt.want {
					t.Errorf("saved assignment = %+v (present=%v), want %+v", got, ok, tt.want)
				}
				if got := c.ResolveTask(tt.task, ""); got != tt.want {
					t.Errorf("resolved assignment = %+v, want %+v", got, tt.want)
				}
			})
		})
	}
	t.Run("unknown_rejected_without_mutation", func(t *testing.T) {
		c := Config{TaskAssignments: map[Task]TaskAssignment{
			TaskChat: {DestinationPrimary, CostPremium},
		}}
		before := c.Clone()
		assignment := TaskAssignment{DestinationLocal, CostEconomy}
		if err := c.SetTaskAssignment(Task("unknown_task"), &assignment); err == nil {
			t.Error("SetTaskAssignment accepted unknown task")
		}
		if !reflect.DeepEqual(c, before) {
			t.Error("SetTaskAssignment mutated config while rejecting unknown task")
		}
	})
}
