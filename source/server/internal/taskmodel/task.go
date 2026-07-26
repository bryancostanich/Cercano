// Package taskmodel is the one true task representation for Cercano.
//
// A task is a task regardless of scope: whether it was jotted down ad-hoc in the
// middle of a conversation ("also, fix the flaky test") or laid out up front as
// part of a plan ("Phase 2, task 3: migrate the config loader"), it is the same
// shape — a title, a status, some notes, and possibly sub-parts. The differences
// people intuit between "my working set for this session" and "the plan" are not
// properties of a task; they are properties of the store that holds it and the
// way the tasks are arranged (flat list vs. tree). So the task is modeled exactly
// once, as a single recursive node.
//
// This package is deliberately wire-agnostic: it imports no proto and knows
// nothing about the CLI or gRPC. Clients are thin; all task logic lives here and
// change notification is bridged by the server layer, not by the store.
//
// See docs/features/agent-capabilities/task-model/design.md for the full design.
package taskmodel

import "fmt"

// Status is the explicit lifecycle state of a task. Status is always set
// directly by the agent or user; it is never inferred from a task's children.
// A parent with children still carries its own explicit status. A UI may display
// a computed rollup (e.g. "3/5 done"), but that rollup is display-only and is
// never persisted here.
type Status string

const (
	// StatusPending is a task that has not been started.
	StatusPending Status = "pending"
	// StatusInProgress is a task actively being worked.
	StatusInProgress Status = "in_progress"
	// StatusDone is a completed task. Completed tasks are not auto-removed; the
	// store that holds them decides their lifetime (see the session store).
	StatusDone Status = "done"
	// StatusBlocked is a task that cannot proceed.
	StatusBlocked Status = "blocked"
)

// Valid reports whether s is one of the four defined statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusDone, StatusBlocked:
		return true
	default:
		return false
	}
}

// Task is the single recursive node that models every task at every scope.
//
// A flat working set is a forest of root-level tasks (ParentID == nil, Children
// empty). A plan is a tree of the same node: plan > phase > task > subtask,
// arbitrarily deep. "Phase" is not a distinct type — it is a Task that happens to
// have children. There is deliberately no scope or lifetime field: lifetime is
// decided by which store holds the task, so the node stays pure.
type Task struct {
	// ID is a stable identifier, unique within the store that holds the task.
	ID string
	// Title is a short imperative description.
	Title string
	// Status is the explicit lifecycle state; see Status.
	Status Status
	// Notes is freeform detail, optional. For plan trees this field also carries
	// phase-level metadata (objective / files-to-touch / tests-to-write) and must
	// survive a Markdown round-trip intact — it is not a scratch field.
	Notes string
	// Children are subtasks, in significant sibling order. Empty for a leaf.
	Children []Task
	// ParentID is the ID of the parent task, or nil for a root-level task.
	ParentID *string
}

// Validate reports the first structural problem with t and its subtree, or nil
// if the task is well-formed. It checks that every node has a non-empty ID and a
// valid status, that IDs are unique within the subtree, and that each child's
// ParentID points at its actual parent.
func (t *Task) Validate() error {
	seen := make(map[string]struct{})
	return t.validate(seen)
}

func (t *Task) validate(seen map[string]struct{}) error {
	if t.ID == "" {
		return fmt.Errorf("task with title %q has empty ID", t.Title)
	}
	if _, dup := seen[t.ID]; dup {
		return fmt.Errorf("duplicate task ID %q", t.ID)
	}
	seen[t.ID] = struct{}{}
	if !t.Status.Valid() {
		return fmt.Errorf("task %q has invalid status %q", t.ID, t.Status)
	}
	for i := range t.Children {
		child := &t.Children[i]
		if child.ParentID == nil || *child.ParentID != t.ID {
			return fmt.Errorf("child %q of %q has mismatched ParentID", child.ID, t.ID)
		}
		if err := child.validate(seen); err != nil {
			return err
		}
	}
	return nil
}

// Walk visits t and every descendant in depth-first, document order (a node
// before its children, children in slice order). The visitor receives a pointer
// to the live node so it may read or mutate it in place. Walk stops and returns
// the first error a visitor returns.
func (t *Task) Walk(visit func(*Task) error) error {
	if err := visit(t); err != nil {
		return err
	}
	for i := range t.Children {
		if err := t.Children[i].Walk(visit); err != nil {
			return err
		}
	}
	return nil
}

// Find returns a pointer to the task with the given ID within t's subtree
// (including t itself), or nil if no such task exists. The returned pointer is
// live: mutating it mutates the tree.
func (t *Task) Find(id string) *Task {
	var found *Task
	_ = t.Walk(func(n *Task) error {
		if n.ID == id {
			found = n
			return errStopWalk
		}
		return nil
	})
	return found
}

// errStopWalk is a sentinel used internally to halt a Walk early. It never
// escapes this package.
var errStopWalk = fmt.Errorf("taskmodel: stop walk")

// AddChild appends child to t's children, setting child.ParentID to t.ID so the
// tree stays internally consistent. It returns a pointer to the newly attached
// child within t's slice.
func (t *Task) AddChild(child Task) *Task {
	id := t.ID
	child.ParentID = &id
	t.Children = append(t.Children, child)
	return &t.Children[len(t.Children)-1]
}
