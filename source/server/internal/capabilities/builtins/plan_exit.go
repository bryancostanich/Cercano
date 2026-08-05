package builtins

import (
	"context"
	"fmt"

	"cercano/source/server/internal/capabilities"
)

// planExitCap is the model-invoked, silent exit from planning mode. Unlike
// request_plan_approval, it does NOT ask the human and does NOT require an
// approved plan: it simply leaves the read-only planning profile and returns to
// the unrestricted default.
//
// It exists for the abandon/bail case — the model concluded a plan is not worth
// writing, the effort was scrapped, or planning wound down without a handoff.
// request_plan_approval remains the path when a plan WAS written and the model
// wants execution to begin under human approval.
//
// It is W-tier and exits with no confirm gate: leaving planning mode only drops
// a restriction (it lifts the read-only fence), and the model still hits the
// normal confirm gates for any actual write afterward, so a quiet exit is safe.
// Gating every exit would be pure noise.
type planExitCap struct{}

// PlanExit returns the capability.
func PlanExit() capabilities.Capability { return planExitCap{} }

func (planExitCap) Name() string { return "plan_exit" }

// TierW: no confirm gate. Exiting planning mode only removes a restriction.
func (planExitCap) Tier() capabilities.Tier { return capabilities.TierW }

func (planExitCap) Surfaces() capabilities.Surface {
	// Agent surface only: a session-local profile switch is meaningless over MCP.
	return capabilities.SurfaceAgent
}

func (planExitCap) Description() string {
	return "Exit planning mode without requesting approval. Call this to leave the read-only planning profile when you are abandoning a plan, decided a written plan is not needed, or otherwise want to return to normal operation without the request_plan_approval handoff. Exits silently — no user prompt. Use request_plan_approval instead when you have written a plan and want execution to begin under human approval."
}

func (planExitCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"reason": {"type": "string", "description": "Optional short note on why planning is being exited (e.g. \"plan abandoned\", \"no plan needed\")."}
		}
	}`)
}

func (planExitCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	if call.Svc.EnterProfile == nil {
		return nil, fmt.Errorf("plan_exit: session profile switching is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile("default"); err != nil {
		return nil, fmt.Errorf("plan_exit: leaving planning mode: %w", err)
	}
	return &capabilities.Result{
		Type: capabilities.ResultText,
		Text: "Left planning mode; normal tools are available. No plan was approved for execution.",
	}, nil
}
