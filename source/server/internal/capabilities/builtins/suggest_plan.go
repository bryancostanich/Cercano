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
// Interaction shape: suggest_plan is an X-tier capability, so the tool loop's
// existing confirm gate fires the y/n/d/c prompt BEFORE Execute runs even in
// Permissive and Bypass modes — that prompt IS the user-facing suggestion. Approving (y) runs Execute, which flips
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

// TierX so the confirm gate fires before Execute even in Permissive and Bypass
// modes — the suggestion prompt. This capability changes session mode; it must never auto-run.
func (suggestPlanCap) Tier() capabilities.Tier { return capabilities.TierX }

func (suggestPlanCap) Surfaces() capabilities.Surface {
	// Agent surface only: entering a session mode is meaningless over MCP, where
	// there is no interactive session to fence.
	return capabilities.SurfaceAgent
}

func (suggestPlanCap) Description() string {
	return "Default to direct execution for clear, bounded requests: routine bug fixes, text/UI tweaks, straightforward features, mechanical edits, and their tests, even across multiple files. File count, step count, or the number of subsystems touched never justify planning mode on their own. Once an approach has been agreed in conversation, implementing it is execution, however many subsystems it spans — do not then propose planning for the thing that was just settled. Treat a direct instruction to build, fix, do, or try something as an instruction to execute: investigate and implement it, asking a focused question if one specific detail blocks you. A request for \"a plan\", \"the shape\", or \"the approach\" asks for prose in your reply, not planning mode; only an explicit request for a spec or plan file counts. While iterating on a bug or a failed fix, keep debugging and fixing rather than proposing a reviewed design. Suggest planning only when the design itself is still open and a written, reviewed design would prevent significant rework, such as consequential architecture tradeoffs or migrations with compatibility and rollout decisions. The user is shown a y/n/d/c prompt to approve; on approval the session enters a read-only exploration mode where you investigate and write an effort spec and plan (spec.md / plan.md) before implementation. Pass a short reason naming the still-open design question that planning would settle; do not cite file count, subsystem count, or work already agreed in conversation."
}

func (suggestPlanCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"reason": {"type": "string", "description": "Short, human-facing reason a reviewed plan would materially reduce risk (shown in the approval prompt), e.g. \"database migration requires compatibility and rollout decisions\"."},
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
	if err := call.Svc.EnterProfile(call.ConversationID, "plan"); err != nil {
		return nil, fmt.Errorf("suggest_plan: entering planning mode: %w", err)
	}

	msg := "Entered planning mode (read-only exploration). Pull the `planning-mode` protocol (get_protocol) and follow it: investigate the codebase, then author the effort's spec.md (what & why) and plan.md (phased to-do) before making any changes. Write/exec tools other than file writes are unavailable until the plan is approved."
	if r := strings.TrimSpace(a.Reason); r != "" {
		msg = "Entered planning mode: " + r + ".\n\n" + msg
	}
	return &capabilities.Result{Type: capabilities.ResultText, Text: msg}, nil
}
