package installation

import (
	"reflect"
	"testing"
)

// These tests are pure: they classify caller-supplied evidence only. They
// never touch the filesystem, run package managers, or reach the network, and
// they never run against the developer's real installation.

func chocoEvidence(installed bool, scope Scope, ownedFiles ...string) ManagerEvidence {
	return ManagerEvidence{
		Manager:          OwnerChocolatey,
		Present:          true,
		PackageInstalled: installed,
		PackageName:      "cercano",
		OwnedFiles:       ownedFiles,
		Scope:            scope,
	}
}

func delegatedWindowsInstall() (ExecutableEvidence, []ManagerEvidence, DelegationEvidence) {
	exe := ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: `C:\Users\me\AppData\Local\Cercano\cercano.exe`,
		ResolvedRoot: `C:\Users\me\AppData\Local\Cercano`,
	}
	managers := []ManagerEvidence{chocoEvidence(true, ScopeUser, exe.ResolvedPath)}
	dl := DelegationEvidence{
		Owner:                OwnerChocolatey,
		Platform:             "windows",
		Arch:                 "amd64",
		Scope:                ScopeUser,
		Root:                 exe.ResolvedRoot,
		Executable:           exe.ResolvedPath,
		ReleaseChannel:       "stable",
		FeedID:               "feed-stable-main",
		Source:               DelegatedUpdateSource,
		ContractVersion:      "1",
		ContractVerified:     true,
		ConsentRecorded:      true,
		RegistrationRetained: true,
	}
	return exe, managers, dl
}

func TestClassifyWithDelegation_ZeroEvidenceMatchesClassify(t *testing.T) {
	exe, managers, _ := delegatedWindowsInstall()
	base := Classify(exe, managers, SelfManagedEvidence{})
	got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, DelegationEvidence{})
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("zero delegation changed classification:\nbase %+v\ngot  %+v", base, got)
	}
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("zero delegation set role %q", got.SelfUpdateRole)
	}
}

func TestClassifyWithDelegation_ChocolateyUserScopeHonored(t *testing.T) {
	exe, managers, dl := delegatedWindowsInstall()
	got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, dl)
	if got.Identity.Owner != OwnerChocolatey {
		t.Fatalf("owner = %q, want chocolatey retained as uninstall owner", got.Identity.Owner)
	}
	if got.Status != StatusActionable || got.Reason != ReasonIdentified {
		t.Fatalf("status/reason = %q/%q, want actionable/identified", got.Status, got.Reason)
	}
	if got.SelfUpdateRole != SelfUpdateRoleDelegate {
		t.Fatalf("self-update role = %q, want delegate", got.SelfUpdateRole)
	}
	if got.Delegation != dl {
		t.Fatalf("delegation evidence not preserved: %+v", got.Delegation)
	}
	if got.AutoEditable {
		t.Fatal("delegation must not make package-manager files auto-editable")
	}
}

func TestClassifyWithDelegation_NeverOverridesAmbiguity(t *testing.T) {
	exe, _, dl := delegatedWindowsInstall()
	// Chocolatey AND Homebrew both prove ownership: ambiguous conflict.
	brew := ManagerEvidence{
		Manager:          OwnerHomebrew,
		Present:          true,
		PackageInstalled: true,
		PackageName:      "cercano",
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            ScopeUser,
	}
	got := ClassifyWithDelegation(exe, []ManagerEvidence{chocoEvidence(true, ScopeUser, exe.ResolvedPath), brew}, SelfManagedEvidence{}, dl)
	if got.Status != StatusNonActionable || got.Reason != ReasonAmbiguousConflict {
		t.Fatalf("delegation overrode conflict: %+v", got)
	}
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("delegation granted role over ambiguous classification")
	}

	// A self-managed enrollment conflicting with the Chocolatey claim is
	// equally ambiguous and never overridden.
	self := SelfManagedEvidence{Enrolled: true, Scope: ScopeUser, Root: exe.ResolvedRoot, Executable: exe.ResolvedPath}
	got = ClassifyWithDelegation(exe, []ManagerEvidence{chocoEvidence(true, ScopeUser, exe.ResolvedPath)}, self, dl)
	if got.Status != StatusNonActionable || got.Reason != ReasonAmbiguousConflict || got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("delegation overrode enrollment conflict: %+v", got)
	}
}

func TestClassifyWithDelegation_RequiresActualChocolateyOwnership(t *testing.T) {
	exe, _, dl := delegatedWindowsInstall()
	// Manager present with an installed package but no owned-file proof:
	// unknown provenance that a delegation record must not override.
	got := ClassifyWithDelegation(exe, []ManagerEvidence{chocoEvidence(true, ScopeUser)}, SelfManagedEvidence{}, dl)
	if got.Status != StatusUnknown || got.Reason != ReasonManagerDoesNotOwnExecutable {
		t.Fatalf("delegation overrode missing ownership proof: %+v", got)
	}
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("delegation granted role without ownership proof")
	}

	// No managers at all: unknown.
	got = ClassifyWithDelegation(exe, nil, SelfManagedEvidence{}, dl)
	if got.Status != StatusUnknown || got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("delegation overrode unknown classification: %+v", got)
	}

	// Development build: never overridden.
	devExe := exe
	devExe.IsDevelopmentBuild = true
	got = ClassifyWithDelegation(devExe, []ManagerEvidence{chocoEvidence(true, ScopeUser, devExe.ResolvedPath)}, SelfManagedEvidence{}, dl)
	if got.Identity.Owner != OwnerDevelopment || got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("delegation overrode development classification: %+v", got)
	}
}

func TestClassifyWithDelegation_MachineScopeRefused(t *testing.T) {
	exe, _, dl := delegatedWindowsInstall()
	got := ClassifyWithDelegation(exe, []ManagerEvidence{chocoEvidence(true, ScopeMachine, exe.ResolvedPath)}, SelfManagedEvidence{}, dl)
	if got.Identity.Scope != ScopeMachine || got.Status != StatusActionable {
		t.Fatalf("machine classification changed: %+v", got)
	}
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("machine-wide installation received delegation")
	}
}

func TestClassifyWithDelegation_NonWindowsRefused(t *testing.T) {
	exe, _, dl := delegatedWindowsInstall()
	// A Chocolatey claim observed on a non-Windows platform cannot carry
	// the Windows-only delegation contract.
	exe.Platform = "linux"
	exe.ResolvedPath = "/home/me/cercano/cercano"
	exe.ResolvedRoot = "/home/me/cercano"
	dl.Root = exe.ResolvedRoot
	dl.Executable = exe.ResolvedPath
	got := ClassifyWithDelegation(exe, []ManagerEvidence{chocoEvidence(true, ScopeUser, exe.ResolvedPath)}, SelfManagedEvidence{}, dl)
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("non-windows installation received delegation: %+v", got)
	}
	// Wrong delegation platform for a real Windows install is equally refused.
	exe, managers, _ := delegatedWindowsInstall()
	dl = DelegationEvidence{Owner: OwnerChocolatey, Platform: "darwin", Arch: "amd64", Scope: ScopeUser,
		Root: exe.ResolvedRoot, Executable: exe.ResolvedPath, ReleaseChannel: "stable", FeedID: "feed-stable-main", Source: DelegatedUpdateSource,
		ContractVersion: "1", ContractVerified: true, ConsentRecorded: true, RegistrationRetained: true}
	got = ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, dl)
	if got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("cross-platform delegation honored: %+v", got)
	}
}

func TestClassifyWithDelegation_UnboundRecordRefused(t *testing.T) {
	exe, managers, dl := delegatedWindowsInstall()
	// Stale root: the installation moved since enrollment.
	staleRoot := dl
	staleRoot.Root = `C:\Users\me\AppData\Local\CercanoOld`
	if got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, staleRoot); got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("stale-root delegation honored")
	}
	// Stale executable binding.
	staleExe := dl
	staleExe.Executable = `C:\Users\me\AppData\Local\Cercano\cercano-old.exe`
	if got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, staleExe); got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("stale-executable delegation honored")
	}
	// Arch mismatch.
	otherArch := dl
	otherArch.Arch = "arm64"
	if got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, otherArch); got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("arch-mismatch delegation honored")
	}
	// Relative (non-canonical) root never binds.
	relative := dl
	relative.Root = `C:relative\path`
	if got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, relative); got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatal("non-canonical delegation honored")
	}
}

func TestClassifyWithDelegation_IncompleteEvidenceRefused(t *testing.T) {
	exe, managers, dl := delegatedWindowsInstall()
	mutations := map[string]func(*DelegationEvidence){
		"owner-not-chocolatey": func(d *DelegationEvidence) { d.Owner = OwnerHomebrew },
		"machine-scope":        func(d *DelegationEvidence) { d.Scope = ScopeMachine },
		"unknown-scope":        func(d *DelegationEvidence) { d.Scope = ScopeUnknown },
		"missing-channel":      func(d *DelegationEvidence) { d.ReleaseChannel = "" },
		"missing-feed-id":      func(d *DelegationEvidence) { d.FeedID = "" },
		"missing-arch":         func(d *DelegationEvidence) { d.Arch = "" },
		"wrong-source":         func(d *DelegationEvidence) { d.Source = "chocolatey" },
		"missing-contract":     func(d *DelegationEvidence) { d.ContractVersion = "" },
		"contract-unverified":  func(d *DelegationEvidence) { d.ContractVerified = false },
		"consent-not-recorded": func(d *DelegationEvidence) { d.ConsentRecorded = false },
		"registration-dropped": func(d *DelegationEvidence) { d.RegistrationRetained = false },
	}
	for name, mutate := range mutations {
		mutated := dl
		mutate(&mutated)
		got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, mutated)
		if got.SelfUpdateRole != SelfUpdateRoleNone {
			t.Fatalf("%s: delegation honored: %+v", name, got)
		}
		if got.Status != StatusActionable || got.Identity.Owner != OwnerChocolatey {
			t.Fatalf("%s: refusal changed the chocolatey classification: %+v", name, got)
		}
	}
}

func TestClassifyWithDelegation_NonChocolateyOwnerRefused(t *testing.T) {
	// A delegation record naming Chocolatey can never transfer Homebrew's
	// ownership, and a Homebrew installation never receives the
	// Chocolatey-only delegation contract.
	exe := ExecutableEvidence{
		Platform:     "darwin",
		Arch:         "arm64",
		ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		ResolvedRoot: "/opt/homebrew/Cellar/cercano/0.7.0",
	}
	dl := DelegationEvidence{
		Owner:                OwnerChocolatey,
		Platform:             "darwin",
		Arch:                 "arm64",
		Scope:                ScopeUser,
		Root:                 exe.ResolvedRoot,
		Executable:           exe.ResolvedPath,
		ReleaseChannel:       "stable",
		FeedID:               "feed-stable-main",
		Source:               DelegatedUpdateSource,
		ContractVersion:      "1",
		ContractVerified:     true,
		ConsentRecorded:      true,
		RegistrationRetained: true,
	}
	got := ClassifyWithDelegation(exe, []ManagerEvidence{brewEvidence(true, exe.ResolvedPath)}, SelfManagedEvidence{}, dl)
	if got.Identity.Owner != OwnerHomebrew || got.SelfUpdateRole != SelfUpdateRoleNone {
		t.Fatalf("delegation hijacked homebrew installation: %+v", got)
	}
}
