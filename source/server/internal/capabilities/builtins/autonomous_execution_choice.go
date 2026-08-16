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
	Effort   string `json:"effort"`
	Summary  string `json:"summary"`
	PlanPath string `json:"plan_path"`
	SpecPath string `json:"spec_path"`
}

// RequestAutonomousExecution asks whether an approved plan should be executed
// hands-off. It is a second approval boundary after request_plan_approval: yes
// means draft a lightweight brief and call suggest_autonomous; no means proceed
// step-by-step under executing-plans.
func RequestAutonomousExecution() capabilities.Capability { return requestAutonomousExecutionCap{} }

func (requestAutonomousExecutionCap) Name() string            { return "request_autonomous_execution" }
func (requestAutonomousExecutionCap) Tier() capabilities.Tier { return capabilities.TierX }
func (requestAutonomousExecutionCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent
}
func (requestAutonomousExecutionCap) Description() string {
	return "After request_plan_approval succeeds, ask the user whether to execute the approved plan to completion autonomously. The user is shown a y/n/d/c prompt. On yes, draft a lightweight autonomous run brief from spec.md/plan.md and call suggest_autonomous for brief approval; on no, continue step-by-step with human approval."
}
func (requestAutonomousExecutionCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"effort":{"type":"string","description":"Effort directory or slug, e.g. efforts/migrate-config-loader."},
			"summary":{"type":"string","description":"Concise summary of the approved plan."},
			"plan_path":{"type":"string","description":"Path to the approved plan.md."},
			"spec_path":{"type":"string","description":"Path to the approved spec.md."}
		}
	}`)
}
func (requestAutonomousExecutionCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a requestAutonomousExecutionArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("request_autonomous_execution: parse args: %w", err)
		}
	}
	parts := []string{"User approved autonomous execution as the execution style for this plan. Draft a concise autonomous run brief from the approved spec.md/plan.md, then call suggest_autonomous for the separate run-brief approval before entering autonomous mode."}
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
