package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

const planSetStatusSample = `# Demo Effort

## Phase 1
Objective: test semantic status updates.

- [ ] First task
- [ ] Shared title
- [ ] Parent task
  - [ ] Nested child

## Phase 2
Objective: test structural targeting.

- [ ] Shared title
- [ ] Other task
`

func writePlanSetStatusSample(t *testing.T) (dir, rel string) {
	t.Helper()
	dir = t.TempDir()
	rel = filepath.Join("efforts", "demo", "plan.md")
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(planSetStatusSample), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, rel
}

func TestPlanSetStatus_Meta(t *testing.T) {
	c := PlanSetStatus()
	if c.Name() != "plan_set_status" {
		t.Errorf("Name() = %q", c.Name())
	}
	if c.Tier() != capabilities.TierW {
		t.Errorf("Tier() = %q, want TierW", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("plan_set_status is execution-session-only and should not be exposed over MCP")
	}
}

func TestPlanSetStatus_Execute_ByBareUniqueTitleWritesThrough(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":  rel,
		"task_title": "First task",
		"status":     "in_progress",
	})
	res, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "First task") || !strings.Contains(res.Text, "in_progress") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	raw := readPlanSetStatusFile(t, dir, rel)
	if !strings.Contains(raw, "- [~] First task") {
		t.Fatalf("plan.md did not get the in-progress glyph:\n%s", raw)
	}
}

func TestPlanSetStatus_Execute_EmitsTaskChangeProgress(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":  rel,
		"task_title": "First task",
		"status":     "in_progress",
	})
	var events []agenttools.ProgressEvent
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{
		Args:    args,
		WorkDir: dir,
		EmitProgress: func(ev agenttools.ProgressEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected hydration plus one task-change progress event, got %d", len(events))
	}
	if events[0].TaskChangeKind != "updated" || events[0].TaskSnapshot.Title != "Demo Effort" {
		t.Fatalf("unexpected hydration event: %+v", events[0])
	}
	if len(events[0].TaskSnapshot.Children) != 2 {
		t.Fatalf("hydration should include the full plan tree, got %+v", events[0].TaskSnapshot)
	}
	if events[1].TaskChangeKind != "updated" {
		t.Fatalf("TaskChangeKind = %q, want updated", events[1].TaskChangeKind)
	}
	if events[1].TaskSnapshot.Title != "First task" || events[1].TaskSnapshot.Status != "in_progress" {
		t.Fatalf("unexpected task snapshot: %+v", events[1].TaskSnapshot)
	}
}

func TestPlanSetStatus_Execute_PhaseTitleDisambiguatesTaskTitle(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":   rel,
		"phase_title": "Phase 2",
		"task_title":  "Shared title",
		"status":      "done",
	})
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	raw := readPlanSetStatusFile(t, dir, rel)
	if strings.Count(raw, "- [x] Shared title") != 1 {
		t.Fatalf("expected exactly one Shared title done, got:\n%s", raw)
	}
	phase2 := raw[strings.Index(raw, "## Phase 2"):]
	if !strings.Contains(phase2, "- [x] Shared title") {
		t.Fatalf("Phase 2 Shared title was not the one updated:\n%s", raw)
	}
}

func TestPlanSetStatus_Execute_TaskPathTargetsNestedTask(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":   rel,
		"phase_title": "Phase 1",
		"task_path":   []string{"Parent task", "Nested child"},
		"status":      "blocked",
	})
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	raw := readPlanSetStatusFile(t, dir, rel)
	if !strings.Contains(raw, "  - [-] Nested child") {
		t.Fatalf("nested task did not get blocked glyph:\n%s", raw)
	}
}

func TestPlanSetStatus_Execute_DuplicateBareTitleRequiresContext(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":  rel,
		"task_title": "Shared title",
		"status":     "done",
	})
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err == nil || !strings.Contains(err.Error(), "provide phase_title or task_path") {
		t.Fatalf("expected context-required ambiguity error, got %v", err)
	}
}

func TestPlanSetStatus_Execute_ValidatesStatusAndPath(t *testing.T) {
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: json.RawMessage(`{"plan_path":"p.md","task_title":"x","status":"bogus"}`)})
	if err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("expected invalid-status error, got %v", err)
	}
	_, err = PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: json.RawMessage(`{"status":"done"}`)})
	if err == nil || !strings.Contains(err.Error(), "plan_path is required") {
		t.Fatalf("expected missing-path error, got %v", err)
	}
}

func readPlanSetStatusFile(t *testing.T, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
