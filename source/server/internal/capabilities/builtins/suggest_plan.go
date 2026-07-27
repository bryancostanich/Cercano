package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// suggestPlanCap is the model-invoked entrypoint to planning mode. It is how the
// agent proposes "this is large/ambiguous enough to plan before touching
// anything" — the same way it reaches for any other skill: the capability is
// advertised with a natural-language description and the model calls it when the
// situation fits (there is no server-side heuristic; skill selection is the
// model's job, exactly as with dispatch).
//
// Interaction shape: suggest_plan is a W-tier capability, so the tool loop's
// existing confirm gate fires the y/n/d/c prompt BEFORE Execute runs — that
// prompt IS the user-facing suggestion. Approving (y) runs Execute, which flips
// the session into the read-only planning profile; declining (n) means Execute
// never runs and the model is told the user preferred to proceed directly. The
// d (details) and c (chat/compose) keys work because it is the standard gate.
//
// Execute itself does no planning — it only enters the mode. Generation of the
// spec/plan then proceeds under the read-only fence via the ordinary file
// tools. This keeps the capability's single responsibility crisp: "propose, and
// on approval enter, planning mode."
type suggestPlanCap struct{}

// SuggestPlan returns the capability.
func SuggestPlan() capabilities.Capability { return suggestPlanCap{} }

func (suggestPlanCap) Name() string { return "suggest_plan" }

// TierW so the confirm gate fires before Execute — the suggestion prompt.
func (suggestPlanCap) Tier() capabilities.Tier { return capabilities.TierW }

func (suggestPlanCap) Surfaces() capabilities.Surface {
	// Agent surface only: entering a session mode is meaningless over MCP, where
	// there is no interactive session to fence.
	return capabilities.SurfaceAgent
}

func (suggestPlanCap) Description() string {
	return "Propose entering planning mode for a request that is large, ambiguous, or multi-step enough to benefit from a written plan before any changes are made. Call this instead of diving in when the work would span multiple files or phases, when the approach is genuinely uncertain, or when the user would likely want to review a plan first. The user is shown a y/n/d/c prompt to approve; on approval the session enters a read-only exploration mode where you investigate and write an effort spec and plan (spec.md / plan.md) before touching anything. Pass a short `reason` describing why planning would help — it is shown to the user in the prompt. Do NOT call this for small, clear, single-file changes."
}

func (suggestPlanCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"reason": {"type": "string", "description": "Short, human-facing reason planning would help (shown in the approval prompt), e.g. \"spans 4 files across parser and codegen; approach is uncertain\"."},
			"effort": {"type": "string", "description": "Optional short slug for the effort directory under efforts/, e.g. \"migrate-config-loader\". If omitted, one is derived during generation."}
		}
	}`)
}

type suggestPlanArgs struct {
	Reason string `json:"reason"`
	Effort string `json:"effort"`
}

func (suggestPlanCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	// Reaching Execute means the user already approved at the confirm gate.
	var a suggestPlanArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("suggest_plan: parse args: %w", err)
		}
	}

	if call.Svc.EnterProfile == nil {
		// Loudly surface a wiring gap rather than silently pretending we planned.
		return nil, fmt.Errorf("suggest_plan: planning mode is not available (no profile broker wired)")
	}
	if err := call.Svc.EnterProfile("plan"); err != nil {
		return nil, fmt.Errorf("suggest_plan: entering planning mode: %w", err)
	}

	msg := "Entered planning mode (read-only exploration). Pull the `planning-mode` protocol (get_protocol) and follow it: investigate the codebase, then author the effort's spec.md (what & why) and plan.md (phased to-do) before making any changes. Write/exec tools other than file writes are unavailable until the plan is approved."
	if r := strings.TrimSpace(a.Reason); r != "" {
		msg = "Entered planning mode: " + r + ".\n\n" + msg
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: msg}, nil
}
