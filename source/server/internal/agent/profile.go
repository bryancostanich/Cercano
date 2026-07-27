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

// planWriteTools are the write-tier tools the plan profile permits so the agent
// can author the effort's spec.md and plan.md while otherwise fenced to
// read-only. This mirrors how Claude Code plan mode and obra/superpowers work:
// there is no bespoke "make a plan" tool — the plan is prose (Superpowers
// checkbox markdown, which our codec speaks) written with the ordinary file
// tools, and the read-only gate simply permits those two writes and nothing
// else. Structured status flips and task adds are execution-time store ops
// (PlanStore.SetStatus), not model-facing write tools during generation.
//
// Both the agent-surface display aliases ("Write"/"Edit") and the underlying
// capability names ("write_file"/"edit_file") are listed so the fence permits
// the tool regardless of whether it was registered with an alias — the fence
// sees whatever name reaches the gate.
var planWriteTools = []string{"Write", "Edit", "write_file", "edit_file"}

// PlanProfile is the read-only exploration fence for planning mode (Fork 1):
// read-tier tools plus the file-write tools needed to author spec.md/plan.md,
// nothing else. Exec tools (bash), git mutations, and destructive tools are
// neither advertised to the model nor executable while it is active.
func PlanProfile() Profile {
	extra := make(map[string]bool, len(planWriteTools))
	for _, n := range planWriteTools {
		extra[n] = true
	}
	return Profile{
		Name:         "plan",
		AllowedTiers: map[llm.Permission]bool{llm.PermR: true},
		ExtraCaps:    extra,
	}
}
