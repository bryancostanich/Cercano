package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
)

type requestAutonomousExecutionCap struct{}

type requestAutonomousExecutionArgs struct {
	Effort       string   `json:"effort"`
	Summary      string   `json:"summary"`
	PlanPath     string   `json:"plan_path"`
	SpecPath     string   `json:"spec_path"`
	Reason       string   `json:"reason"`
	Goal         string   `json:"goal"`
	DoneWhen     []string `json:"done_when"`
	Constraints  []string `json:"constraints"`
	ReviewPoints []string `json:"review_points"`
}

// RequestAutonomousExecution asks whether an approved plan should be executed
// hands-off under a concrete autonomous run brief. It is the single approval
// boundary for approved-plan autonomous entry: yes saves the autonomy ledger and
// enters autonomous mode; no means proceed step-by-step under executing-plans.
func RequestAutonomousExecution() capabilities.Capability { return requestAutonomousExecutionCap{} }

func (requestAutonomousExecutionCap) Name() string            { return "request_autonomous_execution" }
func (requestAutonomousExecutionCap) Tier() capabilities.Tier { return capabilities.TierX }
func (requestAutonomousExecutionCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent
}
func (requestAutonomousExecutionCap) Description() string {
	return "After request_plan_approval succeeds, ask the user whether to execute the approved plan to completion autonomously under the supplied run brief. The user is shown a single y/n/d/c prompt containing the plan context and brief. On yes, the autonomy ledger is saved and autonomous mode starts; on no, continue step-by-step with human approval."
}
func (requestAutonomousExecutionCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"effort":{"type":"string","description":"Effort directory or slug, e.g. efforts/migrate-config-loader."},
			"summary":{"type":"string","description":"Concise summary of the approved plan."},
			"plan_path":{"type":"string","description":"Path to the approved plan.md."},
			"spec_path":{"type":"string","description":"Path to the approved spec.md."},
			"reason":{"type":"string","description":"Short reason autonomous execution is appropriate for this approved plan."},
			"goal":{"type":"string","description":"One concise goal for the autonomous run."},
			"done_when":{"type":"array","items":{"type":"string"},"description":"Short checklist of completion criteria."},
			"constraints":{"type":"array","items":{"type":"string"},"description":"Boundaries the agent must honor."},
			"review_points":{"type":"array","items":{"type":"string"},"description":"Decision or risk areas to capture for final review."}
		},
		"required":["goal"]
	}`)
}
func (requestAutonomousExecutionCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a requestAutonomousExecutionArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("request_autonomous_execution: parse args: %w", err)
		}
	}
	msg, err := enterAutonomousMode(ctx, call, autonomousEntryRequest{
		Reason:         a.Reason,
		Goal:           a.Goal,
		DoneWhen:       a.DoneWhen,
		Constraints:    a.Constraints,
		ReviewPoints:   a.ReviewPoints,
		SourcePlanPath: a.PlanPath,
		SourceSpecPath: a.SpecPath,
		ErrPrefix:      "request_autonomous_execution",
	})
	if err != nil {
		return nil, err
	}
	parts := []string{"Autonomous execution approved for the plan.", msg}
	if effort := strings.TrimSpace(a.Effort); effort != "" {
		parts = append(parts, "Effort: "+effort)
	}
	if summary := strings.TrimSpace(a.Summary); summary != "" {
		parts = append(parts, "Summary: "+summary)
	}
	if spec := strings.TrimSpace(a.SpecPath); spec != "" {
		parts = append(parts, "Spec: "+spec)
	}
	if plan := strings.TrimSpace(a.PlanPath); plan != "" {
		parts = append(parts, "Plan: "+plan)
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: strings.Join(parts, "\n")}, nil
}
