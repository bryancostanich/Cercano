package taskmodel

import (
	"fmt"
	"os"
	"path/filepath"
)

// PlanStore is the durable, file-backed task backend for an effort's plan.md. It
// is the persistent counterpart to SessionStore: where the session store lives
// and dies with a conversation, the plan store is the working form of a Markdown
// file on disk, and — per planning-mode design §3.3 — the file is canon.
//
// A PlanStore holds exactly one root (the effort), parsed from plan.md at
// construction, with phases / tasks / sub-tasks below it. Every mutation is
// applied to the in-memory tree and then written straight through to plan.md
// before the change event is emitted, so the file stays continuously in sync and
// a failed write aborts the mutation rather than emitting a change that never
// reached disk. Reads are served from the in-memory tree.
//
// Staleness caveat (accepted by design §3.3): if a human hand-edits plan.md while
// a PlanStore is live, the in-memory tree goes stale until the store is
// reconstructed. The design mitigates this structurally by assigning plan.md to
// the machine and spec.md to the human; PlanStore does not watch the file.
//
// PlanStore is not safe for concurrent use; the owning layer serializes access.
type PlanStore struct {
	path string
	f    *forest
}

// OpenPlan opens the plan.md at path into a PlanStore, parsing it into the task
// tree and emitting subsequent mutations to sink (nil sink → no-op emission). It
// returns an error if the file is missing, unreadable, or not valid
// Conductor-format Markdown. Use CreatePlan to start a fresh effort.
func OpenPlan(path string, sink func(ChangeEvent)) (*PlanStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("taskmodel: opening plan %q: %w", path, err)
	}
	root, err := ParsePlan(string(data))
	if err != nil {
		return nil, fmt.Errorf("taskmodel: parsing plan %q: %w", path, err)
	}
	return newPlanStore(path, root, sink)
}

// CreatePlan initializes a new plan.md at path from root (the effort), creating
// parent directories as needed, and returns a PlanStore over it. It returns an
// error if root is structurally invalid or the file cannot be written.
func CreatePlan(path string, root Task, sink func(ChangeEvent)) (*PlanStore, error) {
	if err := root.Validate(); err != nil {
		return nil, fmt.Errorf("taskmodel: invalid effort root: %w", err)
	}
	ps, err := newPlanStore(path, root, sink)
	if err != nil {
		return nil, err
	}
	if err := ps.write(); err != nil {
		return nil, err
	}
	return ps, nil
}

// newPlanStore builds a PlanStore whose forest holds root as its single tree and
// whose persist hook write-throughs to path. It indexes the tree up front so ID
// collisions are caught by later mutations.
func newPlanStore(path string, root Task, sink func(ChangeEvent)) (*PlanStore, error) {
	ps := &PlanStore{path: path}
	ps.f = newForest(sink, ps.write)
	ps.f.roots = []Task{root}
	if err := ps.f.reindex(); err != nil {
		return nil, fmt.Errorf("taskmodel: indexing plan %q: %w", path, err)
	}
	return ps, nil
}

// write serializes the current tree to plan.md atomically (temp file + rename) so
// a crash mid-write can never leave a half-written canon file. It is the forest's
// persist hook.
func (s *PlanStore) write() error {
	if len(s.f.roots) == 0 {
		// An effort must retain its root; refuse to erase it via a Remove of the root.
		return fmt.Errorf("taskmodel: refusing to write an empty plan (effort root removed)")
	}
	md := SerializePlan(s.f.roots[0])
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("taskmodel: creating plan dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".plan-*.md.tmp")
	if err != nil {
		return fmt.Errorf("taskmodel: creating temp plan: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.WriteString(md); err != nil {
		tmp.Close()
		return fmt.Errorf("taskmodel: writing temp plan: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("taskmodel: closing temp plan: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("taskmodel: replacing plan file: %w", err)
	}
	return nil
}

// Path returns the plan.md path backing this store.
func (s *PlanStore) Path() string { return s.path }

// Root returns a snapshot of the effort root (the single tree the plan holds).
func (s *PlanStore) Root() Task { return s.f.roots[0].Clone() }

// Roots returns deep-copy snapshots of the root tasks (for a plan, the single
// effort root) in order.
func (s *PlanStore) Roots() []Task { return s.f.snapshotRoots() }

// Get returns a snapshot of the task with id, or (zero, false).
func (s *PlanStore) Get(id string) (Task, bool) { return s.f.get(id) }

// Add inserts t as the last child of a named parent (t.ParentID must name a task
// in the plan). Adding a new root is refused — an effort has exactly one root.
// On success it write-throughs to plan.md and emits ChangeAdded.
func (s *PlanStore) Add(t Task) error {
	if t.ParentID == nil {
		return fmt.Errorf("taskmodel: a plan has a single effort root; Add requires a ParentID")
	}
	return s.f.add(t)
}

// Update replaces the mutable fields (Title, Status, Notes) of the task with
// t.ID, write-throughs to plan.md, and emits ChangeUpdated.
func (s *PlanStore) Update(t Task) error { return s.f.update(t) }

// SetStatus flips one task's status in place — the execution hot path — writing
// the glyph change through to plan.md and emitting ChangeUpdated.
func (s *PlanStore) SetStatus(id string, status Status) error { return s.f.setStatus(id, status) }

// Remove deletes a task and its subtree. Removing the effort root is refused (see
// write); on success it write-throughs to plan.md and emits ChangeRemoved.
func (s *PlanStore) Remove(id string) error {
	if len(s.f.roots) > 0 && s.f.roots[0].ID == id {
		return fmt.Errorf("taskmodel: cannot remove the effort root %q from a plan", id)
	}
	return s.f.remove(id)
}

// compile-time assertion that PlanStore satisfies Store.
var _ Store = (*PlanStore)(nil)
