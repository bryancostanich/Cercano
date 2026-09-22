package config

import "testing"

func TestCompactionTaskDefaults(t *testing.T) {
	task := Task("compaction")
	if !ValidTask(task) {
		t.Fatal("compaction is missing from task routing")
	}
	c := Config{}
	if got := c.TaskAssignment(task); got != (TaskAssignment{DestinationSecondary, CostEconomy}) {
		t.Fatalf("default=%+v", got)
	}
	c.TaskAssignments = map[Task]TaskAssignment{task: {DestinationLocal, CostPremium}}
	if got := c.TaskAssignment(task); got != (TaskAssignment{DestinationLocal, CostPremium}) {
		t.Fatalf("override=%+v", got)
	}
	if err := c.ValidateRouting(); err != nil {
		t.Fatal(err)
	}
}
