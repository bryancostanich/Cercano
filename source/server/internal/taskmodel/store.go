package taskmodel

import "fmt"

// ChangeKind classifies a store mutation for the change-event stream.
type ChangeKind string

const (
	// ChangeAdded is emitted when a task is inserted (root or child).
	ChangeAdded ChangeKind = "added"
	// ChangeUpdated is emitted when a task's mutable fields change in place.
	ChangeUpdated ChangeKind = "updated"
	// ChangeRemoved is emitted when a task (and its subtree) is deleted.
	ChangeRemoved ChangeKind = "removed"
)

// ChangeEvent describes a single mutation to a Store. It is emitted to the sink
// injected at store construction, immediately after the mutation is applied. The
// Task field is a deep-copy snapshot of the affected node in its post-mutation
// state (for ChangeRemoved it is the node as it was just before deletion), so a
// consumer may read it freely without racing further mutations.
//
// This type is wire-agnostic. The server layer maps it onto whatever protocol
// event the clients consume; the store neither knows nor cares how.
type ChangeEvent struct {
	Kind ChangeKind
	Task Task
}

// Store is the single interface both task backends implement over the one Task
// node: the ephemeral session store (a flat working-set forest, dropped at
// session end) and the durable plan store (a Markdown-backed tree). Callers
// mutate tasks only through the Store so that every mutation flows through the
// injected change sink — there is no live-pointer escape hatch across this
// boundary. Reads return deep-copy snapshots for the same reason.
//
// A Store is not required to be safe for concurrent use unless a concrete backend
// documents otherwise; callers should serialize access at the owning layer.
type Store interface {
	// Roots returns snapshots of all root-level tasks in significant order. For
	// the session store this is the flat working set; for the plan store it is
	// the plan root(s). The returned slice and its tasks are copies.
	Roots() []Task

	// Get returns a snapshot of the task with the given ID (searching the whole
	// forest), and true, or the zero Task and false if no such task exists.
	Get(id string) (Task, bool)

	// Add inserts t. If t.ParentID is non-nil it is attached as the last child of
	// that parent; otherwise t becomes a new root. t (and any children it carries)
	// must have IDs unique within the store. On success it emits a single
	// ChangeAdded for t. It returns an error if the ID collides or a named parent
	// is absent.
	Add(t Task) error

	// Update replaces the mutable fields (Title, Status, Notes) of the existing
	// task with t.ID. It does not restructure children or re-parent the node; use
	// Add/Remove for structural change. On success it emits ChangeUpdated. It
	// returns an error if no task has t.ID or t.Status is invalid.
	Update(t Task) error

	// SetStatus is the execution hot path: it flips one task's Status in place.
	// On success it emits ChangeUpdated. It returns an error if the ID is unknown
	// or the status is invalid.
	SetStatus(id string, s Status) error

	// Remove deletes the task with id and its entire subtree. On success it emits
	// a single ChangeRemoved for the removed root (not one per descendant). It
	// returns an error if no task has that ID.
	Remove(id string) error
}

// errNotFound and errDuplicate are shared sentinels backends may wrap.
var (
	errNotFound  = fmt.Errorf("taskmodel: task not found")
	errDuplicate = fmt.Errorf("taskmodel: duplicate task ID")
)
