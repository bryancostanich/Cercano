package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestTaskMetadata(t *testing.T) {
	want := []TaskDefinition{
		{TaskChat, "Chat", TaskAssignment{DestinationPrimary, CostStandard}},
		{TaskCompaction, "Compaction", TaskAssignment{DestinationSecondary, CostEconomy}},
		{TaskDispatch, "Default dispatch", TaskAssignment{DestinationSecondary, CostPremium}},
		{TaskReconnaissance, "Reconnaissance", TaskAssignment{DestinationLocal, CostEconomy}},
		{TaskMechanicalDevelopment, "Mechanical development", TaskAssignment{DestinationLocal, CostStandard}},
		{TaskInvestigation, "Investigation", TaskAssignment{DestinationSecondary, CostPremium}},
		{TaskImplementation, "Implementation", TaskAssignment{DestinationSecondary, CostPremium}},
		{TaskReview, "Review", TaskAssignment{DestinationSecondary, CostPremium}},
		{TaskResearch, "Research", TaskAssignment{DestinationSecondary, CostPremium}},
		{TaskGitLand, "Git land", TaskAssignment{DestinationLocal, CostPremium}},
		{TaskWatchdog, "Watchdog", TaskAssignment{DestinationLocal, CostStandard}},
	}
	got := TaskDefinitions()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata=%+v", got)
	}
	got[0].Label = "mutated"
	got[0].Default.Quality = CostEconomy
	if !reflect.DeepEqual(TaskDefinitions(), want) {
		t.Fatal("metadata aliases shared defaults")
	}
	for _, def := range want {
		if !ValidTask(def.Task) || (Config{}).TaskAssignment(def.Task) != def.Default {
			t.Fatalf("metadata disagrees with resolution: %s", def.Task)
		}
	}
	for _, task := range []Task{"", "unknown", "Research"} {
		if ValidTask(task) {
			t.Fatalf("invalid task accepted: %q", task)
		}
		c := Config{TaskAssignments: map[Task]TaskAssignment{task: {DestinationPrimary, CostPremium}}}
		if c.ResolveTask(task, "deep") != (TaskAssignment{}) {
			t.Fatalf("unknown task acquired routing intent: %q", task)
		}
	}
}

func TestTaskMetadataOverridesAndReset(t *testing.T) {
	for _, def := range TaskDefinitions() {
		t.Run(string(def.Task), func(t *testing.T) {
			c := Config{}
			override := TaskAssignment{Destination: DestinationPrimary}
			if err := c.SetTaskAssignment(def.Task, &override); err != nil {
				t.Fatal(err)
			}
			if got := c.TaskAssignment(def.Task); got.Destination != DestinationPrimary || got.Quality != def.Default.Quality {
				t.Fatalf("sparse destination=%+v", got)
			}
			override = TaskAssignment{Quality: CostStandard}
			if err := c.SetTaskAssignment(def.Task, &override); err != nil {
				t.Fatal(err)
			}
			if got := c.TaskAssignment(def.Task); got.Destination != def.Default.Destination || got.Quality != CostStandard {
				t.Fatalf("sparse quality=%+v", got)
			}
			for _, quality := range []struct {
				input string
				want  CostTier
			}{{"light", CostEconomy}, {"standard", CostStandard}, {"deep", CostPremium}, {"", CostStandard}} {
				got := c.ResolveTask(def.Task, quality.input)
				want := quality.want
				if def.Task == TaskChat {
					want = CostStandard
				}
				if got.Destination != def.Default.Destination || got.Quality != want {
					t.Fatalf("explicit %q=%+v want quality %q", quality.input, got, want)
				}
			}
			if err := c.SetTaskAssignment(def.Task, nil); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.TaskAssignments[def.Task]; ok {
				t.Fatal("reset retained override")
			}
			if c.TaskAssignment(def.Task) != def.Default {
				t.Fatal("reset lost defaults")
			}
		})
	}
}

func TestTaskMetadataPersistenceAndAtomicity(t *testing.T) {
	c := Config{}
	for _, def := range TaskDefinitions() {
		a := TaskAssignment{Quality: CostStandard}
		if err := c.SetTaskAssignment(def.Task, &a); err != nil {
			t.Fatal(err)
		}
	}
	before := c.Clone()
	for _, bad := range []struct {
		task Task
		a    TaskAssignment
	}{{"unknown", TaskAssignment{DestinationLocal, CostEconomy}}, {TaskReview, TaskAssignment{Destination: "bogus"}}, {TaskReview, TaskAssignment{Quality: "bogus"}}} {
		if err := c.SetTaskAssignment(bad.task, &bad.a); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
		if !reflect.DeepEqual(c, before) {
			t.Fatal("invalid update mutated config")
		}
	}
	clone := c.Clone()
	delete(clone.TaskAssignments, TaskReview)
	if len(c.TaskAssignments) != len(TaskDefinitions()) {
		t.Fatal("clone aliases original")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(c, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.TaskAssignments, c.TaskAssignments) {
		t.Fatal("persistence expanded sparse overrides or lost task keys")
	}
	for _, def := range TaskDefinitions() {
		if loaded.TaskAssignment(def.Task) != c.TaskAssignment(def.Task) {
			t.Fatalf("persistence changed %q", def.Task)
		}
	}
}
