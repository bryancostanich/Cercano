package installation

import (
	"path"
	"strings"
)

// Status is what the caller may do with an installation after classification.
type Status string

const (
	// StatusActionable means ownership was determined from actual
	// owned-file evidence (or explicit enrollment) and an update action may
	// be offered through that owner. It does not mean files may be edited:
	// package-manager owners perform their own updates.
	StatusActionable Status = "actionable"
	// StatusNonActionable means ownership evidence conflicts, is
	// insufficient, or the installation is not eligible for updates, and no
	// update action may be offered. This is the fail-closed result.
	StatusNonActionable Status = "non-actionable"
	// StatusUnknown means ownership could not be determined at all; it is
	// also non-actionable.
	StatusUnknown Status = "unknown"
)

// Reason explains a classification result.
type Reason string

const (
	// ReasonIdentified means ownership was determined from verifiable
	// owned-file evidence or explicit enrollment.
	ReasonIdentified Reason = "identified"
	// ReasonInsufficientEvidence means no owner could be proven from the
	// supplied evidence.
	ReasonInsufficientEvidence Reason = "insufficient-evidence"
	// ReasonManagerDoesNotOwnExecutable means at least one package manager
	// is present and reports a Cercano package installed, but none of them
	// verifiably owns the running executable's canonical path.
	ReasonManagerDoesNotOwnExecutable Reason = "manager-does-not-own-executable"
	// ReasonAmbiguousConflict means more than one owner can prove
	// ownership, or explicit enrollment conflicts with a package-manager
	// claim. There is no preference fallback; the result is
	// non-actionable.
	ReasonAmbiguousConflict Reason = "ambiguous-conflict"
	// ReasonDevelopmentCheckout means the running executable is a
	// development build/checkout. It is never auto-editable.
	ReasonDevelopmentCheckout Reason = "development-checkout"
	// ReasonMachineScopeDeferred means the installation is machine-wide.
	// Machine-wide self-updates are deferred by approved decision;
	// machine-wide installations remain package-manager/admin-controlled.
	ReasonMachineScopeDeferred Reason = "machine-scope-deferred"
	// ReasonIncompleteEnrollment means explicit enrollment exists but is
	// incomplete (unknown scope).
	ReasonIncompleteEnrollment Reason = "incomplete-enrollment"
)

// ExecutableEvidence describes the actually-running executable, as observed by
// the caller's platform probe. All paths must be canonical, resolved absolute
// paths: symlinks are represented by their resolved values. This package
// performs no path resolution, no filesystem access, and no symlink following.
type ExecutableEvidence struct {
	// Platform is the operating system the probe observed (for example
	// "darwin", "linux", "windows"). It is recorded, not detected, here.
	Platform string
	// Arch is the architecture the probe observed (for example "arm64").
	Arch string
	// ResolvedPath is the canonical resolved path of the running
	// executable (the symlink target, when the launcher is a symlink).
	ResolvedPath string
	// ResolvedRoot is the canonical resolved installation root, when known.
	ResolvedRoot string
	// LinkPath is the unresolved launcher path when the executable was
	// reached through a symlink; empty otherwise. A package manager may own
	// the launcher but not its retargeted destination; only the resolved
	// target is compared against owned-file records.
	LinkPath string
	// IsDevelopmentBuild is set only by an explicit development probe (for
	// example, a build-info marker), never inferred from a path.
	IsDevelopmentBuild bool
	// LayoutHints are caller observations about path shape (for example
	// "under /opt/homebrew"). They are recorded but deliberately ignored:
	// no path heuristic counts as proof of ownership.
	LayoutHints []string
}

// ManagerEvidence records what a package manager verifiably knows about
// Cercano on this machine. It is caller-supplied observation, not
// authentication.
type ManagerEvidence struct {
	// Manager is the package-manager owner this evidence is about
	// (homebrew, apt, or chocolatey).
	Manager Owner
	// Present records that the manager executable exists. This alone is
	// never evidence of ownership.
	Present bool
	// PackageInstalled records that the manager reports a Cercano package
	// installed. This alone is never evidence that it owns this
	// executable.
	PackageInstalled bool
	// PackageName is the package name the manager uses.
	PackageName string
	// OwnedFiles are the canonical resolved file paths the manager's
	// database verifiably records for the package (for example from
	// dpkg -S or a brew file listing). Only an exact match (after target-platform syntax validation) with the running executable's resolved path
	// proves ownership. Directory prefixes, similar-looking roots, and
	// other path heuristics are not accepted.
	OwnedFiles []string
	// Scope is the installation scope the manager reports.
	Scope Scope
}

// SelfManagedEvidence records explicit self-managed enrollment. Enrollment
// must be the result of a recorded user choice; it can never be inferred from
// a layout, path, or archive extraction.
type SelfManagedEvidence struct {
	// Root and Executable bind enrollment to this observed installation. They
	// must come from a protected platform enrollment probe, not UI input.
	Root       string
	Executable string
	// Enrolled records explicit enrollment.
	Enrolled bool
	// Scope is the enrolled installation scope. Only ScopeUser is eligible
	// for self-managed updates in the first milestone.
	Scope Scope
}

// Classification is the result of classifying installation ownership.
type Classification struct {
	// Identity is the determined installation identity.
	Identity Identity
	// Status is the actionable status.
	Status Status
	// Reason explains the result, especially for non-actionable cases.
	Reason Reason
	// AutoEditable reports whether the updater may modify this
	// installation's files itself. It is true only for explicitly enrolled
	// self-managed installations in user scope. Package-manager owners,
	// development builds, and unknown or ambiguous installations are never
	// auto-editable.
	AutoEditable bool
	// Conflicting lists the owners that each held proof in an ambiguous
	// conflict, in evidence order. Empty otherwise.
	Conflicting []Owner
}

// Classify determines installation ownership from caller-supplied evidence.
//
// The rules, in order:
//
//   - A development build is always classified as OwnerDevelopment and is
//     never auto-editable, regardless of other evidence. Development
//     checkouts are never silently converted or modified.
//   - A package manager proves ownership only when its verifiable owned-file
//     records contain the running executable's canonical resolved path. A
//     present manager, an installed package, and layout
//     hints are never sufficient on their own.
//   - Explicit self-managed enrollment is the only path to
//     OwnerSelfManaged.
//   - More than one claim (including enrollment conflicting with a
//     package-manager claim) yields an ambiguous, non-actionable result with
//     no preference fallback.
//   - Otherwise ownership is unknown and the result is non-actionable.
func Classify(exe ExecutableEvidence, managers []ManagerEvidence, self SelfManagedEvidence) Classification {
	identity := Identity{
		Owner:      OwnerUnknown,
		Scope:      ScopeUnknown,
		Platform:   exe.Platform,
		Arch:       exe.Arch,
		Executable: exe.ResolvedPath,
		Root:       exe.ResolvedRoot,
	}

	if exe.IsDevelopmentBuild {
		identity.Owner = OwnerDevelopment
		return Classification{
			Identity: identity,
			Status:   StatusNonActionable,
			Reason:   ReasonDevelopmentCheckout,
		}
	}

	if canonicalPath(exe.ResolvedPath, exe.Platform) == "" {
		return Classification{Identity: identity, Status: StatusUnknown, Reason: ReasonInsufficientEvidence}
	}

	// Collect ownership claims. Layout hints are ignored everywhere below:
	// they never create a claim.
	var claims []Owner
	var scopes []Scope
	for _, m := range managers {
		if !IsManagerOwner(m.Manager) {
			continue
		}
		if m.PackageInstalled && ownsExecutable(exe, m) {
			claims = append(claims, m.Manager)
			scopes = append(scopes, m.Scope)
		}
	}
	if self.Enrolled {
		root := canonicalPath(exe.ResolvedRoot, exe.Platform)
		enrolledRoot := canonicalPath(self.Root, exe.Platform)
		bound := root != "" && root == enrolledRoot && canonicalPath(self.Executable, exe.Platform) == canonicalPath(exe.ResolvedPath, exe.Platform) && strings.HasPrefix(canonicalPath(exe.ResolvedPath, exe.Platform), strings.TrimSuffix(root, "/")+"/")
		if !bound && len(claims) == 0 {
			return Classification{Identity: identity, Status: StatusNonActionable, Reason: ReasonIncompleteEnrollment}
		}
		// Even an incomplete competing enrollment is not permission to choose
		// a package-manager owner silently. Its provenance needs reconciliation.
		claims = append(claims, OwnerSelfManaged)
		scopes = append(scopes, self.Scope)
	}

	switch len(claims) {
	case 0:
		// No proof. If a manager looked plausible but did not verifiably
		// own the running executable, say so; otherwise the evidence was
		// simply insufficient.
		reason := ReasonInsufficientEvidence
		for _, m := range managers {
			if IsManagerOwner(m.Manager) && m.Present && m.PackageInstalled {
				reason = ReasonManagerDoesNotOwnExecutable
				break
			}
		}
		return Classification{
			Identity: identity,
			Status:   StatusUnknown,
			Reason:   reason,
		}
	case 1:
		return singleClaim(identity, claims[0], scopes[0])
	default:
		return Classification{
			Identity:    identity,
			Status:      StatusNonActionable,
			Reason:      ReasonAmbiguousConflict,
			Conflicting: claims,
		}
	}
}

// singleClaim builds the result for an unambiguous single owner claim.
func singleClaim(identity Identity, owner Owner, scope Scope) Classification {
	identity.Owner = owner
	identity.Scope = scope

	switch owner {
	case OwnerHomebrew, OwnerAPT, OwnerChocolatey:
		// The manager owns its own updates; the updater never edits
		// package-manager files, so these are actionable but never
		// auto-editable.
		identity.Source = string(owner)
		return Classification{
			Identity: identity,
			Status:   StatusActionable,
			Reason:   ReasonIdentified,
		}
	case OwnerSelfManaged:
		if scope != ScopeUser {
			// Machine-wide self-update is deferred by approved decision;
			// unknown scope means enrollment is incomplete. Neither is
			// auto-editable.
			reason := ReasonMachineScopeDeferred
			if scope == ScopeUnknown {
				reason = ReasonIncompleteEnrollment
			}
			return Classification{
				Identity: identity,
				Status:   StatusNonActionable,
				Reason:   reason,
			}
		}
		identity.Source = "tuf"
		return Classification{
			Identity:     identity,
			Status:       StatusActionable,
			Reason:       ReasonIdentified,
			AutoEditable: true,
		}
	}
	// Unreachable for claims produced by Classify; kept as a fail-closed
	// guard.
	return Classification{
		Identity: identity,
		Status:   StatusUnknown,
		Reason:   ReasonInsufficientEvidence,
	}
}

// ownsExecutable reports whether m's verifiable owned-file records contain the
// running executable. Only exact canonical-path matches (the resolved
// executable path) count; roots, prefixes, and name
// similarity are not proof.
func ownsExecutable(exe ExecutableEvidence, m ManagerEvidence) bool {
	target := canonicalPath(exe.ResolvedPath, exe.Platform)
	if target == "" {
		return false
	}
	for _, owned := range m.OwnedFiles {
		if p := canonicalPath(owned, exe.Platform); p != "" && p == target {
			return true
		}
	}
	return false
}

// Canonical input is a probe contract, not symlink or case resolution here.
// Validate the target OS syntax independently of the host running unit tests.
func canonicalPath(p, platform string) string {
	if p == "" || strings.ContainsRune(p, 0) {
		return ""
	}
	if platform == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
		if strings.HasPrefix(p, "//?/") || strings.HasPrefix(p, "//./") {
			return ""
		}
		if strings.HasPrefix(p, "//") {
			tail := strings.TrimPrefix(p, "//")
			parts := strings.Split(tail, "/")
			if len(parts) < 3 || parts[0] == "" || parts[1] == "" || path.Clean(tail) != tail || strings.Contains(tail, ":") {
				return ""
			}
			return "//" + tail
		}
		if len(p) < 4 || p[1] != ':' || p[2] != '/' || !((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) || strings.Contains(p[2:], ":") || path.Clean(p[2:]) != p[2:] {
			return ""
		}
		return p
	}
	if platform != "linux" && platform != "darwin" {
		return ""
	}
	if !strings.HasPrefix(p, "/") || p == "/" || path.Clean(p) != p {
		return ""
	}
	return p
}
