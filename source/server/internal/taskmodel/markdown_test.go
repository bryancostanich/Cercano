package taskmodel

import (
	"strings"
	"testing"
)

// samplePlan is a representative Conductor-format plan.md covering: effort title
// + context prose, two phases with metadata prose, tasks with mixed statuses,
// nested sub-tasks, and a task with continuation notes.
const samplePlan = `# Migrate Config Loader

This effort moves config parsing off the legacy path onto the typed loader.

## Phase 1 — Typed loader

Objective: introduce the typed loader behind a flag.
Files: config/loader.go, config/loader_test.go
Tests: round-trip of every existing field.

- [x] Define the typed Config struct
- [~] Wire the loader behind CERCANO_TYPED_CONFIG
  - [x] Read the env flag
  - [ ] Fall back to legacy on parse error
    beware: the legacy path swallows errors, log them
- [ ] Delete the legacy parser

## Phase 2 — Cutover

Objective: flip the default and remove the flag.

- [-] Flip default to typed
- [ ] Remove the flag
`

func TestParsePlan_StructureAndStatus(t *testing.T) {
	root, err := ParsePlan(samplePlan)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if root.Title != "Migrate Config Loader" {
		t.Fatalf("root title = %q", root.Title)
	}
	if !strings.Contains(root.Notes, "legacy path onto the typed loader") {
		t.Fatalf("root notes lost context prose: %q", root.Notes)
	}
	if len(root.Children) != 2 {
		t.Fatalf("phases = %d, want 2", len(root.Children))
	}

	p1 := root.Children[0]
	if p1.Title != "Phase 1 — Typed loader" {
		t.Fatalf("phase1 title = %q", p1.Title)
	}
	if !strings.Contains(p1.Notes, "Objective:") || !strings.Contains(p1.Notes, "Files:") {
		t.Fatalf("phase1 metadata prose lost: %q", p1.Notes)
	}
	if len(p1.Children) != 3 {
		t.Fatalf("phase1 tasks = %d, want 3", len(p1.Children))
	}

	// statuses on phase-1 top-level tasks
	if got := p1.Children[0].Status; got != StatusDone {
		t.Fatalf("task0 status = %q, want done", got)
	}
	if got := p1.Children[1].Status; got != StatusInProgress {
		t.Fatalf("task1 status = %q, want in_progress", got)
	}
	if got := p1.Children[2].Status; got != StatusPending {
		t.Fatalf("task2 status = %q, want pending", got)
	}

	// nested sub-tasks under task1
	wire := p1.Children[1]
	if len(wire.Children) != 2 {
		t.Fatalf("wire sub-tasks = %d, want 2", len(wire.Children))
	}
	if wire.Children[0].Status != StatusDone {
		t.Fatalf("sub0 status = %q, want done", wire.Children[0].Status)
	}
	fallback := wire.Children[1]
	if fallback.Status != StatusPending {
		t.Fatalf("sub1 status = %q, want pending", fallback.Status)
	}
	if !strings.Contains(fallback.Notes, "swallows errors") {
		t.Fatalf("sub-task continuation note lost: %q", fallback.Notes)
	}

	// phase 2 blocked task
	p2 := root.Children[1]
	if p2.Children[0].Status != StatusBlocked {
		t.Fatalf("p2 task0 status = %q, want blocked", p2.Children[0].Status)
	}
}

func TestParsePlan_ValidTree(t *testing.T) {
	root, err := ParsePlan(samplePlan)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	// The parsed tree must satisfy the model's own structural invariants:
	// unique IDs, valid status, correct ParentID back-references.
	if err := root.Validate(); err != nil {
		t.Fatalf("parsed tree fails Validate: %v", err)
	}
}

func TestSerializePlan_Idempotent(t *testing.T) {
	// Semantic round-trip contract: Serialize(Parse(Serialize(Parse(x)))) is
	// stable. We normalize by going through one parse+serialize, then assert a
	// second cycle is a fixed point.
	root, err := ParsePlan(samplePlan)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	once := SerializePlan(root)

	root2, err := ParsePlan(once)
	if err != nil {
		t.Fatalf("re-parse of serialized output failed: %v\n---\n%s", err, once)
	}
	twice := SerializePlan(root2)

	if once != twice {
		t.Fatalf("serialize not idempotent.\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

func TestRoundTrip_PreservesEverything(t *testing.T) {
	root, err := ParsePlan(samplePlan)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	md := SerializePlan(root)
	root2, err := ParsePlan(md)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}

	// Compare the two trees on the fields that carry meaning (ignore IDs, which
	// are synthesized per-parse and need only be internally consistent).
	assertSameShape(t, &root, &root2, "root")
}

func assertSameShape(t *testing.T, a, b *Task, path string) {
	t.Helper()
	if a.Title != b.Title {
		t.Fatalf("%s: title %q != %q", path, a.Title, b.Title)
	}
	if a.Status != b.Status {
		t.Fatalf("%s: status %q != %q", path, a.Status, b.Status)
	}
	if strings.TrimSpace(a.Notes) != strings.TrimSpace(b.Notes) {
		t.Fatalf("%s: notes differ:\n  a=%q\n  b=%q", path, a.Notes, b.Notes)
	}
	if len(a.Children) != len(b.Children) {
		t.Fatalf("%s: child count %d != %d", path, len(a.Children), len(b.Children))
	}
	for i := range a.Children {
		assertSameShape(t, &a.Children[i], &b.Children[i], path+"/"+a.Children[i].Title)
	}
}

func TestParsePlan_Errors(t *testing.T) {
	cases := map[string]string{
		"no title":          "some prose\n- [ ] orphan task\n",
		"two titles":        "# One\n\n# Two\n",
		"checkbox no phase": "# Effort\n\n- [ ] task before any phase\n",
		"bad glyph":         "# E\n\n## P\n\n- [?] weird\n",
		"over-indent":       "# E\n\n## P\n\n    - [ ] jumped two levels\n",
	}
	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePlan(md); err == nil {
				t.Fatalf("expected error for %q, got nil", name)
			}
		})
	}
}

func TestParsePlan_EmptyPhaseNoTasks(t *testing.T) {
	md := "# E\n\n## Phase with only prose\n\nObjective: think about it.\n"
	root, err := ParsePlan(md)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(root.Children) != 1 {
		t.Fatalf("phases = %d, want 1", len(root.Children))
	}
	if len(root.Children[0].Children) != 0 {
		t.Fatalf("empty phase should have no task children")
	}
	if !strings.Contains(root.Children[0].Notes, "think about it") {
		t.Fatalf("phase prose lost: %q", root.Children[0].Notes)
	}
}
