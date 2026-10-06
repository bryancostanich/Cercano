package installation

import "testing"

// These tests are pure: they classify caller-supplied evidence only. They
// never touch the filesystem, run package managers, or reach the network, and
// they never run against the developer's real installation.

func brewEvidence(installed bool, ownedFiles ...string) ManagerEvidence {
	return ManagerEvidence{
		Manager:          OwnerHomebrew,
		Present:          true,
		PackageInstalled: installed,
		PackageName:      "cercano",
		OwnedFiles:       ownedFiles,
		Scope:            ScopeUser,
	}
}

func TestClassify_HomebrewOwnsResolvedTarget(t *testing.T) {
	// The launcher is a Homebrew symlink; the evidence carries both the
	// link path and the resolved Cellar path, and the manager's owned-file
	// records contain the resolved value.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		Arch:         "arm64",
		ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		ResolvedRoot: "/opt/homebrew/Cellar/cercano/0.7.0",
		LinkPath:     "/opt/homebrew/bin/cercano",
	}
	got := Classify(exe, []ManagerEvidence{brewEvidence(true, exe.ResolvedPath, exe.LinkPath)}, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerHomebrew {
		t.Fatalf("owner = %q, want homebrew", got.Identity.Owner)
	}
	if got.Status != StatusActionable {
		t.Fatalf("status = %q, want actionable", got.Status)
	}
	if got.Reason != ReasonIdentified {
		t.Fatalf("reason = %q, want identified", got.Reason)
	}
	if got.AutoEditable {
		t.Fatal("package-manager installations must never be auto-editable")
	}
	if got.Identity.Scope != ScopeUser {
		t.Fatalf("scope = %q, want user", got.Identity.Scope)
	}
	if got.Identity.Platform != "darwin" || got.Identity.Arch != "arm64" {
		t.Fatalf("platform/arch not carried into identity: %q/%q", got.Identity.Platform, got.Identity.Arch)
	}
	if got.Identity.Source != "homebrew" {
		t.Fatalf("source = %q, want homebrew", got.Identity.Source)
	}
	if got.Identity.Executable != exe.ResolvedPath || got.Identity.Root != exe.ResolvedRoot {
		t.Fatalf("identity paths not carried: %+v", got.Identity)
	}
}

func TestClassify_HomebrewLinkOnlyIsNotTargetOwnership(t *testing.T) {
	exe := ExecutableEvidence{Platform: "darwin", ResolvedPath: "/tmp/manual/cercano", LinkPath: "/opt/homebrew/bin/cercano"}
	got := Classify(exe, []ManagerEvidence{brewEvidence(true, exe.LinkPath)}, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("link-only evidence claimed target: %+v", got)
	}
}

func TestClassify_ManagerPresentButDoesNotOwnExecutable(t *testing.T) {
	// brew exists and has a cercano package, but the running executable is
	// a different binary (a development checkout elsewhere). The manager
	// being present and its package existing must not transfer ownership.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		ResolvedPath: "/Users/dev/src/Cercano/bin/cercano",
	}
	managers := []ManagerEvidence{brewEvidence(true,
		"/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		"/opt/homebrew/bin/cercano",
	)}
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown", got.Identity.Owner)
	}
	if got.Status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", got.Status)
	}
	if got.Reason != ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("reason = %q, want manager-does-not-own-executable", got.Reason)
	}
	if got.AutoEditable {
		t.Fatal("must not be auto-editable")
	}
}

func TestClassify_MultipleManagersExistingButNotOwning(t *testing.T) {
	// brew and choco both exist and both report cercano installed, but
	// neither owns the running executable. This is the classic
	// misclassification trap of the old DetectInstallMethod; the result
	// must be unknown with an explicit reason.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		ResolvedPath: "/usr/local/bin/cercano",
	}
	managers := []ManagerEvidence{
		brewEvidence(true, "/opt/homebrew/bin/cercano"),
		{
			Manager:          OwnerAPT,
			Present:          true,
			PackageInstalled: true,
			PackageName:      "cercano",
			OwnedFiles:       []string{"/usr/bin/cercano"},
			Scope:            ScopeMachine,
		},
	}
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown || got.Status != StatusUnknown {
		t.Fatalf("got owner=%q status=%q, want unknown/unknown", got.Identity.Owner, got.Status)
	}
	if got.Reason != ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("reason = %q, want manager-does-not-own-executable", got.Reason)
	}
}

func TestClassify_WrongRoot(t *testing.T) {
	// The manager owns cercano files under a different root (Intel prefix
	// vs ARM prefix). Owned files exist but none matches the running
	// executable, so ownership is not proven.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		Arch:         "arm64",
		ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		ResolvedRoot: "/opt/homebrew/Cellar/cercano/0.7.0",
	}
	managers := []ManagerEvidence{brewEvidence(true,
		"/usr/local/Cellar/cercano/0.7.0/bin/cercano",
		"/usr/local/bin/cercano",
	)}
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown (wrong root is not ownership)", got.Identity.Owner)
	}
	if got.Reason != ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("reason = %q, want manager-does-not-own-executable", got.Reason)
	}
}

func TestClassify_RootOrPrefixIsNotProof(t *testing.T) {
	// The owned-file record names the package directory itself, not the
	// executable. Prefix/directory containment must not count as proof.
	exe := ExecutableEvidence{
		Platform:     "linux",
		ResolvedPath: "/opt/cercano/bin/cercano",
	}
	managers := []ManagerEvidence{
		{
			Manager:          OwnerAPT,
			Present:          true,
			PackageInstalled: true,
			PackageName:      "cercano",
			OwnedFiles:       []string{"/opt/cercano"},
			Scope:            ScopeMachine,
		},
	}
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown (directory prefix is not owned-file proof)", got.Identity.Owner)
	}
}

func TestClassify_PathHeuristicIsNeverProof(t *testing.T) {
	// The executable literally sits inside a Homebrew-style Cellar layout
	// and the caller even supplies a layout hint, but no manager verifiably
	// owns the file. Layout must not create ownership.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		ResolvedRoot: "/opt/homebrew/Cellar/cercano/0.7.0",
		LayoutHints:  []string{"under /opt/homebrew", "cellar-layout"},
	}
	managers := []ManagerEvidence{brewEvidence(true)} // present + installed, no owned files
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown || got.Status != StatusUnknown {
		t.Fatalf("got owner=%q status=%q, want unknown/unknown", got.Identity.Owner, got.Status)
	}
	if got.Reason != ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("reason = %q, want manager-does-not-own-executable", got.Reason)
	}
}

func TestClassify_PresentManagerWithoutInstalledPackage(t *testing.T) {
	// brew exists on PATH but no cercano package is installed: the classic
	// PATH-appearance trap. Insufficient evidence, never homebrew.
	exe := ExecutableEvidence{ResolvedPath: "/usr/local/bin/cercano"}
	got := Classify(exe, []ManagerEvidence{brewEvidence(false)}, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown", got.Identity.Owner)
	}
	if got.Reason != ReasonInsufficientEvidence {
		t.Fatalf("reason = %q, want insufficient-evidence", got.Reason)
	}
}

func TestClassify_DirectUnknownInstallation(t *testing.T) {
	// A direct install with no manager evidence and no enrollment is
	// unknown and non-actionable; it must not silently become
	// self-managed.
	exe := ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: `C:\Users\dev\AppData\Local\Programs\Cercano\cercano.exe`,
		ResolvedRoot: `C:\Users\dev\AppData\Local\Programs\Cercano`,
	}
	got := Classify(exe, nil, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown", got.Identity.Owner)
	}
	if got.Status != StatusUnknown {
		t.Fatalf("status = %q, want unknown", got.Status)
	}
	if got.Reason != ReasonInsufficientEvidence {
		t.Fatalf("reason = %q, want insufficient-evidence", got.Reason)
	}
	if got.AutoEditable {
		t.Fatal("an unenrolled direct install must not be auto-editable")
	}
	if got.Identity.Source != "" {
		t.Fatalf("an unowned installation must have no verified source, got %q", got.Identity.Source)
	}
}

func TestClassify_DevelopmentBuildNeverAutoEditable(t *testing.T) {
	// A development build dominates all other evidence, even a manager
	// owned-file claim and enrollment, and is never auto-editable.
	exe := ExecutableEvidence{
		Platform:           "darwin",
		ResolvedPath:       "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		IsDevelopmentBuild: true,
	}
	got := Classify(exe,
		[]ManagerEvidence{brewEvidence(true, exe.ResolvedPath)},
		SelfManagedEvidence{Enrolled: true, Scope: ScopeUser})
	if got.Identity.Owner != OwnerDevelopment {
		t.Fatalf("owner = %q, want development", got.Identity.Owner)
	}
	if got.Status != StatusNonActionable {
		t.Fatalf("status = %q, want non-actionable", got.Status)
	}
	if got.Reason != ReasonDevelopmentCheckout {
		t.Fatalf("reason = %q, want development-checkout", got.Reason)
	}
	if got.AutoEditable {
		t.Fatal("development builds must never be auto-editable")
	}
}

func TestClassify_SelfManagedRequiresExplicitEnrollment(t *testing.T) {
	exe := ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: `C:\Users\dev\AppData\Local\Programs\Cercano\versions\0.7.0\cercano.exe`,
		ResolvedRoot: `C:\Users\dev\AppData\Local\Programs\Cercano\versions\0.7.0`,
		LayoutHints:  []string{"versioned-directories"},
	}

	// Same layout, no enrollment: layout alone must not enroll.
	got := Classify(exe, nil, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown || got.AutoEditable {
		t.Fatalf("layout without enrollment: got owner=%q autoEditable=%v", got.Identity.Owner, got.AutoEditable)
	}

	// Explicit enrollment in user scope: the only auto-editable result.
	got = Classify(exe, nil, SelfManagedEvidence{Root: exe.ResolvedRoot, Executable: exe.ResolvedPath, Enrolled: true, Scope: ScopeUser})
	if got.Identity.Owner != OwnerSelfManaged {
		t.Fatalf("owner = %q, want self-managed", got.Identity.Owner)
	}
	if got.Status != StatusActionable || !got.AutoEditable {
		t.Fatalf("enrolled user-scope install: status=%q autoEditable=%v, want actionable/true", got.Status, got.AutoEditable)
	}
	if got.Identity.Source != "tuf" {
		t.Fatalf("source = %q, want tuf", got.Identity.Source)
	}

	// Machine-scope enrollment is deferred by approved decision.
	got = Classify(exe, nil, SelfManagedEvidence{Root: exe.ResolvedRoot, Executable: exe.ResolvedPath, Enrolled: true, Scope: ScopeMachine})
	if got.Status != StatusNonActionable || got.AutoEditable {
		t.Fatalf("machine-scope enrollment: status=%q autoEditable=%v", got.Status, got.AutoEditable)
	}
	if got.Reason != ReasonMachineScopeDeferred {
		t.Fatalf("reason = %q, want machine-scope-deferred", got.Reason)
	}

	// Unknown-scope enrollment is incomplete.
	got = Classify(exe, nil, SelfManagedEvidence{Root: exe.ResolvedRoot, Executable: exe.ResolvedPath, Enrolled: true, Scope: ScopeUnknown})
	if got.Status != StatusNonActionable || got.Reason != ReasonIncompleteEnrollment {
		t.Fatalf("unknown-scope enrollment: status=%q reason=%q", got.Status, got.Reason)
	}
}

func TestClassify_AmbiguousConflictNoPreferenceFallback(t *testing.T) {
	// Two managers both verifiably own the executable's canonical path.
	// The result must be non-actionable with both owners reported; there is
	// no preference order that silently picks one.
	exe := ExecutableEvidence{
		Platform:     "linux",
		ResolvedPath: "/usr/bin/cercano",
	}
	managers := []ManagerEvidence{
		{
			Manager:          OwnerAPT,
			Present:          true,
			PackageInstalled: true,
			PackageName:      "cercano",
			OwnedFiles:       []string{"/usr/bin/cercano"},
			Scope:            ScopeMachine,
		},
		{
			Manager:          OwnerHomebrew,
			Present:          true,
			PackageInstalled: true,
			PackageName:      "cercano",
			OwnedFiles:       []string{"/usr/bin/cercano"},
			Scope:            ScopeUser,
		},
	}
	got := Classify(exe, managers, SelfManagedEvidence{})
	if got.Status != StatusNonActionable {
		t.Fatalf("status = %q, want non-actionable", got.Status)
	}
	if got.Reason != ReasonAmbiguousConflict {
		t.Fatalf("reason = %q, want ambiguous-conflict", got.Reason)
	}
	if got.Identity.Owner != OwnerUnknown {
		t.Fatalf("owner = %q, want unknown (no preference fallback)", got.Identity.Owner)
	}
	if got.AutoEditable {
		t.Fatal("ambiguous ownership must not be auto-editable")
	}
	if len(got.Conflicting) != 2 || got.Conflicting[0] != OwnerAPT || got.Conflicting[1] != OwnerHomebrew {
		t.Fatalf("conflicting = %v, want [apt homebrew]", got.Conflicting)
	}
}

func TestClassify_EnrollmentConflictsWithPackageManagerOwnership(t *testing.T) {
	// Explicit enrollment cannot coexist with a package-manager claim over
	// the same files; that is exactly the concurrent-updater hazard the
	// spec forbids.
	exe := ExecutableEvidence{
		Platform:     "windows",
		ResolvedPath: `C:\ProgramData\chocolatey\bin\cercano.exe`,
	}
	managers := []ManagerEvidence{
		{
			Manager:          OwnerChocolatey,
			Present:          true,
			PackageInstalled: true,
			PackageName:      "cercano",
			OwnedFiles:       []string{`C:\ProgramData\chocolatey\bin\cercano.exe`},
			Scope:            ScopeMachine,
		},
	}
	got := Classify(exe, managers, SelfManagedEvidence{Enrolled: true, Scope: ScopeUser})
	if got.Status != StatusNonActionable || got.Reason != ReasonAmbiguousConflict {
		t.Fatalf("got status=%q reason=%q, want non-actionable/ambiguous-conflict", got.Status, got.Reason)
	}
	if got.AutoEditable {
		t.Fatal("conflicted enrollment must not be auto-editable")
	}
}

func TestClassify_ScopesAndPlatforms(t *testing.T) {
	cases := []struct {
		name      string
		exe       ExecutableEvidence
		managers  []ManagerEvidence
		self      SelfManagedEvidence
		wantOwner Owner
		wantScope Scope
	}{
		{
			name: "chocolatey machine scope on windows",
			exe: ExecutableEvidence{
				Platform:     "windows",
				Arch:         "amd64",
				ResolvedPath: `C:\ProgramData\chocolatey\lib\cercano\tools\cercano.exe`,
			},
			managers: []ManagerEvidence{{
				Manager:          OwnerChocolatey,
				Present:          true,
				PackageInstalled: true,
				PackageName:      "cercano",
				OwnedFiles:       []string{`C:\ProgramData\chocolatey\lib\cercano\tools\cercano.exe`},
				Scope:            ScopeMachine,
			}},
			wantOwner: OwnerChocolatey,
			wantScope: ScopeMachine,
		},
		{
			name: "chocolatey per-user scope on windows",
			exe: ExecutableEvidence{
				Platform:     "windows",
				Arch:         "amd64",
				ResolvedPath: `C:\Users\dev\AppData\Roaming\choco\lib\cercano\tools\cercano.exe`,
			},
			managers: []ManagerEvidence{{
				Manager:          OwnerChocolatey,
				Present:          true,
				PackageInstalled: true,
				PackageName:      "cercano",
				OwnedFiles:       []string{`C:\Users\dev\AppData\Roaming\choco\lib\cercano\tools\cercano.exe`},
				Scope:            ScopeUser,
			}},
			wantOwner: OwnerChocolatey,
			wantScope: ScopeUser,
		},
		{
			name: "apt machine scope on linux",
			exe: ExecutableEvidence{
				Platform:     "linux",
				Arch:         "amd64",
				ResolvedPath: "/usr/bin/cercano",
			},
			managers: []ManagerEvidence{{
				Manager:          OwnerAPT,
				Present:          true,
				PackageInstalled: true,
				PackageName:      "cercano",
				OwnedFiles:       []string{"/usr/bin/cercano"},
				Scope:            ScopeMachine,
			}},
			wantOwner: OwnerAPT,
			wantScope: ScopeMachine,
		},
		{
			name: "homebrew user scope on macos",
			exe: ExecutableEvidence{
				Platform:     "darwin",
				Arch:         "arm64",
				ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
			},
			managers:  []ManagerEvidence{brewEvidence(true, "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano")},
			wantOwner: OwnerHomebrew,
			wantScope: ScopeUser,
		},
		{
			name: "self-managed user scope on linux",
			exe: ExecutableEvidence{
				Platform:     "linux",
				Arch:         "amd64",
				ResolvedPath: "/home/dev/.local/share/cercano/versions/0.7.0/cercano",
				ResolvedRoot: "/home/dev/.local/share/cercano",
			},
			self:      SelfManagedEvidence{Enrolled: true, Scope: ScopeUser},
			wantOwner: OwnerSelfManaged,
			wantScope: ScopeUser,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.self.Enrolled {
				tc.self.Root = tc.exe.ResolvedRoot
				tc.self.Executable = tc.exe.ResolvedPath
			}
			got := Classify(tc.exe, tc.managers, tc.self)
			if got.Identity.Owner != tc.wantOwner {
				t.Fatalf("owner = %q, want %q", got.Identity.Owner, tc.wantOwner)
			}
			if got.Identity.Scope != tc.wantScope {
				t.Fatalf("scope = %q, want %q", got.Identity.Scope, tc.wantScope)
			}
			if got.Identity.Platform != tc.exe.Platform || got.Identity.Arch != tc.exe.Arch {
				t.Fatalf("identity platform/arch = %q/%q, want %q/%q",
					got.Identity.Platform, got.Identity.Arch, tc.exe.Platform, tc.exe.Arch)
			}
			if got.AutoEditable != (tc.wantOwner == OwnerSelfManaged && tc.wantScope == ScopeUser) {
				t.Fatalf("autoEditable = %v for owner=%q scope=%q", got.AutoEditable, tc.wantOwner, tc.wantScope)
			}
		})
	}
}

func TestReleaseAvailability_AnnouncedSeparateFromInstallable(t *testing.T) {
	// A GitHub announcement of 0.9.0 while the Homebrew tap still serves
	// 0.8.0: announced and installable stay distinct by source and
	// version, and neither implies the other.
	avail := ReleaseAvailability{
		AnnouncedVersion:   "0.9.0",
		AnnouncedSource:    "github",
		InstallableVersion: "0.8.0",
		InstallableSource:  "homebrew",
		Verified:           true,
	}
	if !avail.Announced() {
		t.Fatal("expected announcement")
	}
	if !avail.Installable() {
		t.Fatal("expected installable")
	}
	if avail.AnnouncedVersion == avail.InstallableVersion {
		t.Fatal("test setup error: versions should differ")
	}

	// Announcement without source verification: announced but not
	// installable.
	unverified := ReleaseAvailability{
		AnnouncedVersion: "0.9.0",
		AnnouncedSource:  "github",
	}
	if !unverified.Announced() {
		t.Fatal("expected announcement")
	}
	if unverified.Installable() {
		t.Fatal("an announcement alone must never be installable")
	}

	// Verification flag without a version or source is still not
	// installable.
	badVerified := ReleaseAvailability{Verified: true}
	if badVerified.Installable() {
		t.Fatal("verified flag without version/source must not be installable")
	}
}
