package taskmodel

import "fmt"

// forest is the shared, container-agnostic core both stores are built on: a flat
// slice of root Tasks (each possibly a subtree), an ID index for O(1) collision
// checks, a change sink, and a persist hook. It implements every structural
// mutation — Add / Update / SetStatus / Remove — plus lookups, emitting a
// ChangeEvent after each applied mutation.
//
// The only thing that differs between the ephemeral session store and the
// durable plan store is what happens after a mutation is applied to the tree:
// the session store does nothing, the plan store writes plan.md back to disk.
// That difference is the injected persist func — called after the tree changes
// but before the change event is emitted, so a persistence failure aborts the
// mutation and never emits a lie. A nil persist is a no-op.
//
// forest is not safe for concurrent use; the owning Store serializes access.
type forest struct {
	roots   []Task
	index   map[string]struct{}
	sink    func(ChangeEvent)
	persist func() error // nil for in-memory stores; write-through for the plan store
}

func newForest(sink func(ChangeEvent), persist func() error) *forest {
	return &forest{
		index:   make(map[string]struct{}),
		sink:    sink,
		persist: persist,
	}
}

func (f *forest) emit(ev ChangeEvent) {
	if f.sink != nil {
		f.sink(ev)
	}
}

// save runs the persist hook if one is set. Callers invoke it after mutating the
// tree and before emitting the change event.
func (f *forest) save() error {
	if f.persist == nil {
		return nil
	}
	return f.persist()
}

// findLive returns a live pointer to the task with id, or nil. Internal only —
// never crosses the Store boundary.
func (f *forest) findLive(id string) *Task {
	for i := range f.roots {
		if n := f.roots[i].Find(id); n != nil {
			return n
		}
	}
	return nil
}

// collectIDs records every ID in t's subtree into dst, failing on any collision
// with an ID already in the store or already in dst.
func (f *forest) collectIDs(t *Task, dst map[string]struct{}) error {
	return t.Walk(func(n *Task) error {
		if n.ID == "" {
			return fmt.Errorf("taskmodel: task with title %q has empty ID", n.Title)
		}
		if _, exists := f.index[n.ID]; exists {
			return fmt.Errorf("%w: %q", errDuplicate, n.ID)
		}
		if _, exists := dst[n.ID]; exists {
			return fmt.Errorf("%w: %q", errDuplicate, n.ID)
		}
		dst[n.ID] = struct{}{}
		return nil
	})
}

func (f *forest) forgetIDs(t *Task) {
	_ = t.Walk(func(n *Task) error {
		delete(f.index, n.ID)
		return nil
	})
}

// reindex rebuilds the ID index from the current roots. Used after a bulk load
// (e.g. the plan store parsing a file into the forest).
func (f *forest) reindex() error {
	f.index = make(map[string]struct{})
	for i := range f.roots {
		if err := f.roots[i].Walk(func(n *Task) error {
			if n.ID == "" {
				return fmt.Errorf("taskmodel: task with title %q has empty ID", n.Title)
			}
			if _, exists := f.index[n.ID]; exists {
				return fmt.Errorf("%w: %q", errDuplicate, n.ID)
			}
			f.index[n.ID] = struct{}{}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// snapshotRoots returns deep-copy snapshots of the roots in order.
func (f *forest) snapshotRoots() []Task {
	out := make([]Task, len(f.roots))
	for i := range f.roots {
		out[i] = f.roots[i].Clone()
	}
	return out
}

func (f *forest) get(id string) (Task, bool) {
	if n := f.findLive(id); n != nil {
		return n.Clone(), true
	}
	return Task{}, false
}

// add inserts t as a root or as the last child of a named parent, validating the
// whole incoming subtree's IDs first, then persisting, then emitting ChangeAdded.
func (f *forest) add(t Task) error {
	if !t.Status.Valid() {
		return fmt.Errorf("taskmodel: task %q has invalid status %q", t.ID, t.Status)
	}
	newIDs := make(map[string]struct{})
	if err := f.collectIDs(&t, newIDs); err != nil {
		return err
	}

	// Apply to a candidate copy of the affected slice so a persist failure can be
	// rolled back cleanly. Simpler here: mutate, persist, and on persist error undo.
	if t.ParentID == nil {
		clone := t.Clone()
		clone.ParentID = nil
		f.roots = append(f.roots, clone)
		if err := f.save(); err != nil {
			f.roots = f.roots[:len(f.roots)-1]
			return err
		}
	} else {
		parent := f.findLive(*t.ParentID)
		if parent == nil {
			return fmt.Errorf("taskmodel: parent %q not found for task %q", *t.ParentID, t.ID)
		}
		child := t.Clone()
		parent.AddChild(child)
		if err := f.save(); err != nil {
			parent.Children = parent.Children[:len(parent.Children)-1]
			return err
		}
	}

	for id := range newIDs {
		f.index[id] = struct{}{}
	}
	stored, _ := f.get(t.ID)
	f.emit(ChangeEvent{Kind: ChangeAdded, Task: stored})
	return nil
}

func (f *forest) update(t Task) error {
	if !t.Status.Valid() {
		return fmt.Errorf("taskmodel: task %q has invalid status %q", t.ID, t.Status)
	}
	n := f.findLive(t.ID)
	if n == nil {
		return fmt.Errorf("%w: %q", errNotFound, t.ID)
	}
	prevTitle, prevStatus, prevNotes := n.Title, n.Status, n.Notes
	n.Title, n.Status, n.Notes = t.Title, t.Status, t.Notes
	if err := f.save(); err != nil {
		n.Title, n.Status, n.Notes = prevTitle, prevStatus, prevNotes
		return err
	}
	f.emit(ChangeEvent{Kind: ChangeUpdated, Task: n.Clone()})
	return nil
}

func (f *forest) setStatus(id string, status Status) error {
	if !status.Valid() {
		return fmt.Errorf("taskmodel: invalid status %q", status)
	}
	n := f.findLive(id)
	if n == nil {
		return fmt.Errorf("%w: %q", errNotFound, id)
	}
	prev := n.Status
	n.Status = status
	if err := f.save(); err != nil {
		n.Status = prev
		return err
	}
	f.emit(ChangeEvent{Kind: ChangeUpdated, Task: n.Clone()})
	return nil
}

func (f *forest) remove(id string) error {
	// Root-level removal.
	for i := range f.roots {
		if f.roots[i].ID == id {
			removed := f.roots[i].Clone()
			saved := f.roots
			f.roots = append(append([]Task{}, f.roots[:i]...), f.roots[i+1:]...)
			if err := f.save(); err != nil {
				f.roots = saved
				return err
			}
			f.forgetIDs(&removed)
			f.emit(ChangeEvent{Kind: ChangeRemoved, Task: removed})
			return nil
		}
	}
	// Nested removal: find the parent that owns id as a direct child.
	var (
		parent *Task
		childI = -1
	)
	for i := range f.roots {
		_ = f.roots[i].Walk(func(n *Task) error {
			for j := range n.Children {
				if n.Children[j].ID == id {
					parent = n
					childI = j
					return errStopWalk
				}
			}
			return nil
		})
		if parent != nil {
			break
		}
	}
	if parent == nil {
		return fmt.Errorf("%w: %q", errNotFound, id)
	}
	removed := parent.Children[childI].Clone()
	savedChildren := parent.Children
	parent.Children = append(append([]Task{}, parent.Children[:childI]...), parent.Children[childI+1:]...)
	if err := f.save(); err != nil {
		parent.Children = savedChildren
		return err
	}
	f.forgetIDs(&removed)
	f.emit(ChangeEvent{Kind: ChangeRemoved, Task: removed})
	return nil
}
