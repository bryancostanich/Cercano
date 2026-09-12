package agentclient

import (
	"cercano/source/server/pkg/config"
	"testing"
)

func TestTaskTaxonomyClientRoundTrip(t *testing.T) {
	a := &RoutingAssignments{SecondaryRedirect: "local", Tasks: map[string]TaskAssignment{}}
	for _, def := range config.TaskDefinitions() {
		a.Tasks[string(def.Task)] = TaskAssignment{Quality: "standard"}
	}
	clone := a.Clone()
	got := assignmentsFromProto(assignmentsToProto(clone))
	if got.SecondaryRedirect != a.SecondaryRedirect || len(got.Tasks) != len(a.Tasks) {
		t.Fatal("client lost routing shape")
	}
	for task, assignment := range a.Tasks {
		if got.Tasks[task] != assignment {
			t.Fatalf("client lost class %q", task)
		}
	}
	delete(clone.Tasks, string(config.TaskReview))
	if len(a.Tasks) != len(config.TaskDefinitions()) {
		t.Fatal("clone aliases saved assignments")
	}
}
