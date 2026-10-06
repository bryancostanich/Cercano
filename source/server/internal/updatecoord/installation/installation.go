// Package installation defines the value model and evidence classification
// for Cercano installation ownership.
//
// This package is pure: it performs no filesystem access, executes no package
// managers, and reaches no network. All evidence is supplied by the caller.
// Caller-supplied evidence describes what a platform probe observed; it is not
// authentication of an update feed. The platform probes that would produce
// this evidence are still pending; until they exist, nothing here can claim
// that ownership detection works end to end.
//
// Ownership is proven only by actual canonical executable/package owned-file
// evidence: a package manager must verifiably list the running executable (its
// canonical resolved path) among the files it owns. A manager merely being
// present, a package merely being installed for some other binary, or any
// path-layout heuristic is never proof of ownership.
//
// A release announcement is separate from an installable version: the
// ReleaseAvailability type keeps the two distinct by verified source and
// version, and no ownership classification ever asserts that an announced
// release is installable through the installation's own source.
//
// Conflict, ambiguity, and unknown provenance fail closed to a non-actionable
// result with an explicit reason. There is no preference-order fallback among
// plausible owners. Development builds are never auto-editable. Self-managed
// ownership arises only from explicit enrollment, never from a layout guess.
package installation

// Owner identifies who owns the files of an installation and therefore who
// must perform any update to it.
type Owner string

const (
	// OwnerUnknown means ownership could not be determined from trustworthy
	// evidence. Installations with this owner are never edited.
	OwnerUnknown Owner = "unknown"
	// OwnerDevelopment marks a development checkout or development build.
	// Development installs are never converted or modified by the updater.
	OwnerDevelopment Owner = "development"
	// OwnerHomebrew marks an installation owned by the Homebrew package
	// manager on macOS.
	OwnerHomebrew Owner = "homebrew"
	// OwnerAPT marks an installation owned by APT on Linux.
	OwnerAPT Owner = "apt"
	// OwnerChocolatey marks an installation owned by Chocolatey on Windows.
	OwnerChocolatey Owner = "chocolatey"
	// OwnerSelfManaged marks an installation explicitly enrolled into
	// Cercano's self-managed updates. Enrollment is the only path to this
	// owner.
	OwnerSelfManaged Owner = "self-managed"
)

// ManagerOwners are the package managers that can own an installation.
func ManagerOwners() []Owner {
	return []Owner{OwnerHomebrew, OwnerAPT, OwnerChocolatey}
}

// IsManagerOwner reports whether o is a package-manager owner.
func IsManagerOwner(o Owner) bool {
	switch o {
	case OwnerHomebrew, OwnerAPT, OwnerChocolatey:
		return true
	}
	return false
}

// Scope is the account/machine visibility of an installation.
type Scope string

const (
	// ScopeUser is a per-user installation.
	ScopeUser Scope = "user"
	// ScopeMachine is a machine-wide installation.
	ScopeMachine Scope = "machine"
	// ScopeUnknown is used when the scope has not been determined.
	ScopeUnknown Scope = "unknown"
)

// Identity is the resolved installation identity. Platform, architecture, and
// source are caller-supplied probe results, not detections made here.
type Identity struct {
	// Owner is the determined installation owner.
	Owner Owner
	// Scope is the installation scope.
	Scope Scope
	// Platform is the operating system of the installation (for example
	// "darwin", "linux", "windows").
	Platform string
	// Arch is the architecture of the installation (for example "arm64").
	Arch string
	// Source is the release source this installation consumes updates
	// through, for example "homebrew", "apt", "chocolatey", or "tuf".
	// For OwnerUnknown this is empty: an unowned installation has no
	// verified update source.
	Source string
	// Executable is the canonical, resolved absolute path of the running
	// executable. Symlinks are represented by their resolved values; this
	// package never resolves paths itself.
	Executable string
	// Root is the canonical, resolved installation root path, if known.
	Root string
}

// ReleaseAvailability keeps a release announcement (for example, a GitHub
// release discovered upstream) separate from a version verified as
// installable through this installation's own source (for example, the
// Homebrew tap or the TUF feed). The two are never conflated: a newer
// announcement must not be presented as an installable package-manager
// update, and an unverified source must not be reported as installable.
type ReleaseAvailability struct {
	// AnnouncedVersion is the newest announced release version (may be
	// empty when nothing was announced).
	AnnouncedVersion string
	// AnnouncedSource is the source of the announcement, for example
	// "github".
	AnnouncedSource string
	// InstallableVersion is the newest version verified available through
	// the installation's own source.
	InstallableVersion string
	// InstallableSource is the verified source of the installable version.
	InstallableSource string
	// Verified records that InstallableVersion was actually verified
	// against InstallableSource. Until the platform probes exist, this must
	// stay false and Installable remains false.
	Verified bool
}

// Announced reports whether a release announcement exists.
func (r ReleaseAvailability) Announced() bool {
	return r.AnnouncedVersion != "" && r.AnnouncedSource != ""
}

// Installable reports whether the value carries verified availability evidence.
// It does not bind that evidence to any installation; use InstallableFor when
// deciding whether to present a version for a particular installation.
func (r ReleaseAvailability) Installable() bool {
	return r.Verified && r.InstallableVersion != "" && r.InstallableSource != ""
}

// InstallableFor requires matching installation-source evidence. This remains
// a value-model guard; trusted platform/TUF probes must establish Verified.
func (r ReleaseAvailability) InstallableFor(c Classification) bool {
	return c.Status == StatusActionable && c.Identity.Source != "" &&
		c.Identity.Platform != "" && c.Identity.Arch != "" &&
		r.Installable() && r.InstallableSource == c.Identity.Source
}
