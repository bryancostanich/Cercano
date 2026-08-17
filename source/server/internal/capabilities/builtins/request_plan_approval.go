package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// requestPlanApprovalCap is the model-invoked handoff out of planning mode.
// The model calls it only after it has authored the effort's spec.md and plan.md
// and is ready for the human to approve execution.
//
// It is X-tier on purpose: the standard tool confirm gate fires BEFORE Execute
// even in Permissive and Bypass modes, giving the user the y/n/d/c prompt. Approving runs Execute, which leaves the
// read-only planning profile (back to the unrestricted default). Declining means
// Execute never runs; the model receives the denial and can revise the plan or
// continue discussing it.
//
// This capability does not implement the execution driver loop. Step 4 is the
// handoff only: approval drops the fence so normal implementation can begin.
// The dedicated execute profile / divergence classifier is step 5.
type requestPlanApprovalCap struct{}

// RequestPlanApproval returns the capability.
func RequestPlanApproval() capabilities.Capability { return requestPlanApprovalCap{} }

func (requestPlanApprovalCap) Name() string { return "request_plan_approval" }

// TierX so the confirm gate is the approval prompt even in Permissive and Bypass modes.
func (requestPlanApprovalCap) Tier() capabilities.Tier { return capabilities.TierX }

func (requestPlanApprovalCap) Surfaces() capabilities.Surface {
	// Agent surface only: approving a session-local plan handoff is meaningless
	// over MCP.
	return capabilities.SurfaceAgent
}

func (requestPlanApprovalCap) Description() string {
	return "Request human approval to leave planning mode and begin executing the written plan. Call this only after you have written the effort's spec.md and plan.md and summarized what will be executed. The user is shown the standard y/n/d/c prompt; on approval the session exits the read-only planning profile so implementation can proceed. After approval, ask the execution-style follow-up by calling request_autonomous_execution with a concise autonomous run brief, or continue step-by-step if the user declines autonomous execution."
}

func (requestPlanApprovalCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["effort", "summary"],
		"properties": {
			"effort": {"type": "string", "description": "Effort directory or slug, e.g. \"efforts/migrate-config-loader\"."},
			"summary": {"type": "string", "description": "Concise human-readable summary of the plan to execute."},
			"plan_path": {"type": "string", "description": "Optional path to the plan file, usually efforts/<slug>/plan.md."},
			"spec_path": {"type": "string", "description": "Optional path to the spec file, usually efforts/<slug>/spec.md."}
		}
	}`)
}

type requestPlanApprovalArgs struct {
	Effort   string `json:"effort"`
	Summary  string `json:"summary"`
	PlanPath string `json:"plan_path"`
	SpecPath string `json:"spec_path"`
}

func (requestPlanApprovalCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	// Reaching Execute means the user approved at the confirm gate.
	var a requestPlanApprovalArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("request_plan_approval: parse args: %w", err)
		}
	}
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("request_plan_approval: session profile switching is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile(call.ConversationID, "default"); err != nil {
		return nil, fmt.Errorf("request_plan_approval: leaving planning mode: %w", err)
	}

	parts := []string{"Plan approved. Left planning mode; normal implementation tools are available. Before beginning implementation, call request_autonomous_execution with a concise autonomous run brief from the approved spec.md/plan.md. That single follow-up asks: \"Plan approved. Execute it autonomously with this run brief?\" If the user says yes, autonomous mode starts from that approval; if no, proceed step-by-step under the executing-plans protocol."}
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
