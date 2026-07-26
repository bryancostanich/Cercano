package taskmodel

import "fmt"

// SessionStore is the ephemeral, session-scoped task backend: the ad-hoc working
// set a user or agent jots down mid-conversation ("also, fix the flaky test").
// It holds a flat forest of root-level tasks in significant order. Nesting is
// permitted — a session task may carry children — but the common case is depth-0.
//
// Lifetime is the container's job, not the node's (see design §4): completed
// tasks are NOT auto-cleared — a task marked done stays visible (the CLI renders
// it checked/struck-through in place) so the working session keeps its
// at-a-glance record of what was just finished. The entire store is discarded
// when the session ends; there is no persistence and no Markdown backing. The
// durable, file-backed plan store is a separate Store implementation.
//
// SessionStore is not safe for concurrent use; the owning layer must serialize
// access. Every mutation, after it is applied, is emitted to the sink injected at
// construction — that single seam is how task changes reach the turn broker and,
// through it, every attached client (design §11).
type SessionStore struct {
	roots []Task
	index map[string]struct{} // every ID currently in the forest, for O(1) collision checks
	sink  func(ChangeEvent)
}

// NewSessionStore returns an empty session store that emits every mutation to
// sink. A nil sink is allowed and makes emission a no-op — useful in tests and in
// any context with no live clients attached.
func NewSessionStore(sink func(ChangeEvent)) *SessionStore {
	return &SessionStore{
		index: make(map[string]struct{}),
		sink:  sink,
	}
}

// emit forwards ev to the sink if one is set.
func (s *SessionStore) emit(ev ChangeEvent) {
	if s.sink != nil {
		s.sink(ev)
	}
}

// findLive returns a live pointer to the task with id anywhere in the forest, or
// nil. The pointer is internal to the store and never crosses the interface
// boundary — callers get clones.
func (s *SessionStore) findLive(id string) *Task {
	for i := range s.roots {
		if n := s.roots[i].Find(id); n != nil {
			return n
		}
	}
	return nil
}

// collectIDs records every ID in t's subtree into dst, failing on any collision
// with an ID already present (either in dst or already in the store).
func (s *SessionStore) collectIDs(t *Task, dst map[string]struct{}) error {
	return t.Walk(func(n *Task) error {
		if n.ID == "" {
			return fmt.Errorf("taskmodel: task with title %q has empty ID", n.Title)
		}
		if _, exists := s.index[n.ID]; exists {
			return fmt.Errorf("%w: %q", errDuplicate, n.ID)
		}
		if _, exists := dst[n.ID]; exists {
			return fmt.Errorf("%w: %q", errDuplicate, n.ID)
		}
		dst[n.ID] = struct{}{}
		return nil
	})
}

// Roots returns deep-copy snapshots of the root tasks in order.
func (s *SessionStore) Roots() []Task {
	out := make([]Task, len(s.roots))
	for i := range s.roots {
		out[i] = s.roots[i].Clone()
	}
	return out
}

// Get returns a snapshot of the task with id, or (zero, false).
func (s *SessionStore) Get(id string) (Task, bool) {
	if n := s.findLive(id); n != nil {
		return n.Clone(), true
	}
	return Task{}, false
}

// Add inserts t as a new root (t.ParentID == nil) or as the last child of an
// existing parent (t.ParentID names a present task). t and every task in its
// subtree must have non-empty IDs unique within the store. On success it emits a
// single ChangeAdded carrying a snapshot of the inserted task.
func (s *SessionStore) Add(t Task) error {
	if !t.Status.Valid() {
		return fmt.Errorf("taskmodel: task %q has invalid status %q", t.ID, t.Status)
	}
	// Validate the whole incoming subtree's IDs before touching any state.
	newIDs := make(map[string]struct{})
	if err := s.collectIDs(&t, newIDs); err != nil {
		return err
	}

	if t.ParentID == nil {
		clone := t.Clone()
		clone.ParentID = nil
		s.roots = append(s.roots, clone)
	} else {
		parent := s.findLive(*t.ParentID)
		if parent == nil {
			return fmt.Errorf("taskmodel: parent %q not found for task %q", *t.ParentID, t.ID)
		}
		child := t.Clone()
		parent.AddChild(child) // sets child.ParentID to parent.ID
	}

	for id := range newIDs {
		s.index[id] = struct{}{}
	}
	// Emit a snapshot of the inserted node in its stored form.
	stored, _ := s.Get(t.ID)
	s.emit(ChangeEvent{Kind: ChangeAdded, Task: stored})
	return nil
}

// Update replaces the mutable fields (Title, Status, Notes) of the task with
// t.ID. Structure (Children, ParentID) is left untouched. On success it emits
// ChangeUpdated with a snapshot of the updated node.
func (s *SessionStore) Update(t Task) error {
	if !t.Status.Valid() {
		return fmt.Errorf("taskmodel: task %q has invalid status %q", t.ID, t.Status)
	}
	n := s.findLive(t.ID)
	if n == nil {
		return fmt.Errorf("%w: %q", errNotFound, t.ID)
	}
	n.Title = t.Title
	n.Status = t.Status
	n.Notes = t.Notes
	s.emit(ChangeEvent{Kind: ChangeUpdated, Task: n.Clone()})
	return nil
}

// SetStatus flips one task's status in place. On success it emits ChangeUpdated.
func (s *SessionStore) SetStatus(id string, status Status) error {
	if !status.Valid() {
		return fmt.Errorf("taskmodel: invalid status %q", status)
	}
	n := s.findLive(id)
	if n == nil {
		return fmt.Errorf("%w: %q", errNotFound, id)
	}
	n.Status = status
	s.emit(ChangeEvent{Kind: ChangeUpdated, Task: n.Clone()})
	return nil
}

// Remove deletes the task with id and its whole subtree, whether it is a root or
// a nested child. On success it emits a single ChangeRemoved carrying a snapshot
// of the removed node as it was.
func (s *SessionStore) Remove(id string) error {
	// Root-level removal.
	for i := range s.roots {
		if s.roots[i].ID == id {
			removed := s.roots[i].Clone()
			s.forgetIDs(&s.roots[i])
			s.roots = append(s.roots[:i], s.roots[i+1:]...)
			s.emit(ChangeEvent{Kind: ChangeRemoved, Task: removed})
			return nil
		}
	}
	// Nested removal: find the parent that owns id as a direct child.
	var (
		parent *Task
		childI = -1
	)
	for i := range s.roots {
		_ = s.roots[i].Walk(func(n *Task) error {
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
	s.forgetIDs(&parent.Children[childI])
	parent.Children = append(parent.Children[:childI], parent.Children[childI+1:]...)
	s.emit(ChangeEvent{Kind: ChangeRemoved, Task: removed})
	return nil
}

// forgetIDs drops every ID in t's subtree from the store's index.
func (s *SessionStore) forgetIDs(t *Task) {
	_ = t.Walk(func(n *Task) error {
		delete(s.index, n.ID)
		return nil
	})
}

// compile-time assertion that SessionStore satisfies Store.
var _ Store = (*SessionStore)(nil)
