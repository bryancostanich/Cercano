package agent

import "cercano/source/server/internal/llm"

// Profile is a capability fence layered *on top of* the permission mode. It is a
// distinct axis from PermissionMode: the mode is the confirm-aggressiveness dial
// (strict / permissive / bypass — "do I ask the human?"), while a Profile
// answers a prior, harder question — "is this tool available at all right now?"
//
// The two compose. Planning mode (design Fork 1) is "read-only exploration": the
// agent may read the world but may not change it, *while still honoring* the
// active mode for the reads it is allowed (§4.2 ties divergence thresholds to
// mode during planning). Collapsing planning into a fourth mode would make
// "planning + strict" unrepresentable, so the fence is its own value.
//
// A Profile denies by capability tier, with an escape hatch for specific
// named capabilities (e.g. the `plan` capability itself, which is not PermR but
// must be reachable while planning). It is consulted at two seams that share
// this one predicate:
//
//   - Advertisement (ergonomics): tools the profile forbids are filtered out of
//     the catalog sent to the model, so the model never reaches for a write it
//     can't have.
//   - Enforcement (the fence): before the confirm gate, any tool the profile
//     forbids is denied outright — no y/n, no "yes" available. Filtering is not
//     a substitute for this; a tool that reaches the loop by any other path
//     (hallucinated name, dispatched work, future code) still hits the fence.
//
// The zero Profile denies nothing: AllowedTiers nil means "all tiers allowed",
// so callers that don't set a profile are unaffected.
type Profile struct {
	// Name is a short label for diagnostics and gate messages (e.g. "plan").
	Name string

	// AllowedTiers is the set of permission tiers a tool may carry to be
	// available. Nil means unrestricted (all tiers) — the default posture.
	// A read-only profile sets this to {PermR}.
	AllowedTiers map[llm.Permission]bool

	// ExtraCaps names specific capabilities allowed regardless of their tier —
	// the escape hatch for a not-read-tier capability that the profile's whole
	// point depends on (the `plan` capability under the plan profile).
	ExtraCaps map[string]bool
}

// Restricts reports whether this profile fences anything off at all. The zero
// value (and an explicit unrestricted profile) restricts nothing, letting both
// seams skip their work entirely on the common path.
func (p Profile) Restricts() bool {
	return len(p.AllowedTiers) > 0
}

// Allows reports whether a tool with the given tier and name may be used under
// this profile. An unrestricted profile allows everything. Otherwise a tool is
// allowed iff its tier is in AllowedTiers OR its name is in ExtraCaps.
func (p Profile) Allows(tier llm.Permission, name string) bool {
	if !p.Restricts() {
		return true
	}
	if p.AllowedTiers[tier] {
		return true
	}
	return p.ExtraCaps[name]
}

// planExtraTools are the non-read-tier tools the plan profile explicitly
// permits while otherwise fenced to read-only.
//
// Write/Edit let the agent author the effort's spec.md and plan.md using the
// ordinary file tools, matching the file-as-plan architecture. The plan remains
// prose/checkbox markdown; structured status flips and task adds are
// execution-time store ops (PlanStore.SetStatus), not model-facing write tools
// during generation.
//
// request_plan_approval is the handoff tool: it must remain callable from inside
// planning mode so the model can raise the y/n/d/c approval gate after the plan
// is written. It is X-tier specifically so that gate fires before Execute even
// in Permissive mode.
//
// plan_exit is the silent abandon path: it must remain callable from inside
// planning mode so the model can leave when a plan is not worth writing. It is
// W-tier and exits with no gate.
//
// Both the agent-surface display aliases ("Write"/"Edit") and the underlying
// capability names ("write_file"/"edit_file") are listed so the fence permits
// the file tools regardless of which name reaches the gate.
var planExtraTools = []string{"Write", "Edit", "write_file", "edit_file", "request_plan_approval", "plan_exit"}

// ToolLiftsPlanFence reports whether a tool, once executed successfully, exits
// planning mode and therefore must drop the read-only fence for the remainder
// of the current turn.
//
// Both handoff tools flip the ProfileBroker to the default (unrestricted)
// profile in their Execute: plan_exit abandons planning silently, and
// request_plan_approval hands off to execution once the plan is approved. That
// broker change only lands on the NEXT turn's profile read (runner reads the
// active profile once at turn start and freezes it into ToolLoopInput.Profile),
// so without lifting the loop-local fence mid-turn the rest of THIS turn stays
// fenced and any follow-on write/exec tool is wrongly blocked. The lift is
// asymmetric: it only ever relaxes the fence, never tightens it — entering a
// profile still happens cleanly at a turn boundary.
func ToolLiftsPlanFence(toolName string) bool {
	return toolName == "plan_exit" || toolName == "request_plan_approval"
}

// IsSessionControlTool reports whether a tool changes, requests, or finalizes
// the session's supervisory profile/state rather than performing ordinary work.
// These tools are explicit control boundaries: a redirect/denial-with-message or
// execution error must stop the current tool turn instead of being fed back to
// the model as ordinary steerable tool output.
func IsSessionControlTool(toolName string) bool {
	switch toolName {
	case "suggest_plan",
		"request_plan_approval",
		"plan_exit",
		"suggest_autonomous",
		"request_autonomous_execution",
		"request_autonomous_exit",
		"complete_autonomous_review",
		"auto_exit":
		return true
	default:
		return false
	}
}

// PlanProfile is the read-only exploration fence for planning mode (Fork 1):
// read-tier tools plus the file-write tools needed to author spec.md/plan.md and
// the approval handoff tool, nothing else. Exec tools (bash), git mutations, and
// destructive tools are neither advertised to the model nor executable while it
// is active.
func PlanProfile() Profile {
	extra := make(map[string]bool, len(planExtraTools))
	for _, n := range planExtraTools {
		extra[n] = true
	}
	return Profile{
		Name:         "plan",
		AllowedTiers: map[llm.Permission]bool{llm.PermR: true},
		ExtraCaps:    extra,
	}
}

// AutonomousProfile is the live-work posture for autonomous mode. It does not
// remove any permission tier by itself: the strict/permissive/bypass permission
// mode remains the confirm-aggressiveness dial, while autonomous mode supplies
// behavioral shaping (approved brief, decision logging, final review) through
// the profile signal. Listing every known tier makes the profile "restricting"
// for signaling/filtering purposes without actually fencing normal tools.
func AutonomousProfile() Profile {
	return Profile{
		Name: "autonomous",
		AllowedTiers: map[llm.Permission]bool{
			llm.PermR: true,
			llm.PermW: true,
			llm.PermX: true,
		},
	}
}
