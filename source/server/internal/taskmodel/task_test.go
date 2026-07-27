package taskmodel

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func TestStatusValid(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPending, true},
		{StatusInProgress, true},
		{StatusDone, true},
		{StatusBlocked, true},
		{Status(""), false},
		{Status("cancelled"), false},
		{Status("Done"), false}, // case-sensitive
	}
	for _, c := range cases {
		if got := c.status.Valid(); got != c.want {
			t.Errorf("Status(%q).Valid() = %v, want %v", c.status, got, c.want)
		}
	}
}

// sampleTree builds a small, well-formed plan tree:
//
//	root (phase)
//	├── a
//	└── b
//	    └── b1
func sampleTree() *Task {
	root := &Task{ID: "root", Title: "Phase 1", Status: StatusInProgress}
	root.AddChild(Task{ID: "a", Title: "Task A", Status: StatusDone})
	b := root.AddChild(Task{ID: "b", Title: "Task B", Status: StatusPending})
	b.AddChild(Task{ID: "b1", Title: "Subtask B1", Status: StatusBlocked})
	return root
}

func TestValidate(t *testing.T) {
	if err := sampleTree().Validate(); err != nil {
		t.Fatalf("well-formed tree should validate, got: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*Task)
		wantSub string
	}{
		{
			name:    "empty ID",
			mutate:  func(root *Task) { root.Children[0].ID = "" },
			wantSub: "empty ID",
		},
		{
			name:    "invalid status",
			mutate:  func(root *Task) { root.Children[0].Status = "nope" },
			wantSub: "invalid status",
		},
		{
			name:    "duplicate ID",
			mutate:  func(root *Task) { root.Children[1].ID = "a" },
			wantSub: "duplicate task ID",
		},
		{
			name:    "mismatched ParentID",
			mutate:  func(root *Task) { root.Children[0].ParentID = ptr("wrong") },
			wantSub: "mismatched ParentID",
		},
		{
			name:    "nil ParentID on child",
			mutate:  func(root *Task) { root.Children[0].ParentID = nil },
			wantSub: "mismatched ParentID",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := sampleTree()
			c.mutate(tree)
			err := tree.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestWalkOrder(t *testing.T) {
	var order []string
	err := sampleTree().Walk(func(n *Task) error {
		order = append(order, n.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk returned error: %v", err)
	}
	want := []string{"root", "a", "b", "b1"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("walk order = %v, want %v (depth-first, document order)", order, want)
	}
}

func TestWalkStopsOnError(t *testing.T) {
	sentinel := errStopWalk
	var visited int
	err := sampleTree().Walk(func(n *Task) error {
		visited++
		if n.ID == "a" {
			return sentinel
		}
		return nil
	})
	if err != sentinel {
		t.Errorf("Walk should return the visitor's error, got %v", err)
	}
	if visited != 2 { // root, then a
		t.Errorf("Walk should stop at first error; visited %d nodes, want 2", visited)
	}
}

func TestFind(t *testing.T) {
	tree := sampleTree()

	if got := tree.Find("root"); got == nil || got.ID != "root" {
		t.Errorf("Find(root) = %v, want the root node", got)
	}
	if got := tree.Find("b1"); got == nil || got.ID != "b1" {
		t.Errorf("Find(b1) = %v, want the deep child", got)
	}
	if got := tree.Find("nope"); got != nil {
		t.Errorf("Find(nope) = %v, want nil", got)
	}

	// The returned pointer must be live: mutating it mutates the tree.
	tree.Find("b1").Status = StatusDone
	if tree.Children[1].Children[0].Status != StatusDone {
		t.Error("Find must return a live pointer into the tree")
	}
}

func TestAddChildSetsParent(t *testing.T) {
	root := &Task{ID: "root", Title: "root", Status: StatusPending}
	child := root.AddChild(Task{ID: "c", Title: "child", Status: StatusPending})

	if child.ParentID == nil || *child.ParentID != "root" {
		t.Errorf("AddChild should set ParentID to parent's ID, got %v", child.ParentID)
	}
	if len(root.Children) != 1 || root.Children[0].ID != "c" {
		t.Errorf("AddChild should append to Children, got %v", root.Children)
	}
	// Returned pointer must be live within the parent's slice.
	child.Status = StatusDone
	if root.Children[0].Status != StatusDone {
		t.Error("AddChild must return a live pointer into the parent's slice")
	}
}
