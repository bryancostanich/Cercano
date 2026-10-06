// Package policy defines the pure value model for update-UX policy:
// per-version dismissal of releases and explicit delegation of the
// self-update role for a package-manager-owned installation (the approved
// Chocolatey ownership contract).
//
// This package is PURE and grants no authority:
//
//   - It performs no filesystem access, reads no configuration, speaks no
//     RPC, invokes no package manager, constructs no package commands, and
//     reaches no network. Record serialization is validated in memory only;
//     nothing here persists anything.
//   - No raw JSON field or boolean grants execution authority by itself. A
//     persisted record (even a fully valid one) is data. The ONLY path to a
//     positive decision is EvaluateDelegation, which takes three DISTINCT
//     kinds of evidence beside the record: the currently observed
//     installation identity and classification (actual manager ownership,
//     verified through owned-file evidence by the installation package),
//     administrator restrictions for the machine, and supported
//     cooperating-installer evidence from a fresh probe. The record must
//     corroborate against ALL of them.
//   - Managed restrictions fail closed and are explicitly observed:
//     the zero/unobserved policy value never counts as "known
//     unmanaged" — only a probe that actually ran and read a stance (or
//     read no management) can establish that, and a contradictory
//     prohibited or unknown stance denies even on an unmanaged machine.
//   - The approved delegation contract is Chocolatey-only: Windows platform,
//     user scope, retained Chocolatey registration (Chocolatey remains the
//     uninstall owner), a verified cooperating-installer contract, recorded
//     consent, and delegated update content through the TUF feed. Machine-wide
//     installations, other managers, unbound or stale identity, revoked or
//     non-consented records, and unsupported contracts never yield a
//     positive decision, and never override an ambiguous or unknown
//     classification.
//   - Chocolatey Open Source package records can lag after a delegated
//     self-update. ReconcilePackageVersion is REPORT-ONLY policy: it says
//     whether package-record drift exists and whether an installer must
//     avoid replacing newer application content with the older recorded
//     version. It never constructs or runs package commands, never
//     authorizes registry (package-database) edits, never authorizes an
//     installation at all — least of all one without a verified candidate
//     version — and does not claim the app may set the Chocolatey record
//     to an arbitrary installed version: any reconciliation is performed
//     by the manager itself through its own supported operations after
//     the caller verifies actual state.
//
// The package holds no global mutable state; stores are per-instance values
// guarded by their own mutex, created by their constructors.
package policy

import (
	"strconv"
	"strings"

	"cercano/source/server/internal/updatecoord/installation"
	updatepkg "cercano/source/server/pkg/update"
)

// ManagedValue is an administrator's stance on the self-update opt-in.
type ManagedValue string

const (
	// ManagedUnset means no stance was observed. On a managed machine this
	// fails closed.
	ManagedUnset ManagedValue = ""
	// ManagedAllowed is an explicit administrator allowance.
	ManagedAllowed ManagedValue = "allowed"
	// ManagedProhibited is an explicit administrator prohibition.
	ManagedProhibited ManagedValue = "prohibited"
	// ManagedUnknown means the stance could not be determined (probe
	// failure, unreadable policy source). On a managed machine this fails
	// closed.
	ManagedUnknown ManagedValue = "unknown"
)

// ManagedRestrictions are the administrator-policy observations for the
// machine, supplied by a trusted probe. They are deliberately separate from
// any delegation record: an enrolled record is never self-authorizing, and
// the restrictions are evaluated against the CURRENT machine state.
type ManagedRestrictions struct {
	// Observed records that the administrator-policy probe actually ran
	// and read a policy stance (or read that the machine is unmanaged).
	// This is an explicit tri-state marker: the zero value is "the probe
	// never produced a stance" and NEVER counts as "known unmanaged" —
	// an unreadable or unobserved policy source must not silently
	// masquerade as an allowed unmanaged machine.
	Observed bool
	// Managed reports that this machine is under administrator update
	// policy (for example managed-device or group policy) for the
	// self-update opt-in.
	Managed bool
	// SelfUpdate is the administrator's stance. On a managed machine only
	// ManagedAllowed passes; ManagedUnset, ManagedUnknown, and any other
	// value fail closed. On an unmanaged machine the stance is not needed
	// — but a contradictory ManagedProhibited or ManagedUnknown still
	// denies: an explicitly (or unreadably) contradictory stance is never
	// ignored, whatever Managed says.
	// SelfUpdate is the administrator's stance. On a managed machine only
	// ManagedAllowed passes; ManagedUnset, ManagedUnknown, and any other
	// value fail closed. On an unmanaged machine the stance is not needed
	// — but a contradictory ManagedProhibited or ManagedUnknown still
	// denies: an explicitly (or unreadably) contradictory stance is never
	// ignored, whatever Managed says.
	SelfUpdate ManagedValue
}

// allowsSelfUpdate reports whether the restrictions permit the self-update
// opt-in. Permissive outcomes require the probe to have actually run
// (Observed): the zero/unobserved value never counts as "known unmanaged",
// and a stray allowance from a probe that never produced a stance is not
// evidence. Prohibitive outcomes never need corroboration: an explicit
// prohibition or unknown stance denies whatever Observed and Managed say.
func (r ManagedRestrictions) allowsSelfUpdate() (allowed bool, unknown bool) {
	switch r.SelfUpdate {
	case ManagedProhibited:
		// An explicit prohibition denies even on an unmanaged machine and
		// even without the probe flag: never trust a contradictory
		// "Managed=false + prohibited" observation.
		return false, false
	case ManagedUnknown:
		// Same for an explicitly unreadable stance.
		return false, true
	case ManagedAllowed:
		if !r.Observed {
			// The probe never produced a stance: a stray allowance is
			// not evidence. Fail closed as unknown.
			return false, true
		}
		// An explicit allowance passes whatever Managed says; on an
		// unmanaged machine it is simply not needed.
		return true, false
	case ManagedUnset:
		if r.Managed {
			// Managed machine with no stance: fail closed.
			return false, true
		}
		if !r.Observed {
			// The probe never produced a stance: the zero value must not
			// count as "known unmanaged". Fail closed as unknown.
			return false, true
		}
		// The probe ran and read no management at all: this is the ONLY
		// value that means "known unmanaged".
		return true, false
	default:
		// Unrecognized stance: fail closed.
		return false, true
	}
}

// CurrentInstallation is the distinct, currently observed installation the
// record must match. Its values never come from the record itself.
type CurrentInstallation struct {
	// InstallID is the opaque installation identifier from the trusted
	// resolver, observed now. It must be nonempty for any delegation to
	// bind.
	InstallID string
	// Class is the freshly computed classification of the current observed
	// evidence (installation.Classify or ClassifyWithDelegation output).
	// Actual current manager ownership is only proven there, through
	// owned-file evidence.
	Class installation.Classification
	// ReleaseChannel is the release channel the installation is currently
	// observed to consume. The record's channel must match it exactly:
	// a delegation is bound to the current channel, never to itself.
	ReleaseChannel string
	// FeedID is the stable opaque identifier of the specific update feed
	// the installation currently consumes, observed by the trusted
	// probe. The source KIND ("tuf") is not a feed: the record must name
	// the same concrete feed, and a missing observed feed identity
	// refuses the delegation.
	FeedID string
}

// InstallerContractEvidence is what the current updater build supports and
// what a fresh platform probe actually observed about the installed
// cooperating installer. Probe results are never taken from a persisted
// record.
type InstallerContractEvidence struct {
	// SupportedVersions lists the cooperating-installer contract versions
	// this updater build implements. Empty means none are supported and
	// every delegation is refused.
	SupportedVersions []string
	// ProbeVerified records that the platform probe verified the installed
	// package actually implements ObservedVersion at the installation root.
	ProbeVerified bool
	// ObservedVersion is the contract version the probe observed for this
	// installation.
	ObservedVersion string
}

// DelegationRefusal is the machine-readable reason a delegation was refused.
// Reasons are stable identifiers; they never embed paths or secrets.
type DelegationRefusal string

const (
	// RefusalNone accompanies an allowed delegation.
	RefusalNone DelegationRefusal = ""
	// RefusalManagedProhibited: administrator policy explicitly prohibits
	// the self-update opt-in.
	RefusalManagedProhibited DelegationRefusal = "managed-prohibited"
	// RefusalManagedUnknown: the machine is managed and the administrator
	// stance is missing or unknown. Fails closed.
	RefusalManagedUnknown DelegationRefusal = "managed-unknown"
	// RefusalRecordInvalid: the record failed structural validation. It
	// must be (re)validated; records reaching evaluation should already
	// have passed DecodeDelegationRecord.
	RefusalRecordInvalid DelegationRefusal = "record-invalid"
	// RefusalRevoked: the delegation was explicitly revoked.
	RefusalRevoked DelegationRefusal = "revoked"
	// RefusalConsentNotRecorded: the record does not carry recorded
	// consent for this delegation.
	RefusalConsentNotRecorded DelegationRefusal = "consent-not-recorded"
	// RefusalRegistrationNotRetained: the record does not retain the
	// package-manager registration, so it is not the approved contract.
	RefusalRegistrationNotRetained DelegationRefusal = "registration-not-retained"
	// RefusalClassificationNotActionable: the current classification is
	// ambiguous, conflicting, unknown, or development. A record never
	// overrides classifier ambiguity.
	RefusalClassificationNotActionable DelegationRefusal = "classification-not-actionable"
	// RefusalClassificationNotChocolatey: the current owner of record is
	// not Chocolatey; the approved delegation contract is Chocolatey-only.
	RefusalClassificationNotChocolatey DelegationRefusal = "classification-not-chocolatey"
	// RefusalWrongPlatform: the current installation is not Windows.
	RefusalWrongPlatform DelegationRefusal = "wrong-platform"
	// RefusalMachineScope: the installation is machine-wide (or its scope
	// is not user scope); machine-wide self-updates are deferred by
	// approved decision.
	RefusalMachineScope DelegationRefusal = "machine-scope"
	// RefusalUnboundIdentity: the record does not match the currently
	// observed installation (identifier, owner, platform, arch, scope, or
	// canonical root). Stale identity invalidates delegation.
	RefusalUnboundIdentity DelegationRefusal = "unbound-identity"
	// RefusalContractUnverified: the fresh probe did not verify a
	// cooperating-installer contract at the installation root.
	RefusalContractUnverified DelegationRefusal = "contract-unverified"
	// RefusalUnsupportedContract: the record's contract version is not one
	// this updater build supports.
	RefusalUnsupportedContract DelegationRefusal = "unsupported-contract"
)

// DelegationDecision is the outcome of evaluating a delegation record.
// Allowed is true only when every check passed; Evidence is the fully
// validated value to hand to installation.ClassifyWithDelegation and is the
// zero value otherwise.
type DelegationDecision struct {
	// Allowed reports whether the delegation may take effect.
	Allowed bool
	// Refusal explains a refusal; RefusalNone when Allowed is true.
	Refusal DelegationRefusal
	// Evidence is the corroborated delegation evidence, valid only when
	// Allowed is true.
	Evidence installation.DelegationEvidence
}

// EvaluateDelegation evaluates a persisted delegation record against the
// distinct current observations. It is the only function that can produce a
// positive decision, and it never mutates any state. Every check fails
// closed: a record alone, a probe alone, or an allowance alone is never
// authority.
//
// The checks, in order:
//
//  1. Managed restrictions: an explicitly observed probe result only. The
//     zero/unobserved value never counts as "known unmanaged"; on a managed
//     machine only an explicit ManagedAllowed stance passes; a
//     contradictory prohibited or unknown stance denies even on an
//     unmanaged machine.
//  2. Record validity and revocation/consent/registration flags. An
//     unknown nonzero schema version is a structural error here too: a
//     record delivered as a direct struct (bypassing
//     DecodeDelegationRecord) must not carry a schema the current code
//     does not know.
//  3. Current classification: StatusActionable with OwnerChocolatey, Windows
//     platform, user scope. Ambiguity, conflicts, unknown provenance, and
//     development builds are never overridden.
//  4. Identity binding: the record must match the current opaque install ID,
//     owner, platform, arch, scope, canonical root, observed release
//     channel, and observed feed identity exactly. The source kind "tuf"
//     is not a feed: the concrete feed identity must corroborate.
//  5. Contract: a fresh verified probe whose observed contract version is
//     the record's version, and that version must be supported by this
//     updater build.
//
// On success the decision's Evidence binds the record to the current
// identity so a later ClassifyWithDelegation re-verifies it against
// independently observed executable/manager evidence.
func EvaluateDelegation(rec DelegationRecord, cur CurrentInstallation, restrictions ManagedRestrictions, installer InstallerContractEvidence) DelegationDecision {
	// 1. Administrator restrictions fail closed: unobserved policy is
	// unknown, and a contradictory prohibited/unknown stance denies even
	// on an unmanaged machine.
	if allowed, unknown := restrictions.allowsSelfUpdate(); !allowed {
		if unknown {
			return DelegationDecision{Refusal: RefusalManagedUnknown}
		}
		return DelegationDecision{Refusal: RefusalManagedProhibited}
	}

	// 2. The record must be structurally valid, unrevoked, consented, and
	// retain registration per the approved contract.
	if err := rec.validate(); err != nil {
		return DelegationDecision{Refusal: RefusalRecordInvalid}
	}
	if rec.Revoked {
		return DelegationDecision{Refusal: RefusalRevoked}
	}
	if !rec.ConsentRecorded {
		return DelegationDecision{Refusal: RefusalConsentNotRecorded}
	}
	if !rec.RegistrationRetained {
		return DelegationDecision{Refusal: RefusalRegistrationNotRetained}
	}

	// 3. Actual current ownership must be proven by the classification.
	// A delegation record never overrides ambiguity or unknown provenance.
	id := cur.Class.Identity
	if cur.Class.Status != installation.StatusActionable {
		return DelegationDecision{Refusal: RefusalClassificationNotActionable}
	}
	if id.Owner != installation.OwnerChocolatey {
		return DelegationDecision{Refusal: RefusalClassificationNotChocolatey}
	}
	if id.Platform != "windows" {
		return DelegationDecision{Refusal: RefusalWrongPlatform}
	}
	if id.Scope != installation.ScopeUser {
		return DelegationDecision{Refusal: RefusalMachineScope}
	}

	// 4. Identity binding against the distinct current observation,
	// including the observed release channel and the concrete feed
	// identity (the source kind "tuf" is not a feed).
	if cur.InstallID == "" || rec.InstallID != cur.InstallID ||
		rec.Owner != string(id.Owner) ||
		rec.Platform != id.Platform ||
		rec.Arch != id.Arch ||
		rec.Scope != string(id.Scope) ||
		rec.CanonicalRoot != id.Root ||
		cur.ReleaseChannel == "" || rec.ReleaseChannel != cur.ReleaseChannel ||
		cur.FeedID == "" || rec.FeedID != cur.FeedID {
		return DelegationDecision{Refusal: RefusalUnboundIdentity}
	}

	// 5. The cooperating-installer contract must be freshly verified and
	// supported. A persisted boolean never substitutes for the probe.
	supported := false
	for _, v := range installer.SupportedVersions {
		if v == rec.ContractVersion {
			supported = true
			break
		}
	}
	if !supported {
		return DelegationDecision{Refusal: RefusalUnsupportedContract}
	}
	if !installer.ProbeVerified || installer.ObservedVersion == "" || installer.ObservedVersion != rec.ContractVersion {
		return DelegationDecision{Refusal: RefusalContractUnverified}
	}

	// Fully corroborated: build the validated evidence for the classifier.
	evidence := installation.DelegationEvidence{
		Owner:                installation.OwnerChocolatey,
		Platform:             id.Platform,
		Arch:                 id.Arch,
		Scope:                id.Scope,
		Root:                 id.Root,
		Executable:           id.Executable,
		ReleaseChannel:       rec.ReleaseChannel,
		FeedID:               cur.FeedID,
		Source:               installation.DelegatedUpdateSource,
		ContractVersion:      rec.ContractVersion,
		ContractVerified:     true,
		ConsentRecorded:      true,
		RegistrationRetained: true,
	}
	return DelegationDecision{Allowed: true, Refusal: RefusalNone, Evidence: evidence}
}

// ReconciliationStatus is the machine-readable outcome of package-record
// reconciliation policy. It is REPORT-ONLY: it names the relation between
// the actually installed application version and the package record, and
// says nothing about performing an installation.
type ReconciliationStatus string

const (
	// ReconcileAligned: no drift — the package record already matches the
	// actually installed application version.
	ReconcileAligned ReconciliationStatus = "aligned"
	// ReconcileRecordLags: drift — the package record lags the actually
	// installed application version (the expected state after a delegated
	// self-update). A package-manager replacement driven by the recorded
	// (older) version would replace newer application content with an
	// older one: the installer must avoid the older replacement. The
	// record may be brought up to date only by the manager itself through
	// its own supported operations after the caller verifies actual state;
	// this function does not claim the app may set the Chocolatey record
	// version to an arbitrary installed version.
	ReconcileRecordLags ReconciliationStatus = "record-lags"
	// ReconcileRecordAheadMismatch: drift in the other direction — the
	// package record claims a version the actually installed application
	// does not have (for example after an external package upgrade or a
	// self-update rollback). Nothing here downgrades application content;
	// it is an ahead mismatch that is refused until explicitly
	// reconciled through the manager's own supported operations, and it
	// never authorizes proceeding on the record's word alone.
	ReconcileRecordAheadMismatch ReconciliationStatus = "record-ahead-mismatch"
	// ReconcileInvalid: the versions could not be compared under the
	// strict stable-version rules.
	ReconcileInvalid ReconciliationStatus = "invalid"
)

// ReconciliationDecision is the report-only policy outcome for a package
// record versus the actually installed application version.
type ReconciliationDecision struct {
	// Status is the machine-readable relation.
	Status ReconciliationStatus
	// Drift reports whether the package record version differs from the
	// actually installed application version.
	Drift bool
	// InstallerMustAvoidOlderReplacement reports whether a package-manager
	// replacement driven by the recorded version would install an OLDER
	// version over newer application content and must be avoided. True
	// only in the record-lags case.
	InstallerMustAvoidOlderReplacement bool
}

// ReconcilePackageVersion reports whether Chocolatey Open Source package
// record drift exists (relative to the actually installed application
// version) and whether an installer must avoid replacing newer application
// content with the older recorded version. This is REPORT-ONLY policy: it
// constructs and runs no package command, authorizes no registry
// (package-database) edit, authorizes no installation at all — least of all
// one without a verified candidate version — and does not claim the app may
// set the Chocolatey record version to an arbitrary installed version. Any
// reconciliation is performed by the manager itself through its own
// supported operations, after the caller verifies the actual installed
// version against the real package.
//
// Version inputs are validated strictly against the exact shape the release
// pipeline currently publishes — X.Y.Z with an optional "v" prefix — because
// the shared CompareVersions is deliberately lax (junk and overflowing
// components silently compare as zeros there). Prerelease and build-metadata
// forms are rejected until the release pipeline explicitly supports them; no
// semver dependency is introduced.
func ReconcilePackageVersion(installedVersion, packageRecordVersion string) ReconciliationDecision {
	if !validStableVersion(installedVersion) || !validStableVersion(packageRecordVersion) {
		return ReconciliationDecision{Status: ReconcileInvalid}
	}
	switch cmp := updatepkg.CompareVersions(installedVersion, packageRecordVersion); {
	case cmp > 0:
		// The actually installed application is newer than the record:
		// drift exists, and an installer must avoid replacing the newer
		// content with the older recorded version.
		return ReconciliationDecision{
			Status:                             ReconcileRecordLags,
			Drift:                              true,
			InstallerMustAvoidOlderReplacement: true,
		}
	case cmp == 0:
		return ReconciliationDecision{Status: ReconcileAligned}
	default:
		// The record claims a version the application does not have:
		// ahead mismatch, refused until explicitly reconciled. It is not
		// a downgrade of application content (and never authorizes one).
		return ReconciliationDecision{
			Status: ReconcileRecordAheadMismatch,
			Drift:  true,
		}
	}
}

// validStableVersion reports whether v is the exact shape the release
// pipeline currently publishes: three all-numeric components X.Y.Z with an
// optional "v" prefix, each component within the native signed integer used by CompareVersions.
// Prereleases, build metadata, truncated forms, junk, and overflowing
// components are rejected until the release pipeline explicitly supports
// them. This deliberately does not introduce a semver dependency.
func validStableVersion(v string) bool {
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		// Reject overflowing components instead of letting them silently
		// compare as zeros.
		if _, err := strconv.ParseInt(p, 10, strconv.IntSize); err != nil {
			return false
		}
	}
	return true
}

// validVersionToken rejects empty, whitespace-padded, or NUL-containing
// version strings; it does not validate semantics.
func validVersionToken(v string) bool {
	if v == "" || strings.ContainsRune(v, 0) {
		return false
	}
	return strings.TrimSpace(v) == v
}
