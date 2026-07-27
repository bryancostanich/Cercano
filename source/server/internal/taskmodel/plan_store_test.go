package taskmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePlanFile writes samplePlan (from markdown_test.go) into a temp effort dir
// and returns the plan.md path.
func writePlanFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "efforts", "migrate-config", "plan.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(samplePlan), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanStore_OpenParsesFile(t *testing.T) {
	path := writePlanFile(t)
	ps, err := OpenPlan(path, nil)
	if err != nil {
		t.Fatalf("OpenPlan: %v", err)
	}
	root := ps.Root()
	if root.Title != "Migrate Config Loader" {
		t.Fatalf("root title = %q", root.Title)
	}
	if len(root.Children) != 2 {
		t.Fatalf("phases = %d, want 2", len(root.Children))
	}
}

func TestPlanStore_OpenMissingFile(t *testing.T) {
	if _, err := OpenPlan(filepath.Join(t.TempDir(), "nope.md"), nil); err == nil {
		t.Fatal("expected error opening a missing plan file")
	}
}

func TestPlanStore_SetStatusWritesThroughAndPersists(t *testing.T) {
	path := writePlanFile(t)
	var events []ChangeEvent
	ps, err := OpenPlan(path, func(e ChangeEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("OpenPlan: %v", err)
	}

	// The last phase-1 task is "Delete the legacy parser", pending. Flip it done.
	phase1 := ps.Root().Children[0]
	target := phase1.Children[2]
	if target.Title != "Delete the legacy parser" || target.Status != StatusPending {
		t.Fatalf("unexpected target task: %+v", target)
	}
	if err := ps.SetStatus(target.ID, StatusDone); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// Event emitted.
	if len(events) != 1 || events[0].Kind != ChangeUpdated || events[0].Task.Status != StatusDone {
		t.Fatalf("events = %+v, want one ChangeUpdated to done", events)
	}

	// The change hit disk: the raw file now shows the done glyph on that line.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- [x] Delete the legacy parser") {
		t.Fatalf("plan.md did not persist the status flip:\n%s", raw)
	}

	// Reopening a fresh store sees the persisted status.
	ps2, err := OpenPlan(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := ps2.Get(target.ID)
	if ok && got.Status != StatusDone {
		t.Fatalf("reopened status = %q, want done", got.Status)
	}
	// (ID is synthesized per parse; if the synthesized ID matched, we asserted;
	// regardless, assert via structure below.)
	reopened := ps2.Root().Children[0].Children[2]
	if reopened.Status != StatusDone {
		t.Fatalf("reopened structural status = %q, want done", reopened.Status)
	}
}

func TestPlanStore_AddRequiresParent(t *testing.T) {
	path := writePlanFile(t)
	ps, err := OpenPlan(path, nil)
	if err != nil {
		t.Fatalf("OpenPlan: %v", err)
	}
	// No ParentID → refused (a plan has one effort root).
	if err := ps.Add(Task{ID: "x", Title: "orphan", Status: StatusPending}); err == nil {
		t.Fatal("expected Add without ParentID to be refused")
	}

	// Add a sub-task under an existing phase-1 task, then confirm it persisted.
	phase1 := ps.Root().Children[0]
	parentID := phase1.Children[2].ID // "Delete the legacy parser"
	if err := ps.Add(Task{ID: "newsub", Title: "confirm nothing references it", Status: StatusPending, ParentID: &parentID}); err != nil {
		t.Fatalf("Add child: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "confirm nothing references it") {
		t.Fatalf("added sub-task not persisted:\n%s", raw)
	}
}

func TestPlanStore_RemoveRootRefused(t *testing.T) {
	path := writePlanFile(t)
	ps, err := OpenPlan(path, nil)
	if err != nil {
		t.Fatalf("OpenPlan: %v", err)
	}
	if err := ps.Remove(ps.Root().ID); err == nil {
		t.Fatal("expected removing the effort root to be refused")
	}
}

func TestPlanStore_RemovePhasePersists(t *testing.T) {
	path := writePlanFile(t)
	ps, err := OpenPlan(path, nil)
	if err != nil {
		t.Fatalf("OpenPlan: %v", err)
	}
	phase2 := ps.Root().Children[1]
	if err := ps.Remove(phase2.ID); err != nil {
		t.Fatalf("Remove phase: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "Phase 2 — Cutover") {
		t.Fatalf("removed phase still present in plan.md:\n%s", raw)
	}
	if !strings.Contains(string(raw), "Phase 1 — Typed loader") {
		t.Fatalf("surviving phase 1 was lost:\n%s", raw)
	}
}

func TestCreatePlan_WritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "efforts", "fresh", "plan.md")
	root := Task{ID: "root", Title: "Fresh Effort", Status: StatusPending}
	phaseID := "root"
	root.Children = []Task{{ID: "p1", Title: "Phase One", Status: StatusPending, ParentID: &phaseID}}

	ps, err := CreatePlan(path, root, nil)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if ps.Path() != path {
		t.Fatalf("Path() = %q", ps.Path())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plan.md not created: %v", err)
	}
	if !strings.Contains(string(raw), "# Fresh Effort") || !strings.Contains(string(raw), "## Phase One") {
		t.Fatalf("CreatePlan wrote unexpected content:\n%s", raw)
	}
}

func TestPlanStore_SatisfiesStore(t *testing.T) {
	// Compile-time assertion lives in plan_store.go; this documents intent.
	var _ Store = (*PlanStore)(nil)
}
