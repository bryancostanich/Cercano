package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

const planSetStatusSample = `# Demo Effort

## Phase 1
Objective: test semantic status updates.

- [ ] First task
- [ ] Duplicate
- [ ] Duplicate
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

func TestPlanSetStatus_Execute_ByTitleWritesThrough(t *testing.T) {
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
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- [~] First task") {
		t.Fatalf("plan.md did not get the in-progress glyph:\n%s", raw)
	}
}

func TestPlanSetStatus_Execute_DuplicateTitleRequiresID(t *testing.T) {
	dir, rel := writePlanSetStatusSample(t)
	args, _ := json.Marshal(map[string]any{
		"plan_path":  rel,
		"task_title": "Duplicate",
		"status":     "done",
	})
	_, err := PlanSetStatus().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous-title error, got %v", err)
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
