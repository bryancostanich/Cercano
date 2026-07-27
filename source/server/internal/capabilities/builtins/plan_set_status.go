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
// This is deliberately a narrow operation: execution remains model-driven by the
// executing-plans protocol, but the high-frequency mutation (pending ->
// in_progress -> done/blocked) is a typed, tested store operation.
type planSetStatusCap struct{}

func PlanSetStatus() capabilities.Capability { return planSetStatusCap{} }

func (planSetStatusCap) Name() string { return "plan_set_status" }

// TierW: mutates plan.md. In the default Permissive mode this is frictionless,
// which is what execution needs; Strict mode still prompts for W-tier changes.
func (planSetStatusCap) Tier() capabilities.Tier { return capabilities.TierW }

func (planSetStatusCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (planSetStatusCap) Description() string {
	return "Update one task's status in an approved plan.md using the semantic task store, not raw text edits. Use this while executing a plan to mark tasks in_progress before starting, done when complete, or blocked when execution cannot proceed. The plan_path is usually efforts/<slug>/plan.md. Prefer task_title from the Markdown when calling from normal plan text; task_id is also accepted when a rendered task tree exposes it."
}

func (planSetStatusCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["plan_path", "status"],
		"properties": {
			"plan_path": {"type": "string", "description": "Path to the plan.md file, usually efforts/<slug>/plan.md."},
			"task_id": {"type": "string", "description": "Optional ID of the task to update, if known from the parsed task tree."},
			"task_title": {"type": "string", "description": "Exact task title from the Markdown. Required when task_id is omitted; must match exactly one task."},
			"status": {"type": "string", "enum": ["pending", "in_progress", "done", "blocked"], "description": "New explicit status."}
		}
	}`)
}

type planSetStatusArgs struct {
	PlanPath  string `json:"plan_path"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Status    string `json:"status"`
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
	taskID, before, err := resolvePlanTask(store, strings.TrimSpace(a.TaskID), strings.TrimSpace(a.TaskTitle), planPath)
	if err != nil {
		return nil, err
	}
	if err := store.SetStatus(taskID, status); err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: fmt.Sprintf("Updated %q: %s → %s (%s)", before.Title, before.Status, status, planPath)}, nil
}

func resolvePlanTask(store *taskmodel.PlanStore, id, title, planPath string) (string, taskmodel.Task, error) {
	if id != "" {
		before, ok := store.Get(id)
		if !ok {
			return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task %q not found in %s", id, planPath)
		}
		return id, before, nil
	}
	if title == "" {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task_id or task_title is required")
	}
	var matches []taskmodel.Task
	for _, root := range store.Roots() {
		_ = root.Walk(func(n *taskmodel.Task) error {
			if n.Title == title {
				matches = append(matches, n.Clone())
			}
			return nil
		})
	}
	if len(matches) == 0 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task title %q not found in %s", title, planPath)
	}
	if len(matches) > 1 {
		return "", taskmodel.Task{}, fmt.Errorf("plan_set_status: task title %q is ambiguous in %s; provide task_id", title, planPath)
	}
	return matches[0].ID, matches[0], nil
}
