// Package probe performs the native, read-only filesystem observation half
// of installation ownership classification.
//
// It observes the actually running executable (an injected path for tests
// and later runtime wiring, defaulting to os.Executable) and corroborates
// candidate package-manager owned-file receipts supplied by an injected
// provider. It runs no package managers (never brew/dpkg/choco on a
// developer host), opens no manager database, and parses no registry
// format: receipts are observational inputs whose authenticity is the
// contract of the real trusted manager backends that will be supplied
// later. No provider implementation exists in this package, and no
// Chocolatey registry format is invented before packaging exists; the
// Chocolatey cooperating-installer self-update contract remains refused in
// the installation/policy layers.
//
// Ownership is corroborated only when a receipt names a regular file (never
// a symlink leaf) whose parents resolve to the exact canonical path and
// file identity of the observed executable, inside the observed
// installation root. Manager presence, PATH appearance, layout shape, and
// prefix siblings never create ownership. A hardlink whose canonical path
// lies outside the observed root is not ownership proof. Missing or changed
// paths fail closed. An applicable provider that is unavailable or
// incomplete makes the whole corroboration non-actionable: it is never
// silently dropped, because it could hold conflicting ownership evidence.
//
// This package is not wired into any runtime, UI, RPC, configuration, or
// update flow. It performs no privileged actions and writes no state. Its
// observations grant no write authority: development markers stay
// non-auto-editable and package-manager owners keep their files.
package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"cercano/source/server/internal/updatecoord/installation"
)

// Options configure a single observation. Every input is injected by the
// caller; nothing is inferred from PATH, environment variables, or path
// layout.
type Options struct {
	// ExecutablePath is the executable to observe. Empty means the actual
	// running executable via os.Executable. Tests inject explicit paths;
	// later runtime wiring injects the real process path.
	ExecutablePath string
	// RootPath is the claimed installation root supplied by a trusted
	// resolver. It is never inferred from the executable's layout here.
	// When empty, no root is observed and receipt corroboration refuses
	// every receipt because root containment cannot be enforced.
	RootPath string
	// DevelopmentMarker reports positive development-build evidence for the
	// resolved executable path. Nil means no development evidence was
	// observed. A marker is positive evidence only; it is never inferred
	// from a path, and a development classification is never auto-editable.
	DevelopmentMarker func(resolvedPath string) bool
}

// Facts are the canonical facts observed about the running executable and
// its claimed installation root. Paths are absolute and fully resolved; the
// original launcher path used to reach the executable is kept informational
// only and never carries authority. File identities are captured at
// observation time so later stability checks can fail closed.
type Facts struct {
	platform      string
	arch          string
	originalPath  string
	linkPath      string
	resolvedPath  string
	resolvedRoot  string
	exeIdentity   os.FileInfo
	rootIdentity  os.FileInfo
	isDevelopment bool
}

// Platform returns the observed host operating system.
func (f Facts) Platform() string { return f.platform }

// Arch returns the observed host architecture.
func (f Facts) Arch() string { return f.arch }

// OriginalPath returns the executable path as supplied or discovered,
// before resolution. It is informational.
func (f Facts) OriginalPath() string { return f.originalPath }

// LinkPath returns the original launcher path when the executable was
// reached through a symlink; empty otherwise. A manager may own a launcher
// without owning its retargeted destination, so this is never ownership
// evidence.
func (f Facts) LinkPath() string { return f.linkPath }

// ResolvedPath returns the canonical, fully resolved absolute path of the
// observed executable.
func (f Facts) ResolvedPath() string { return f.resolvedPath }

// ResolvedRoot returns the canonical installation root, or empty when no
// root was supplied.
func (f Facts) ResolvedRoot() string { return f.resolvedRoot }

// IsDevelopmentBuild reports positive development-marker evidence observed
// for the resolved executable, if a marker probe was supplied.
func (f Facts) IsDevelopmentBuild() bool { return f.isDevelopment }

// ExecutableEvidence converts the observed facts into the pure value model
// consumed by installation.Classify.
func (f Facts) ExecutableEvidence() installation.ExecutableEvidence {
	return installation.ExecutableEvidence{
		Platform:           f.platform,
		Arch:               f.arch,
		ResolvedPath:       f.resolvedPath,
		ResolvedRoot:       f.resolvedRoot,
		LinkPath:           f.linkPath,
		IsDevelopmentBuild: f.isDevelopment,
	}
}

// Observe performs one read-only observation of the executable (injected
// path or os.Executable) and the claimed installation root.
//
// The executable path is made absolute and fully resolved with
// Abs/EvalSymlinks/Stat; its leaf must be an existing regular file, never a
// symlink or directory. When the original supplied path is itself a
// symlink, that original link path is recorded informationally and the
// resolved regular file is the only authority.
//
// When a root is supplied, it must exist as a directory after resolution
// and must contain the resolved executable with the directory boundary
// respected (a prefix sibling such as "CercanoOther" is not containment).
// Any failure is an error: observation fails closed and produces no facts.
func Observe(opts Options) (Facts, error) {
	supplied := opts.ExecutablePath
	if supplied == "" {
		exe, err := os.Executable()
		if err != nil {
			return Facts{}, fmt.Errorf("probe: os.Executable: %w", err)
		}
		supplied = exe
	}
	abs, err := filepath.Abs(supplied)
	if err != nil {
		return Facts{}, fmt.Errorf("probe: executable path %q: %w", supplied, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Facts{}, fmt.Errorf("probe: resolve executable %q: %w", supplied, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Facts{}, fmt.Errorf("probe: stat executable %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return Facts{}, fmt.Errorf("probe: executable %q is not a regular file", resolved)
	}
	f := Facts{
		platform:     runtime.GOOS,
		arch:         runtime.GOARCH,
		originalPath: supplied,
		resolvedPath: resolved,
		exeIdentity:  info,
	}
	if leaf, err := os.Lstat(abs); err == nil && leaf.Mode()&os.ModeSymlink != 0 {
		f.linkPath = abs
	}
	if opts.DevelopmentMarker != nil {
		f.isDevelopment = opts.DevelopmentMarker(resolved)
	}
	if opts.RootPath != "" {
		rootAbs, err := filepath.Abs(opts.RootPath)
		if err != nil {
			return Facts{}, fmt.Errorf("probe: root path %q: %w", opts.RootPath, err)
		}
		rootResolved, err := filepath.EvalSymlinks(rootAbs)
		if err != nil {
			return Facts{}, fmt.Errorf("probe: resolve root %q: %w", opts.RootPath, err)
		}
		rootInfo, err := os.Stat(rootResolved)
		if err != nil {
			return Facts{}, fmt.Errorf("probe: stat root %q: %w", rootResolved, err)
		}
		if !rootInfo.IsDir() {
			return Facts{}, fmt.Errorf("probe: root %q is not a directory", rootResolved)
		}
		if !contained(resolved, rootResolved) {
			return Facts{}, fmt.Errorf("probe: executable %q is not contained in root %q", resolved, rootResolved)
		}
		f.resolvedRoot = rootResolved
		f.rootIdentity = rootInfo
	}
	return f, nil
}

// StillObserved re-checks that the observed executable and root are still
// present with the same file identity. A missing file, a non-regular
// replacement, a changed file identity (including a rewritten executable), a
// missing or replaced root, or a root that no longer resolves to the
// observed directory fails closed with an error.
func (f Facts) StillObserved() error {
	info, err := os.Stat(f.resolvedPath)
	if err != nil {
		return fmt.Errorf("probe: executable %q no longer observable: %w", f.resolvedPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("probe: executable %q is no longer a regular file", f.resolvedPath)
	}
	if !os.SameFile(info, f.exeIdentity) {
		return fmt.Errorf("probe: executable %q changed identity since observation", f.resolvedPath)
	}
	if f.resolvedRoot != "" {
		rootInfo, err := os.Stat(f.resolvedRoot)
		if err != nil {
			return fmt.Errorf("probe: root %q no longer observable: %w", f.resolvedRoot, err)
		}
		if !rootInfo.IsDir() {
			return fmt.Errorf("probe: root %q is no longer a directory", f.resolvedRoot)
		}
		if !os.SameFile(rootInfo, f.rootIdentity) {
			return fmt.Errorf("probe: root %q changed identity since observation", f.resolvedRoot)
		}
		resolved, err := filepath.EvalSymlinks(f.resolvedRoot)
		if err != nil || resolved != f.resolvedRoot {
			return fmt.Errorf("probe: root %q no longer resolves to the observed directory (got %q, err %v)", f.resolvedRoot, resolved, err)
		}
	}
	return nil
}

// contained reports whether path lies strictly inside root, treating the
// directory boundary exactly: a prefix sibling that merely shares a string
// prefix (for example "CercanoOther" under a root "Cercano") is not
// containment.
func contained(path, root string) bool {
	if path == root {
		return false
	}
	sep := string(filepath.Separator)
	if root == sep {
		return strings.HasPrefix(path, sep)
	}
	return strings.HasPrefix(path, root+sep)
}

// Receipt is one candidate owned-file record supplied by an injected
// provider. It is observational input: this package does not parse any
// manager database, registry, or package list, and cannot attest that the
// record is authentic. Real trusted manager backends supply receipts later.
type Receipt struct {
	// Manager is the package manager the record speaks for.
	Manager installation.Owner
	// PackageName is the manager's package name.
	PackageName string
	// SourceID identifies the receipt's provenance (which backend and
	// record it came from). It is preserved through corroboration so a
	// later authoritative backend can be selected without re-probing.
	SourceID string
	// Scope is the installation scope the manager reports.
	Scope installation.Scope
	// OwnedPath is the recorded owned path. Its leaf must be an existing
	// regular file (never a symlink) that resolves to the observed
	// executable inside the observed root.
	OwnedPath string
}

// ReceiptProvider supplies candidate receipts for the observed machine.
// Implementations are trusted manager backends added later; none exists in
// this package. Implementations collect receipts through their own trusted
// channels; this package never runs package-manager commands and never
// inspects PATH or layout.
type ReceiptProvider interface {
	// Name identifies the provider in diagnostics.
	Name() string
	// AppliesTo reports whether the provider can hold ownership evidence
	// on the given platform. Providers that do not apply are skipped
	// entirely; their unavailability cannot hide conflicting ownership on
	// a platform they never speak for.
	AppliesTo(platform string) bool
	// Receipts returns candidate receipts, or a non-nil error when the
	// provider could not complete its observation. A nil error with no
	// receipts is an authoritative "no receipts" observation, not a
	// failure.
	Receipts() ([]Receipt, error)
}

// RefusalReason explains why a receipt was refused as ownership evidence.
type RefusalReason string

const (
	// RefusalInvalidReceipt means the receipt is structurally unusable
	// (empty owned path or non-manager owner).
	RefusalInvalidReceipt RefusalReason = "invalid-receipt"
	// RefusalNoObservedRoot means no installation root was observed, so
	// containment cannot be enforced and no receipt can corroborate.
	RefusalNoObservedRoot RefusalReason = "no-observed-root"
	// RefusalMissingFile means the recorded owned path does not exist or
	// cannot be resolved.
	RefusalMissingFile RefusalReason = "missing-file"
	// RefusalSymlinkLeaf means the recorded owned path's leaf is a
	// symlink. A manager owning a launcher symlink never proves ownership
	// of its (possibly retargeted) destination.
	RefusalSymlinkLeaf RefusalReason = "symlink-leaf"
	// RefusalNotRegularFile means the recorded owned path's leaf is not a
	// regular file (for example a directory or device).
	RefusalNotRegularFile RefusalReason = "not-regular-file"
	// RefusalPathMismatch means the recorded owned path resolves to a
	// different canonical path than the observed executable. This refuses
	// look-alike layouts and hardlinks outside the claimed root even when
	// they contain the same bytes or are the same underlying file.
	RefusalPathMismatch RefusalReason = "path-mismatch"
	// RefusalOutsideRoot means the resolved owned path is not contained in
	// the observed installation root.
	RefusalOutsideRoot RefusalReason = "outside-root"
	// RefusalIdentityMismatch means the resolved owned path no longer
	// identifies the observed file (the filesystem changed between
	// resolution and identity comparison).
	RefusalIdentityMismatch RefusalReason = "identity-mismatch"
)

// Refusal records a receipt that was refused as ownership evidence, with
// the reason. Refusals are observational diagnostics, never authority.
type Refusal struct {
	Provider string
	Receipt  Receipt
	Reason   RefusalReason
	// ResolvedPath is the resolved owned path when resolution was possible.
	ResolvedPath string
}

// Owned is a receipt corroborated against the observed filesystem: the
// recorded path named a regular file whose parents resolved to the exact
// canonical path and file identity of the observed executable inside the
// observed root. Provenance (PackageName, SourceID) is preserved for later
// authoritative backend selection.
type Owned struct {
	Provider     string
	Receipt      Receipt
	ResolvedPath string
}

// UnavailableProvider records an applicable provider that could not
// complete its observation. Its potential conflicting ownership cannot be
// excluded, so the whole corroboration is non-actionable rather than
// silently proceeding with the providers that did answer.
type UnavailableProvider struct {
	Provider string
	Err      error
}

// Corroboration is the result of corroborating injected candidate receipts
// against the observed installation. It carries the corroborated evidence
// for installation.Classify plus the observational refusals and provider
// unavailability that a trusted runtime resolver must surface.
type Corroboration struct {
	// Executable is the observed executable evidence.
	Executable installation.ExecutableEvidence
	// Managers is the corroborated manager evidence for
	// installation.Classify: only receipts that survived corroboration
	// appear as owned files. Presence or an installed package alone
	// (manager-exists, PATH or layout heuristics) never appears here.
	Managers []installation.ManagerEvidence
	// Owned lists the corroborated receipts with provenance preserved.
	Owned []Owned
	// Refusals lists refused receipts for diagnostics. Refusals never
	// grant or revoke authority by themselves.
	Refusals []Refusal
	// Unavailable lists applicable providers whose observation did not
	// complete. When non-empty the corroboration is incomplete.
	Unavailable []UnavailableProvider
}

// Complete reports whether every applicable provider completed its
// observation. When false, the corroborated owner set cannot be trusted:
// an unavailable provider might hold conflicting ownership evidence, so
// callers must treat the installation as non-actionable instead of
// classifying on partial evidence.
func (c Corroboration) Complete() bool { return len(c.Unavailable) == 0 }

// Classify runs installation.Classify over the corroborated evidence. It
// fails closed while any applicable provider is unavailable or incomplete:
// the partial result is returned as an error, never as a classification.
func (c Corroboration) Classify(self installation.SelfManagedEvidence) (installation.Classification, error) {
	if !c.Complete() {
		names := make([]string, 0, len(c.Unavailable))
		for _, u := range c.Unavailable {
			names = append(names, u.Provider)
		}
		return installation.Classification{}, fmt.Errorf(
			"probe: applicable receipt provider(s) unavailable, ownership cannot be corroborated: %s",
			strings.Join(names, ", "))
	}
	return installation.Classify(c.Executable, c.Managers, self), nil
}

// Corroborate checks each injected candidate receipt against the observed
// filesystem facts. For every receipt from every applicable provider:
//
//   - The recorded owned path must be non-empty and speak for a real
//     manager owner; anything else is refused as invalid.
//   - The leaf must exist as a regular file. A symlink leaf is refused
//     regardless of where it points, so an owned launcher (even one
//     retargeted outside the claimed root) is never authority over its
//     destination. Directories and devices are refused the same way.
//   - Parent directories are resolved (EvalSymlinks), and the fully
//     resolved canonical path must exactly equal the observed executable's
//     canonical path, with matching file identity. A look-alike layout or
//     a hardlink whose canonical path lies outside the observed root is
//     therefore refused even when bytes or file identity match.
//   - The resolved path must be contained in the observed installation
//     root. Without an observed root, every receipt is refused because
//     containment cannot be enforced.
//
// A provider that applies to the observed platform but cannot complete its
// observation makes the whole corroboration non-actionable
// (Corroboration.Unavailable). A provider that does not apply to this
// platform is skipped entirely. No receipt or provider ever grants write
// authority; classification and auto-editability remain the pure value
// model's contract.
func Corroborate(facts Facts, providers []ReceiptProvider) Corroboration {
	c := Corroboration{Executable: facts.ExecutableEvidence()}
	var order []installation.Owner
	evidence := make(map[installation.Owner]*installation.ManagerEvidence)
	for _, p := range providers {
		if !p.AppliesTo(facts.platform) {
			continue
		}
		receipts, err := p.Receipts()
		if err != nil {
			c.Unavailable = append(c.Unavailable, UnavailableProvider{Provider: p.Name(), Err: err})
			continue
		}
		for _, r := range receipts {
			if !installation.IsManagerOwner(r.Manager) || r.OwnedPath == "" {
				c.Refusals = append(c.Refusals, Refusal{
					Provider: p.Name(),
					Receipt:  r,
					Reason:   RefusalInvalidReceipt,
				})
				continue
			}
			// A provider that supplies receipts is reporting an installed
			// cercano package from its manager; that presence is
			// observational and never ownership by itself.
			mgr := evidence[r.Manager]
			if mgr == nil {
				mgr = &installation.ManagerEvidence{
					Manager:          r.Manager,
					Present:          true,
					PackageInstalled: true,
					PackageName:      r.PackageName,
					Scope:            r.Scope,
				}
				evidence[r.Manager] = mgr
				order = append(order, r.Manager)
			}
			owned, refusal := probeOwned(facts, p.Name(), r)
			if refusal != nil {
				c.Refusals = append(c.Refusals, *refusal)
				continue
			}
			c.Owned = append(c.Owned, *owned)
			if !containsPath(mgr.OwnedFiles, owned.ResolvedPath) {
				mgr.OwnedFiles = append(mgr.OwnedFiles, owned.ResolvedPath)
			}
		}
	}
	// Deterministic output independent of provider registration order: an
	// ambiguous conflict must always report the same owner list.
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, m := range order {
		c.Managers = append(c.Managers, *evidence[m])
	}
	return c
}

// probeOwned corroborates one structurally valid receipt against the
// observed facts. It returns a refusal describing why ownership was not
// proven, or the corroborated owned receipt.
func probeOwned(facts Facts, provider string, r Receipt) (*Owned, *Refusal) {
	if facts.resolvedRoot == "" {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalNoObservedRoot}
	}
	abs, err := filepath.Abs(r.OwnedPath)
	if err != nil {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalInvalidReceipt}
	}
	leaf, err := os.Lstat(abs)
	if err != nil {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalMissingFile, ResolvedPath: abs}
	}
	if leaf.Mode()&os.ModeSymlink != 0 {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalSymlinkLeaf, ResolvedPath: abs}
	}
	if !leaf.Mode().IsRegular() {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalNotRegularFile, ResolvedPath: abs}
	}
	// The leaf is regular, so resolving the full path only resolves parent
	// directories; the canonical path is then compared exactly.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalMissingFile, ResolvedPath: abs}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalMissingFile, ResolvedPath: resolved}
	}
	// Exact canonical path and identity equality: refuses look-alike layouts
	// and hardlinks outside the observed root even when os.SameFile would
	// match a differently located copy of the same underlying file.
	if resolved != facts.resolvedPath || !os.SameFile(info, facts.exeIdentity) {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalPathMismatch, ResolvedPath: resolved}
	}
	if !contained(resolved, facts.resolvedRoot) {
		return nil, &Refusal{Provider: provider, Receipt: r, Reason: RefusalOutsideRoot, ResolvedPath: resolved}
	}
	return &Owned{Provider: provider, Receipt: r, ResolvedPath: resolved}, nil
}

func containsPath(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}
