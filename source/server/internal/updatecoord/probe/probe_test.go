package probe

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cercano/source/server/internal/updatecoord/installation"
)

// These tests corroborate real temporary files, symlinks and hardlinks on
// the host filesystem with fake receipt providers. They never run
// brew/dpkg/choco, never read a real manager database, never touch the
// developer's live installation, and observe only the test process's own
// binary when exercising the os.Executable default.

func canonical(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %q: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatalf("resolve %q: %v", abs, err)
	}
	return resolved
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host (%v); native behavior tested where supported", err)
	}
}

type installPaths struct {
	base     string
	root     string
	exe      string
	launcher string
}

// newInstall builds a temporary Cellar-style installation: a package root
// containing a regular executable plus a launcher symlink under a bin
// directory, exactly the shape the ownership contract reasons about.
func newInstall(t *testing.T) installPaths {
	t.Helper()
	base := canonical(t, t.TempDir())
	root := filepath.Join(base, "cellar", "cercano", "1.0")
	exe := filepath.Join(root, "bin", "cercano")
	writeFile(t, exe, "binary-v1")
	launcher := filepath.Join(base, "bin", "cercano")
	symlink(t, exe, launcher)
	return installPaths{base: base, root: root, exe: exe, launcher: launcher}
}

func mustObserve(t *testing.T, opts Options) Facts {
	t.Helper()
	f, err := Observe(opts)
	if err != nil {
		t.Fatalf("Observe(%+v): %v", opts, err)
	}
	return f
}

type fakeProvider struct {
	name      string
	platforms []string
	receipts  []Receipt
	err       error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) AppliesTo(platform string) bool {
	for _, p := range f.platforms {
		if p == platform {
			return true
		}
	}
	return false
}

func (f *fakeProvider) Receipts() ([]Receipt, error) { return f.receipts, f.err }

func brewReceipt(owned string) Receipt {
	return Receipt{
		Manager:     installation.OwnerHomebrew,
		PackageName: "cercano",
		SourceID:    "brew-test-record",
		Scope:       installation.ScopeUser,
		OwnedPath:   owned,
	}
}

func hostPlatforms() []string { return []string{"darwin", "linux", "windows"} }

func containsString(s, sub string) bool { return strings.Contains(s, sub) }

func TestObserve_RegularExecutableUnderRoot(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	if f.ResolvedPath() != p.exe {
		t.Fatalf("resolved path = %q, want %q", f.ResolvedPath(), p.exe)
	}
	if f.ResolvedRoot() != p.root {
		t.Fatalf("resolved root = %q, want %q", f.ResolvedRoot(), p.root)
	}
	if f.LinkPath() != "" {
		t.Fatalf("direct regular file must not report a launcher link, got %q", f.LinkPath())
	}
	if f.Platform() != runtime.GOOS || f.Arch() != runtime.GOARCH {
		t.Fatalf("platform/arch = %q/%q, want %q/%q", f.Platform(), f.Arch(), runtime.GOOS, runtime.GOARCH)
	}
	if f.IsDevelopmentBuild() {
		t.Fatal("no marker was supplied, so no development evidence may exist")
	}
	ev := f.ExecutableEvidence()
	if ev.ResolvedPath != p.exe || ev.ResolvedRoot != p.root || ev.Platform != runtime.GOOS ||
		ev.Arch != runtime.GOARCH || ev.LinkPath != "" || ev.IsDevelopmentBuild {
		t.Fatalf("evidence not carried: %+v", ev)
	}
	if err := f.StillObserved(); err != nil {
		t.Fatalf("stable observation must validate: %v", err)
	}
}

func TestObserve_LauncherSymlinkIsInformationalOnly(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.launcher, RootPath: p.root})
	if f.ResolvedPath() != p.exe {
		t.Fatalf("resolved path = %q, want the regular target %q", f.ResolvedPath(), p.exe)
	}
	if f.LinkPath() != p.launcher {
		t.Fatalf("original link path %q, want %q kept informationally", f.LinkPath(), p.launcher)
	}
	if f.ExecutableEvidence().LinkPath != p.launcher {
		t.Fatalf("link path not carried into evidence: %+v", f.ExecutableEvidence())
	}
}

func TestObserve_DefaultUsesActualRunningExecutable(t *testing.T) {
	// No injected path: the probe must observe the actual running
	// executable (here the test binary itself, never the developer's live
	// agent) as a regular file.
	f := mustObserve(t, Options{})
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if want := canonical(t, self); f.ResolvedPath() != want {
		t.Fatalf("resolved path = %q, want %q", f.ResolvedPath(), want)
	}
	if f.OriginalPath() != self {
		t.Fatalf("original path = %q, want %q", f.OriginalPath(), self)
	}
}

func TestObserve_FailsClosed(t *testing.T) {
	p := newInstall(t)
	missing := filepath.Join(p.base, "no-such-file")
	dir := filepath.Join(p.base, "cellar")
	sibling := filepath.Join(p.base, "cellar-other", "1.0", "bin", "cercano")
	writeFile(t, sibling, "not-mine")
	rootFile := filepath.Join(p.base, "plain-file")
	writeFile(t, rootFile, "not-a-root")

	cases := []struct {
		name string
		opts Options
	}{
		{"missing executable", Options{ExecutablePath: missing, RootPath: p.root}},
		{"executable is a directory", Options{ExecutablePath: dir, RootPath: p.root}},
		{"missing root", Options{ExecutablePath: p.exe, RootPath: missing}},
		{"root is not a directory", Options{ExecutablePath: p.exe, RootPath: rootFile}},
		{"prefix sibling is not containment", Options{ExecutablePath: sibling, RootPath: p.root}},
		{"executable equals root", Options{ExecutablePath: p.root, RootPath: p.root}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Observe(tc.opts)
			if err == nil {
				t.Fatalf("observation of %+v must fail closed, got %+v", tc.opts, f)
			}
		})
	}
}

func TestStillObserved_FailsClosedOnChange(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	if err := f.StillObserved(); err != nil {
		t.Fatalf("unchanged observation must validate: %v", err)
	}

	// Replace the executable under a different identity: the path still
	// exists and is regular, but it is a different file.
	replacement := p.exe + ".new"
	writeFile(t, replacement, "binary-v2")
	if err := os.Rename(replacement, p.exe); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := f.StillObserved(); err == nil {
		t.Fatal("a rewritten executable must fail closed; identity changed silently")
	}

	// A fresh observation of the replaced file is valid again and the new
	// identity is what later checks compare against.
	f2 := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	if err := f2.StillObserved(); err != nil {
		t.Fatalf("re-observed replacement must validate: %v", err)
	}

	if err := os.Remove(p.exe); err != nil {
		t.Fatalf("remove exe: %v", err)
	}
	if err := f2.StillObserved(); err == nil {
		t.Fatal("a missing executable must fail closed")
	}

	// Recreating the root directory changes its identity even though the
	// executable itself is untouched.
	p2 := newInstall(t)
	f3 := mustObserve(t, Options{ExecutablePath: p2.exe, RootPath: p2.root})
	if err := os.RemoveAll(p2.root); err != nil {
		t.Fatalf("remove root: %v", err)
	}
	if err := os.MkdirAll(p2.root, 0o755); err != nil {
		t.Fatalf("recreate root: %v", err)
	}
	if err := f3.StillObserved(); err == nil {
		t.Fatal("a replaced root directory must fail closed")
	}
}

func TestCorroborate_OwnedReceiptsProveManagerOwnership(t *testing.T) {
	p := newInstall(t)
	// A receipt may reach the executable through a symlinked parent
	// directory (like a Cellar alias): the leaf stays a regular file and
	// the canonical path is exactly the observed executable.
	current := filepath.Join(p.base, "current")
	symlink(t, p.root, current)
	viaAlias := filepath.Join(current, "bin", "cercano")

	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe), brewReceipt(viaAlias)},
	}})
	if !c.Complete() {
		t.Fatalf("corroboration incomplete: %+v", c.Unavailable)
	}
	if len(c.Owned) != 2 {
		t.Fatalf("owned receipts = %d, want 2 (direct and symlinked-parent): %+v", len(c.Owned), c.Owned)
	}
	for _, o := range c.Owned {
		if o.ResolvedPath != p.exe {
			t.Fatalf("owned receipt resolved to %q, want %q", o.ResolvedPath, p.exe)
		}
		if o.Receipt.PackageName != "cercano" || o.Receipt.SourceID != "brew-test-record" {
			t.Fatalf("provenance not preserved for later backend selection: %+v", o.Receipt)
		}
	}
	if len(c.Refusals) != 0 {
		t.Fatalf("unexpected refusals: %+v", c.Refusals)
	}
	if len(c.Managers) != 1 || c.Managers[0].Manager != installation.OwnerHomebrew {
		t.Fatalf("manager evidence = %+v", c.Managers)
	}
	if got := c.Managers[0].OwnedFiles; len(got) != 1 || got[0] != p.exe {
		t.Fatalf("owned files = %v, want exactly [%q]", got, p.exe)
	}
	if c.Managers[0].PackageName != "cercano" || !c.Managers[0].Present || !c.Managers[0].PackageInstalled {
		t.Fatalf("manager evidence incomplete: %+v", c.Managers[0])
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerHomebrew || got.Status != installation.StatusActionable ||
		got.Reason != installation.ReasonIdentified {
		t.Fatalf("classification = %+v, want actionable homebrew", got)
	}
	if got.AutoEditable {
		t.Fatal("package-manager ownership must never be auto-editable")
	}
}

func TestCorroborate_OwnedLauncherSymlinkIsNeverAuthority(t *testing.T) {
	p := newInstall(t)
	// A manager-owned launcher symlink pointing into the claimed root, and
	// one retargeted outside it: both are refused as ownership proof of
	// their destinations because the leaf is a symlink.
	outsideTarget := filepath.Join(p.base, "manual", "cercano")
	writeFile(t, outsideTarget, "manual-build")
	retargeted := filepath.Join(p.base, "bin", "cercano-retargeted")
	symlink(t, outsideTarget, retargeted)

	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.launcher), brewReceipt(retargeted)},
	}})
	if len(c.Owned) != 0 {
		t.Fatalf("symlink-leaved receipts must not corroborate: %+v", c.Owned)
	}
	if len(c.Refusals) != 2 || c.Refusals[0].Reason != RefusalSymlinkLeaf || c.Refusals[1].Reason != RefusalSymlinkLeaf {
		t.Fatalf("refusals = %+v, want two symlink-leaf refusals", c.Refusals)
	}
	if len(c.Managers) != 1 || len(c.Managers[0].OwnedFiles) != 0 {
		t.Fatalf("manager must be recorded as present/installed without ownership: %+v", c.Managers)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Status != installation.StatusUnknown {
		t.Fatalf("launcher ownership leaked into classification: %+v", got)
	}
	if got.Reason != installation.ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("reason = %q, want manager-does-not-own-executable", got.Reason)
	}
}

func TestCorroborate_HardlinkOutsideRootIsNotOwnership(t *testing.T) {
	p := newInstall(t)
	outside := filepath.Join(p.base, "outside", "cercano")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Link(p.exe, outside); err != nil {
		t.Skipf("hardlinks unavailable on this host (%v); native behavior tested where supported", err)
	}

	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(outside)},
	}})
	if len(c.Owned) != 0 {
		t.Fatalf("a hardlink outside the claimed root must not corroborate: %+v", c.Owned)
	}
	if len(c.Refusals) != 1 || c.Refusals[0].Reason != RefusalPathMismatch {
		t.Fatalf("refusals = %+v, want one path-mismatch", c.Refusals)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Status != installation.StatusUnknown || got.AutoEditable {
		t.Fatalf("hardlink identity granted ownership: %+v", got)
	}
}

func TestCorroborate_LookalikeLayoutAndPrefixSiblingsRefused(t *testing.T) {
	p := newInstall(t)
	// A sibling regular file with identical bytes, a look-alike Cellar
	// layout elsewhere, and the package directory itself: none of them is
	// the observed executable.
	sibling := filepath.Join(p.base, "cellar-other", "1.0", "bin", "cercano")
	writeFile(t, sibling, "binary-v1")
	lookalike := filepath.Join(p.base, "opt", "homebrew", "Cellar", "cercano", "1.0", "bin", "cercano")
	writeFile(t, lookalike, "binary-v1")

	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts: []Receipt{
			brewReceipt(sibling),
			brewReceipt(lookalike),
			brewReceipt(p.root),
			brewReceipt(filepath.Join(p.base, "gone", "cercano")),
		},
	}})
	if len(c.Owned) != 0 {
		t.Fatalf("no look-alike may corroborate: %+v", c.Owned)
	}
	want := []RefusalReason{RefusalPathMismatch, RefusalPathMismatch, RefusalNotRegularFile, RefusalMissingFile}
	if len(c.Refusals) != len(want) {
		t.Fatalf("refusals = %+v, want %v", c.Refusals, want)
	}
	for i, r := range c.Refusals {
		if r.Reason != want[i] {
			t.Fatalf("refusal %d reason = %q, want %q", i, r.Reason, want[i])
		}
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Status != installation.StatusUnknown {
		t.Fatalf("layout or sibling paths granted ownership: %+v", got)
	}
}

func TestCorroborate_InvalidReceiptsCreateNoEvidence(t *testing.T) {
	p := newInstall(t)
	notAManager := brewReceipt("")
	notAManager.Manager = installation.Owner("not-a-manager")

	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{notAManager, brewReceipt("")},
	}})
	if len(c.Owned) != 0 || len(c.Managers) != 0 {
		t.Fatalf("invalid receipts must not produce evidence: %+v", c)
	}
	if len(c.Refusals) != 2 || c.Refusals[0].Reason != RefusalInvalidReceipt || c.Refusals[1].Reason != RefusalInvalidReceipt {
		t.Fatalf("refusals = %+v, want two invalid-receipt refusals", c.Refusals)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Reason != installation.ReasonInsufficientEvidence {
		t.Fatalf("classification = %+v, want unknown/insufficient-evidence", got)
	}
}

func TestCorroborate_NoObservedRootRefusesAllReceipts(t *testing.T) {
	p := newInstall(t)
	// Without an observed root, containment cannot be enforced, so even a
	// receipt naming the exact executable is refused rather than silently
	// trusted.
	f := mustObserve(t, Options{ExecutablePath: p.exe})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe)},
	}})
	if len(c.Owned) != 0 {
		t.Fatalf("receipt corroborated without a root: %+v", c.Owned)
	}
	if len(c.Refusals) != 1 || c.Refusals[0].Reason != RefusalNoObservedRoot {
		t.Fatalf("refusals = %+v, want one no-observed-root", c.Refusals)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Status == installation.StatusActionable || got.AutoEditable {
		t.Fatalf("classification = %+v, want unknown non-actionable", got)
	}
}

func TestCorroborate_AmbiguousOwnersConflict(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	aptReceipt := Receipt{
		Manager:     installation.OwnerAPT,
		PackageName: "cercano",
		SourceID:    "dpkg-test-record",
		Scope:       installation.ScopeMachine,
		OwnedPath:   p.exe,
	}
	c := Corroborate(f, []ReceiptProvider{
		&fakeProvider{name: "brew-test", platforms: hostPlatforms(), receipts: []Receipt{brewReceipt(p.exe)}},
		&fakeProvider{name: "apt-test", platforms: hostPlatforms(), receipts: []Receipt{aptReceipt}},
	})
	if !c.Complete() {
		t.Fatalf("corroboration incomplete: %+v", c.Unavailable)
	}
	if len(c.Owned) != 2 {
		t.Fatalf("both managers verifiably own the file: %+v", c.Owned)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Status != installation.StatusNonActionable || got.Reason != installation.ReasonAmbiguousConflict {
		t.Fatalf("classification = %+v, want non-actionable ambiguous conflict", got)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.AutoEditable {
		t.Fatalf("ambiguous ownership must grant nothing: %+v", got)
	}
	if len(got.Conflicting) != 2 || got.Conflicting[0] != installation.OwnerAPT || got.Conflicting[1] != installation.OwnerHomebrew {
		t.Fatalf("conflicting owners = %v, want [apt homebrew]", got.Conflicting)
	}
}

func TestCorroborate_UnavailableApplicableProviderIsNonActionable(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	brewOK := &fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe)},
	}
	aptDown := &fakeProvider{
		name:      "apt-test",
		platforms: hostPlatforms(),
		err:       errors.New("backend unavailable"),
	}

	c := Corroborate(f, []ReceiptProvider{brewOK, aptDown})
	if c.Complete() {
		t.Fatal("an unavailable applicable provider must make the corroboration incomplete")
	}
	if len(c.Unavailable) != 1 || c.Unavailable[0].Provider != "apt-test" {
		t.Fatalf("unavailable = %+v, want apt-test recorded", c.Unavailable)
	}
	// One corroborated owner is NOT enough: the unavailable provider might
	// hold conflicting ownership, so classification fails closed.
	_, err := c.Classify(installation.SelfManagedEvidence{})
	if err == nil {
		t.Fatal("classification on incomplete corroboration must fail closed")
	} else if !containsString(err.Error(), "apt-test") {
		t.Fatalf("error %q must name the unavailable provider", err)
	}

	// A sole unavailable applicable provider with no other evidence is
	// equally non-actionable, never silently "no receipts".
	c2 := Corroborate(f, []ReceiptProvider{aptDown})
	if c2.Complete() || len(c2.Owned) != 0 {
		t.Fatalf("unavailable provider silently ignored: %+v", c2)
	}
	if _, err := c2.Classify(installation.SelfManagedEvidence{}); err == nil {
		t.Fatal("classification must fail closed for a sole unavailable provider")
	}
}

func TestCorroborate_NonApplicableProviderSkippedEntirely(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	// A provider that never speaks for this platform (and is even
	// unavailable) cannot hold ownership here, so it is skipped rather than
	// blocking the observation.
	otherPlatform := "windows"
	if runtime.GOOS == "windows" {
		otherPlatform = "linux"
	}
	otherOnly := &fakeProvider{
		name:      "other-platform-test",
		platforms: []string{otherPlatform},
		err:       errors.New("backend unavailable"),
	}
	brewOK := &fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe)},
	}
	c := Corroborate(f, []ReceiptProvider{otherOnly, brewOK})
	if !c.Complete() || len(c.Unavailable) != 0 {
		t.Fatalf("non-applicable provider must not affect completeness: %+v", c.Unavailable)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerHomebrew || got.Status != installation.StatusActionable {
		t.Fatalf("classification = %+v, want actionable homebrew", got)
	}
}

func TestCorroborate_WindowsReceiptPathRefusedOnUnixHost(t *testing.T) {
	// A Windows-style receipt path cannot resolve on a Unix host, so it is
	// refused as missing rather than misread; native Windows behavior
	// (drive letters, reparse points) is exercised by the native test run.
	if runtime.GOOS == "windows" {
		t.Skip("host is windows; native behavior covered by the native run")
	}
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "choco-test",
		platforms: hostPlatforms(),
		receipts: []Receipt{{
			Manager:     installation.OwnerChocolatey,
			PackageName: "cercano",
			SourceID:    "choco-test-record",
			Scope:       installation.ScopeMachine,
			OwnedPath:   `C:\ProgramData\chocolatey\bin\cercano.exe`,
		}},
	}})
	if len(c.Owned) != 0 {
		t.Fatalf("windows receipt path corroborated on %s: %+v", runtime.GOOS, c.Owned)
	}
	if len(c.Refusals) != 1 || c.Refusals[0].Reason != RefusalMissingFile {
		t.Fatalf("refusals = %+v, want one missing-file", c.Refusals)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Status == installation.StatusActionable {
		t.Fatalf("windows path granted ownership on %s: %+v", runtime.GOOS, got)
	}
}

func TestCorroborate_AuthoritativeNoReceiptsIsNotOwnership(t *testing.T) {
	// A provider that authoritatively observes no receipts (manager not
	// installed, or no cercano package) produces no evidence at all:
	// manager presence, PATH appearance and layout alone never appear.
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  nil,
	}})
	if !c.Complete() || len(c.Owned) != 0 || len(c.Managers) != 0 || len(c.Refusals) != 0 {
		t.Fatalf("empty authoritative observation must produce nothing: %+v", c)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerUnknown || got.Reason != installation.ReasonInsufficientEvidence {
		t.Fatalf("classification = %+v, want unknown/insufficient-evidence", got)
	}
}

func TestCorroborate_ChocolateyOwnershipGrantsNoCooperatingContract(t *testing.T) {
	// Corroborated Chocolatey receipts prove manager ownership for updates
	// through Chocolatey itself, but this package invents no Chocolatey
	// registry format and never grants the self-update delegation: that
	// contract stays refused until real packaging and a verified contract
	// exist.
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "choco-test",
		platforms: hostPlatforms(),
		receipts: []Receipt{{
			Manager:     installation.OwnerChocolatey,
			PackageName: "cercano",
			SourceID:    "choco-test-record",
			Scope:       installation.ScopeUser,
			OwnedPath:   p.exe,
		}},
	}})
	if !c.Complete() || len(c.Owned) != 1 {
		t.Fatalf("chocolatey receipts must corroborate when injected: %+v", c)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerChocolatey || got.Status != installation.StatusActionable {
		t.Fatalf("classification = %+v, want actionable chocolatey manager ownership", got)
	}
	if got.AutoEditable {
		t.Fatal("chocolatey-owned files must never be auto-editable")
	}
	// Without a verified cooperating-installer contract the delegation
	// remains refused even with corroborated ownership.
	delegated := installation.ClassifyWithDelegation(c.Executable, c.Managers, installation.SelfManagedEvidence{}, installation.DelegationEvidence{})
	if delegated.SelfUpdateRole != installation.SelfUpdateRoleNone {
		t.Fatalf("zero-evidence delegation must grant no role: %+v", delegated)
	}
}

func TestDevelopmentBuild_MarkerIsPositiveEvidenceAndNeverAutoEditable(t *testing.T) {
	p := newInstall(t)
	marker := func(resolvedPath string) bool { return true }
	f := mustObserve(t, Options{
		ExecutablePath:    p.exe,
		RootPath:          p.root,
		DevelopmentMarker: marker,
	})
	if !f.IsDevelopmentBuild() {
		t.Fatal("positive marker must be observed")
	}
	ev := f.ExecutableEvidence()
	if !ev.IsDevelopmentBuild {
		t.Fatalf("marker not carried into evidence: %+v", ev)
	}

	// Development evidence dominates even a corroborated manager claim and
	// explicit self-managed enrollment, and never becomes auto-editable.
	c := Corroborate(f, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe)},
	}})
	if !c.Complete() || len(c.Owned) != 1 {
		t.Fatalf("manager receipts still corroborate observationally: %+v", c)
	}
	got, err := c.Classify(installation.SelfManagedEvidence{
		Enrolled:   true,
		Scope:      installation.ScopeUser,
		Root:       p.root,
		Executable: p.exe,
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got.Identity.Owner != installation.OwnerDevelopment || got.Status != installation.StatusNonActionable ||
		got.Reason != installation.ReasonDevelopmentCheckout {
		t.Fatalf("classification = %+v, want non-actionable development checkout", got)
	}
	if got.AutoEditable {
		t.Fatal("a development build must never be auto-editable")
	}

	// Without positive marker evidence the same installation stays a
	// manager-owned, actionable installation (updates through the manager,
	// still not auto-editable).
	f2 := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	c2 := Corroborate(f2, []ReceiptProvider{&fakeProvider{
		name:      "brew-test",
		platforms: hostPlatforms(),
		receipts:  []Receipt{brewReceipt(p.exe)},
	}})
	got2, err := c2.Classify(installation.SelfManagedEvidence{})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got2.Identity.Owner != installation.OwnerHomebrew || got2.AutoEditable {
		t.Fatalf("classification without marker = %+v, want actionable non-editable homebrew", got2)
	}
}
