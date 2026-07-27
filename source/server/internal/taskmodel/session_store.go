package taskmodel

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
// durable, file-backed plan store (PlanStore) is a separate Store implementation.
//
// SessionStore is a thin wrapper over the shared forest core with no persist
// hook — mutations touch memory only. It is not safe for concurrent use; the
// owning layer must serialize access. Every mutation, after it is applied, is
// emitted to the sink injected at construction — that single seam is how task
// changes reach the turn broker and, through it, every attached client (§11).
type SessionStore struct {
	f *forest
}

// NewSessionStore returns an empty session store that emits every mutation to
// sink. A nil sink is allowed and makes emission a no-op — useful in tests and in
// any context with no live clients attached.
func NewSessionStore(sink func(ChangeEvent)) *SessionStore {
	return &SessionStore{f: newForest(sink, nil)}
}

// Roots returns deep-copy snapshots of the root tasks in order.
func (s *SessionStore) Roots() []Task { return s.f.snapshotRoots() }

// Get returns a snapshot of the task with id, or (zero, false).
func (s *SessionStore) Get(id string) (Task, bool) { return s.f.get(id) }

// Add inserts t as a new root (t.ParentID == nil) or as the last child of an
// existing parent. t and every task in its subtree must have non-empty IDs unique
// within the store. On success it emits a single ChangeAdded.
func (s *SessionStore) Add(t Task) error { return s.f.add(t) }

// Update replaces the mutable fields (Title, Status, Notes) of the task with
// t.ID. Structure is left untouched. On success it emits ChangeUpdated.
func (s *SessionStore) Update(t Task) error { return s.f.update(t) }

// SetStatus flips one task's status in place. On success it emits ChangeUpdated.
func (s *SessionStore) SetStatus(id string, status Status) error { return s.f.setStatus(id, status) }

// Remove deletes the task with id and its whole subtree, root or nested. On
// success it emits a single ChangeRemoved.
func (s *SessionStore) Remove(id string) error { return s.f.remove(id) }

// compile-time assertion that SessionStore satisfies Store.
var _ Store = (*SessionStore)(nil)
