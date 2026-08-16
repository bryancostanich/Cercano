package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestResumePlanPathsExtractsPlanToolInputs(t *testing.T) {
	blocks := []map[string]any{{
		"type":  "tool_use",
		"name":  "request_plan_approval",
		"input": map[string]any{"plan_path": "efforts/demo/plan.md"},
	}}
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	got := resumePlanPaths([]agentclient.PersistedTurn{{Role: "assistant", ContentJSON: string(raw)}})
	if len(got) != 1 || got[0] != "efforts/demo/plan.md" {
		t.Fatalf("resumePlanPaths = %#v", got)
	}
}

func TestTaskPanePlanRootFromMarkdown(t *testing.T) {
	root, ok := taskPanePlanRootFromMarkdown("efforts/demo/plan.md", `# Demo Effort

## Phase 1
- [ ] Pending task
- [~] Active task
  - [x] Finished child
- [!] Blocked task
`)
	if !ok {
		t.Fatal("expected plan root")
	}
	if root.Title != "Demo Effort" || len(root.Children) != 1 {
		t.Fatalf("root = %+v", root)
	}
	phase := root.Children[0]
	if phase.Title != "Phase 1" || len(phase.Children) != 3 {
		t.Fatalf("phase = %+v", phase)
	}
	if phase.Children[1].Status != "in_progress" || len(phase.Children[1].Children) != 1 || phase.Children[1].Children[0].Status != "done" {
		t.Fatalf("nested status tree = %+v", phase.Children[1])
	}
	if phase.Children[2].Status != "blocked" {
		t.Fatalf("blocked status = %+v", phase.Children[2])
	}
}

func TestHydrateTaskPaneFromResumedTurnsLoadsPlanFile(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join("efforts", "demo", "plan.md")
	abs := filepath.Join(dir, planPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("# Demo\n\n## Phase\n- [ ] Restore me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks := []map[string]any{{
		"type":  "tool_use",
		"name":  "request_plan_approval",
		"input": map[string]any{"plan_path": planPath},
	}}
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	m := New(nil, false)
	m.hydrateTaskPaneFromResumedTurns([]agentclient.PersistedTurn{{Role: "assistant", ContentJSON: string(raw)}})
	if !m.taskPaneHasTasks() {
		t.Fatal("task pane should hydrate tasks from resumed plan")
	}
	lines := m.taskPaneLines(40)
	if len(lines) == 0 || !containsPlainTaskLine(lines, "Restore me") {
		t.Fatalf("hydrated task lines missing restored task: %#v", lines)
	}
}

func containsPlainTaskLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}
