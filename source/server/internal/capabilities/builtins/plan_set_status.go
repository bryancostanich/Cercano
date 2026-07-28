package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/taskmodel"
)

// planSetStatusCap is the semantic status-update primitive for executing an
// approved plan. It mutates plan.md through taskmodel.PlanStore rather than raw
// text edits, so the Markdown stays codec-valid and status glyphs are written
// atomically.
//
// Task identity is intentionally human-readable: callers should target tasks by
// phase_title plus task_title/task_path from the Markdown. The plan file must not
// grow machine IDs just so tools can find things. Internally, PlanStore still
// uses synthesized IDs after parsing, but those remain an implementation detail.
type planSetStatusCap struct{}

func PlanSetStatus() capabilities.Capability { return planSetStatusCap{} }

func (planSetStatusCap) Name() string { return "plan_set_status" }

// TierW: mutates plan.md. In the default Permissive mode this is frictionless,
// which is what execution needs; Strict mode still prompts for W-tier changes.
func (planSetStatusCap) Tier() capabilities.Tier { return capabilities.TierW }

func (planSetStatusCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (planSetStatusCap) Description() string {
	return "Update one task's status in an approved plan.md using the semantic task store, not raw text edits. Use this while executing a plan to mark tasks in_progress before starting, done when complete, or blocked when execution cannot proceed. Target tasks using human-readable Markdown structure: phase_title plus task_title, or phase_title plus task_path for nested tasks. Do not add machine IDs to plan.md."
}

func (planSetStatusCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["plan_path", "status"],
		"properties": {
			"plan_path": {"type": "string", "description": "Path to the plan.md file, usually efforts/<slug>/plan.md."},
			"phase_title": {"type": "string", "description": "Optional exact phase heading from the Markdown. Strongly recommended to disambiguate human-readable task titles."},
			"task_title": {"type": "string", "description": "Exact task title from the Markdown. Use with phase_title when possible. Required when task_path and task_id are omitted."},
			"task_path": {"type": "array", "items": {"type": "string"}, "description": "Optional title path to a nested task under phase_title, e.g. [\"Delete legacy parser\", \"Confirm no references\"]. Preferred for subtasks."},
			"task_id": {"type": "string", "description": "Compatibility escape hatch if a rendered task tree exposes an internal ID. Do not add IDs to plan.md; prefer phase_title/task_title/task_path."},
			"status": {"type": "string", "enum": ["pending", "in_progress", "done", "blocked"], "description": "New explicit status."}
		}
	}`)
}

type planSetStatusArgs struct {
	PlanPath   string   `json:"plan_path"`
	PhaseTitle string   `json:"phase_title"`
	TaskTitle  string   `json:"task_title"`
	TaskPath   []string `json:"task_path"`
	TaskID     string   `json:"task_id"`
	Status     string   `json:"status"`
}

func (planSetStatusCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a planSetStatusArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("plan_set_status: parse args: %w", err)
	}
	planPath := strings.TrimSpace(a.PlanPath)
	if planPath == "" {
		return nil, fmt.Errorf("plan_set_status: plan_path is required")
	}
	status := taskmodel.Status(strings.TrimSpace(a.Status))
	if !status.Valid() {
		return nil, fmt.Errorf("plan_set_status: invalid status %q", a.Status)
	}

	resolved := resolvePath(call.WorkDir, planPath)
	store, err := taskmodel.OpenPlan(resolved, nil)
	if err != nil {
		return nil, err
	}
	taskID, before, err := resolvePlanTask(store, planTaskSelector{
		ID:         strings.TrimSpace(a.TaskID),
		PhaseTitle: strings.TrimSpace(a.PhaseTitle),
		TaskTitle:  strings.TrimSpace(a.TaskTitle),
		TaskPath:   trimStringSlice(a.TaskPath),
	}, planPath)
	if err != nil {
		return nil, err
	}
	if err := store.SetStatus(taskID, status); err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: fmt.Sprintf("Updated %q: %s → %s (%s)", before.Title, before.Status, status, planPath)}, nil
}

type planTaskSelector struct {
	ID         string
	PhaseTitle string
	TaskTitle  string
	TaskPath   []string
}

func resolvePlanTask(store *taskmodel.PlanStore, sel planTaskSelector, planPath string) (string, taskmodel.Task, error) {
	// Compatibility escape hatch only. It remains useful for internal callers or
	// a future task pane, but the normal model-facing path is title/structure.
	if sel.ID != "" {
		before, ok := store.Get(sel.ID)
		if !ok {
			return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task %q not found in %s", sel.ID, planPath)
		}
		return sel.ID, before, nil
	}

	roots := store.Roots()
	if len(roots) == 0 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: plan %s has no root", planPath)
	}

	start := roots
	if sel.PhaseTitle != "" {
		phase, err := findUniqueByTitle(roots[0].Children, sel.PhaseTitle, "phase", planPath)
		if err != nil {
			return "", taskmodel.Task{}, err
		}
		start = phase.Children
	}

	if len(sel.TaskPath) > 0 {
		return resolveTaskPath(start, sel.TaskPath, planPath)
	}
	if sel.TaskTitle == "" {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task_title or task_path is required (task IDs are internal; do not add IDs to plan.md)")
	}
	if sel.PhaseTitle != "" {
		n, err := findUniqueByTitleDeep(start, sel.TaskTitle, "task", planPath)
		if err != nil {
			return "", taskmodel.Task{}, err
		}
		return n.ID, n, nil
	}

	// Bare task_title is allowed only when globally unique across the whole plan.
	var matches []taskmodel.Task
	for _, root := range roots {
		_ = root.Walk(func(n *taskmodel.Task) error {
			if n.Title == sel.TaskTitle {
				matches = append(matches, n.Clone())
			}
			return nil
		})
	}
	if len(matches) == 0 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task title %q not found in %s", sel.TaskTitle, planPath)
	}
	if len(matches) > 1 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task title %q is ambiguous in %s; provide phase_title or task_path", sel.TaskTitle, planPath)
	}
	return matches[0].ID, matches[0], nil
}

func resolveTaskPath(nodes []taskmodel.Task, path []string, planPath string) (string, taskmodel.Task, error) {
	if len(path) == 0 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: empty task_path")
	}
	cur, err := findUniqueByTitle(nodes, path[0], "task", planPath)
	if err != nil {
		return "", taskmodel.Task{}, err
	}
	for _, title := range path[1:] {
		cur, err = findUniqueByTitle(cur.Children, title, "task", planPath)
		if err != nil {
			return "", taskmodel.Task{}, err
		}
	}
	return cur.ID, cur, nil
}

func findUniqueByTitle(nodes []taskmodel.Task, title, kind, planPath string) (taskmodel.Task, error) {
	var matches []taskmodel.Task
	for _, n := range nodes {
		if n.Title == title {
			matches = append(matches, n.Clone())
		}
	}
	if len(matches) == 0 {
		return taskmodel.Task{}, fmt.Errorf("plan_set_status: %s title %q not found in %s", kind, title, planPath)
	}
	if len(matches) > 1 {
		return taskmodel.Task{}, fmt.Errorf("plan_set_status: %s title %q is ambiguous in %s; provide more structural context", kind, title, planPath)
	}
	return matches[0], nil
}

func findUniqueByTitleDeep(nodes []taskmodel.Task, title, kind, planPath string) (taskmodel.Task, error) {
	var matches []taskmodel.Task
	for i := range nodes {
		_ = nodes[i].Walk(func(n *taskmodel.Task) error {
			if n.Title == title {
				matches = append(matches, n.Clone())
			}
			return nil
		})
	}
	if len(matches) == 0 {
		return taskmodel.Task{}, fmt.Errorf("plan_set_status: %s title %q not found in %s", kind, title, planPath)
	}
	if len(matches) > 1 {
		return taskmodel.Task{}, fmt.Errorf("plan_set_status: %s title %q is ambiguous in %s; provide task_path", kind, title, planPath)
	}
	return matches[0], nil
}

func trimStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
