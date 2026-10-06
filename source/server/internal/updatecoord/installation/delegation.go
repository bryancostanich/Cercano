package installation

import "strings"

// SelfUpdateRole reports who performs update actions for an installation
// beyond the owner of record. The owner of record (who owns the files and
// therefore uninstallation) never changes because of a delegation: a
// delegated installation keeps its package-manager owner while the shared Go
// coordinator may perform update content transitions through a verified
// cooperating-installer contract.
type SelfUpdateRole string

const (
	// SelfUpdateRoleNone means no delegation is corroborated: only the
	// owner of record may act.
	SelfUpdateRoleNone SelfUpdateRole = "none"
	// SelfUpdateRoleDelegate means an explicit, identity-bound, consented
	// delegation grants the self-update role to this updater's coordinator
	// while the package manager (Chocolatey, per the approved ownership
	// contract) retains ownership and uninstallation.
	SelfUpdateRoleDelegate SelfUpdateRole = "delegate"
)

// DelegatedUpdateSource is the only update source a delegated self-update may
// consume. Delegated updates are self-managed update content (the approved
// trust model is TUF); a delegated installation never pulls update content
// through ad-hoc or manager-side sources outside the verified feed.
const DelegatedUpdateSource = "tuf"

// DelegationEvidence is a fully validated delegation of the self-update role
// for one installation. A value is honored by ClassifyWithDelegation only
// after the policy layer corroborated the persisted record against the
// distinct current observed identity, administrator restrictions, and
// supported cooperating-installer evidence — and ClassifyWithDelegation
// re-verifies the binding against the supplied executable and manager
// evidence independently, so no caller input (and certainly no raw JSON
// field) can conjure update authority on its own.
//
// This is a pure value: it carries no authority, grants no execution
// capability, and describes what a trusted platform probe observed. Booleans
// here are probe/corroboration results, never raw persisted flags copied
// through unchecked.
type DelegationEvidence struct {
	// Owner is the package manager that RETAINS ownership of the
	// installation and therefore uninstallation. Only OwnerChocolatey has
	// an approved delegation contract.
	Owner Owner
	// Platform is the observed platform of the delegated installation.
	// The approved contract is Windows-only.
	Platform string
	// Arch is the observed architecture bound to the record.
	Arch string
	// Scope is the installation scope. Only ScopeUser has an approved
	// delegation contract; machine-wide installations stay
	// manager/administrator-controlled.
	Scope Scope
	// Root is the canonical resolved installation root the delegation is
	// bound to. It must match the currently observed root exactly.
	Root string
	// Executable is the canonical resolved executable path the delegation
	// is bound to. It must match the currently observed path exactly.
	Executable string
	// ReleaseChannel is the release channel the delegation applies to
	// (for example "stable"). A delegation never changes channels.
	ReleaseChannel string
	// FeedID is the stable opaque identifier of the specific delegated
	// update feed. Source names the feed KIND — "tuf" is a trust model,
	// not a feed — so the concrete feed identity must be bound explicitly
	// and nonempty for the evidence to be structurally complete.
	FeedID string
	// Source is the update source delegated self-updates consume. It must
	// be DelegatedUpdateSource.
	Source string
	// ContractVersion is the cooperating-installer contract version both
	// the installed package and this updater build implement.
	ContractVersion string
	// ContractVerified records that a platform probe verified the
	// installed package actually implements ContractVersion at Root.
	// This is a probe result, never a persisted self-declaration.
	ContractVerified bool
	// ConsentRecorded records that the user explicitly consented to this
	// exact delegation, corroborated by the policy layer against the
	// current identity. A persisted boolean is not authority by itself.
	ConsentRecorded bool
	// RegistrationRetained records that the package-manager registration
	// is retained per the approved contract: the manager remains the
	// uninstall owner and its database is never edited by the app.
	RegistrationRetained bool
}

// IsZero reports whether the evidence is unset.
func (d DelegationEvidence) IsZero() bool {
	return d == DelegationEvidence{}
}

// structurallyValid reports whether the evidence carries every field a
// delegation needs before binding is checked. It grants nothing by itself.
func (d DelegationEvidence) structurallyValid() bool {
	return d.Owner == OwnerChocolatey &&
		d.Platform == "windows" &&
		d.Arch != "" &&
		d.Scope == ScopeUser &&
		d.ReleaseChannel != "" &&
		d.FeedID != "" &&
		d.Source == DelegatedUpdateSource &&
		d.ContractVersion != "" &&
		d.ContractVerified &&
		d.ConsentRecorded &&
		d.RegistrationRetained
}

// ClassifyWithDelegation classifies installation ownership exactly like
// Classify, then optionally corroborates a fully validated delegation of the
// self-update role.
//
// The delegation is honored only when ALL of the following hold; otherwise
// the delegation is ignored completely and the base classification is
// returned unchanged:
//
//   - The delegation evidence is structurally complete: Chocolatey owner,
//     Windows platform, user scope, a nonempty arch/channel/contract
//     version, the delegated TUF update source, a verified cooperating
//     contract, recorded consent, and retained registration.
//   - The base classification independently proves CURRENT unambiguous
//     Chocolatey ownership of the running executable through owned-file
//     evidence, in user scope. A conflicting, ambiguous, unknown, or
//     development result is never overridden by a delegation record, and
//     machine-wide installations never receive one.
//   - The delegation is bound to the exact currently observed canonical
//     root and executable path, the executable resides under the delegated
//     root (boundary-respecting), and the observed arch matches.
//
// When honored, the result keeps OwnerChocolatey as the owner of record and
// uninstall owner, keeps AutoEditable false (the updater still never edits
// package-manager files itself; content transitions go through the verified
// cooperating-installer contract), and records
// SelfUpdateRoleDelegate with the corroborating evidence. A future Chocolatey
// action must honor the record, inspect the actual installed version, avoid
// downgrades, and never independently overwrite files beneath the shared
// coordinator.
//
// The delegation parameter is a value validated by the policy layer
// (internal/updatecoord/policy, which imports this package; this package
// must not import that one). Passing a zero DelegationEvidence yields
// exactly Classify's result.
func ClassifyWithDelegation(exe ExecutableEvidence, managers []ManagerEvidence, self SelfManagedEvidence, delegation DelegationEvidence) Classification {
	base := Classify(exe, managers, self)
	if delegation.IsZero() {
		return base
	}
	if !delegationHonored(exe, base, delegation) {
		return base
	}
	base.SelfUpdateRole = SelfUpdateRoleDelegate
	base.Delegation = delegation
	return base
}

// delegationHonored reports whether the delegation may take effect. It
// re-verifies everything against the supplied evidence: a delegation never
// overrides classifier ambiguity and never loosens a single fail-closed rule
// of Classify.
func delegationHonored(exe ExecutableEvidence, base Classification, d DelegationEvidence) bool {
	// Complete, contract-shaped evidence first.
	if !d.structurallyValid() {
		return false
	}
	// The base classification must independently prove current, unambiguous
	// Chocolatey ownership of this exact executable. Conflicting claims,
	// unknown provenance, development builds, and any non-Chocolatey owner
	// are never overridden.
	if base.Status != StatusActionable || base.Identity.Owner != OwnerChocolatey {
		return false
	}
	if base.Identity.Platform != "windows" {
		return false
	}
	// Machine-wide installations stay manager/administrator-controlled; the
	// approved opt-in is per-user only.
	if base.Identity.Scope != ScopeUser {
		return false
	}
	// Identity binding: the delegation must name the exact canonical root
	// and executable observed now, on the same platform and arch. A stale
	// or unbound record grants nothing.
	if exe.Platform != "windows" || d.Platform != exe.Platform || d.Arch != exe.Arch {
		return false
	}
	observedRoot := canonicalPath(exe.ResolvedRoot, exe.Platform)
	observedExe := canonicalPath(exe.ResolvedPath, exe.Platform)
	boundRoot := canonicalPath(d.Root, exe.Platform)
	boundExe := canonicalPath(d.Executable, exe.Platform)
	if observedRoot == "" || observedExe == "" || boundRoot != observedRoot || boundExe != observedExe {
		return false
	}
	// The canonical executable must actually RESIDE under the canonical
	// delegated root, with the prefix boundary respected: a root/exe pair
	// that merely agrees with itself is not a binding (it could name an
	// executable anywhere on the machine), and a prefix-naive check would
	// let sibling directories such as "CercanoOther" through. canonicalPath
	// normalizes separators to "/" and trims trailing separators, so a
	// single dir+"/" boundary comparison is exact here.
	if !strings.HasPrefix(observedExe, observedRoot+"/") {
		return false
	}
	return true
}
