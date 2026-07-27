package taskmodel

import (
	"errors"
	"testing"
)

// recorder captures change events for assertions.
type recorder struct{ events []ChangeEvent }

func (r *recorder) sink(ev ChangeEvent) { r.events = append(r.events, ev) }

func pending(id, title string) Task {
	return Task{ID: id, Title: title, Status: StatusPending}
}

func TestSessionStore_AddRoot_EmitsAdded(t *testing.T) {
	rec := &recorder{}
	s := NewSessionStore(rec.sink)

	if err := s.Add(pending("a", "first")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	roots := s.Roots()
	if len(roots) != 1 || roots[0].ID != "a" {
		t.Fatalf("roots = %+v, want single root a", roots)
	}
	if roots[0].ParentID != nil {
		t.Fatalf("root ParentID = %v, want nil", roots[0].ParentID)
	}
	if len(rec.events) != 1 || rec.events[0].Kind != ChangeAdded || rec.events[0].Task.ID != "a" {
		t.Fatalf("events = %+v, want one ChangeAdded for a", rec.events)
	}
}

func TestSessionStore_AddChild_AttachesAndSetsParent(t *testing.T) {
	rec := &recorder{}
	s := NewSessionStore(rec.sink)
	mustAdd(t, s, pending("p", "parent"))

	child := pending("c", "child")
	child.ParentID = ptr("p")
	if err := s.Add(child); err != nil {
		t.Fatalf("Add child: %v", err)
	}

	got, ok := s.Get("c")
	if !ok {
		t.Fatal("child not found")
	}
	if got.ParentID == nil || *got.ParentID != "p" {
		t.Fatalf("child ParentID = %v, want p", got.ParentID)
	}
	// child must live under the parent, not at root.
	if len(s.Roots()) != 1 {
		t.Fatalf("roots = %d, want 1 (child is nested)", len(s.Roots()))
	}
	if got := s.Roots()[0].Children; len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("parent children = %+v, want [c]", got)
	}
}

func TestSessionStore_AddDuplicateID_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	mustAdd(t, s, pending("dup", "one"))
	err := s.Add(pending("dup", "two"))
	if !errors.Is(err, errDuplicate) {
		t.Fatalf("err = %v, want errDuplicate", err)
	}
	if len(s.Roots()) != 1 {
		t.Fatalf("duplicate leaked into store: %d roots", len(s.Roots()))
	}
}

func TestSessionStore_AddMissingParent_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	orphan := pending("o", "orphan")
	orphan.ParentID = ptr("nope")
	if err := s.Add(orphan); err == nil {
		t.Fatal("Add with absent parent should fail")
	}
	if _, ok := s.Get("o"); ok {
		t.Fatal("failed Add must not insert the task")
	}
}

func TestSessionStore_AddInvalidStatus_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	bad := Task{ID: "x", Title: "bad", Status: "nonsense"}
	if err := s.Add(bad); err == nil {
		t.Fatal("Add with invalid status should fail")
	}
}

func TestSessionStore_GetReturnsSnapshot(t *testing.T) {
	s := NewSessionStore(nil)
	mustAdd(t, s, pending("a", "orig"))

	got, _ := s.Get("a")
	got.Title = "mutated copy"
	got.Status = StatusDone

	again, _ := s.Get("a")
	if again.Title != "orig" || again.Status != StatusPending {
		t.Fatalf("snapshot mutation leaked into store: %+v", again)
	}
}

func TestSessionStore_Update_ReplacesMutableFields(t *testing.T) {
	rec := &recorder{}
	s := NewSessionStore(rec.sink)
	mustAdd(t, s, pending("a", "old"))
	rec.events = nil

	upd := Task{ID: "a", Title: "new", Status: StatusInProgress, Notes: "why"}
	if err := s.Update(upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get("a")
	if got.Title != "new" || got.Status != StatusInProgress || got.Notes != "why" {
		t.Fatalf("update not applied: %+v", got)
	}
	if len(rec.events) != 1 || rec.events[0].Kind != ChangeUpdated {
		t.Fatalf("events = %+v, want one ChangeUpdated", rec.events)
	}
}

func TestSessionStore_Update_UnknownID_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	if err := s.Update(pending("ghost", "x")); !errors.Is(err, errNotFound) {
		t.Fatalf("err = %v, want errNotFound", err)
	}
}

func TestSessionStore_SetStatus_DoneTaskPersists(t *testing.T) {
	rec := &recorder{}
	s := NewSessionStore(rec.sink)
	mustAdd(t, s, pending("a", "task"))
	rec.events = nil

	if err := s.SetStatus("a", StatusDone); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	// Design §9: completed session tasks stay visible, not auto-cleared.
	got, ok := s.Get("a")
	if !ok {
		t.Fatal("done task was removed; it must stay visible")
	}
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if len(rec.events) != 1 || rec.events[0].Kind != ChangeUpdated {
		t.Fatalf("events = %+v, want one ChangeUpdated", rec.events)
	}
}

func TestSessionStore_SetStatus_Invalid_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	mustAdd(t, s, pending("a", "task"))
	if err := s.SetStatus("a", "bogus"); err == nil {
		t.Fatal("SetStatus with invalid status should fail")
	}
}

func TestSessionStore_RemoveRoot(t *testing.T) {
	rec := &recorder{}
	s := NewSessionStore(rec.sink)
	mustAdd(t, s, pending("a", "one"))
	mustAdd(t, s, pending("b", "two"))
	rec.events = nil

	if err := s.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("a still present after Remove")
	}
	if len(s.Roots()) != 1 || s.Roots()[0].ID != "b" {
		t.Fatalf("roots = %+v, want [b]", s.Roots())
	}
	if len(rec.events) != 1 || rec.events[0].Kind != ChangeRemoved || rec.events[0].Task.ID != "a" {
		t.Fatalf("events = %+v, want one ChangeRemoved for a", rec.events)
	}
	// ID must be reusable after removal.
	if err := s.Add(pending("a", "reused")); err != nil {
		t.Fatalf("reusing removed ID failed: %v", err)
	}
}

func TestSessionStore_RemoveNestedChild(t *testing.T) {
	s := NewSessionStore(nil)
	mustAdd(t, s, pending("p", "parent"))
	c := pending("c", "child")
	c.ParentID = ptr("p")
	mustAdd(t, s, c)
	gc := pending("gc", "grandchild")
	gc.ParentID = ptr("c")
	mustAdd(t, s, gc)

	if err := s.Remove("c"); err != nil {
		t.Fatalf("Remove nested: %v", err)
	}
	// Both c and its grandchild should be gone; parent survives.
	if _, ok := s.Get("c"); ok {
		t.Fatal("c still present")
	}
	if _, ok := s.Get("gc"); ok {
		t.Fatal("grandchild survived parent removal")
	}
	if _, ok := s.Get("p"); !ok {
		t.Fatal("parent wrongly removed")
	}
	if got := s.Roots()[0].Children; len(got) != 0 {
		t.Fatalf("parent still has children: %+v", got)
	}
	// grandchild ID must be reclaimable.
	if err := s.Add(pending("gc", "reused")); err != nil {
		t.Fatalf("reusing subtree ID failed: %v", err)
	}
}

func TestSessionStore_RemoveUnknown_Fails(t *testing.T) {
	s := NewSessionStore(nil)
	if err := s.Remove("ghost"); !errors.Is(err, errNotFound) {
		t.Fatalf("err = %v, want errNotFound", err)
	}
}

func TestSessionStore_NilSink_NoPanic(t *testing.T) {
	s := NewSessionStore(nil)
	if err := s.Add(pending("a", "x")); err != nil {
		t.Fatalf("Add with nil sink: %v", err)
	}
	if err := s.SetStatus("a", StatusDone); err != nil {
		t.Fatalf("SetStatus with nil sink: %v", err)
	}
	if err := s.Remove("a"); err != nil {
		t.Fatalf("Remove with nil sink: %v", err)
	}
}

func TestSessionStore_AddSubtree_IndexesAllIDs(t *testing.T) {
	s := NewSessionStore(nil)
	root := pending("r", "root")
	child := pending("rc", "child")
	child.ParentID = ptr("r")
	root.AddChild(child)

	if err := s.Add(root); err != nil {
		t.Fatalf("Add subtree: %v", err)
	}
	// A colliding ID anywhere in a new subtree must be rejected.
	dupChild := pending("rc", "dup")
	if err := s.Add(dupChild); !errors.Is(err, errDuplicate) {
		t.Fatalf("err = %v, want errDuplicate for nested ID collision", err)
	}
}

func mustAdd(t *testing.T, s *SessionStore, task Task) {
	t.Helper()
	if err := s.Add(task); err != nil {
		t.Fatalf("Add(%s): %v", task.ID, err)
	}
}
