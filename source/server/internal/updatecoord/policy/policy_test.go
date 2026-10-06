package policy

import (
	"testing"

	"cercano/source/server/internal/updatecoord/installation"
)

// These tests are pure: they exercise the in-memory policy model only. They
// never touch the filesystem, read configuration, start processes, run
// package managers, or reach the network.

// delegatedChocoFixture returns a fully consistent delegation scenario: a
// currently observed, unambiguous, user-scope Chocolatey-owned Windows
// installation; a valid, consented, identity-bound record; an explicit
// administrator allowance; and a fresh, verified, supported
// cooperating-installer probe.
func delegatedChocoFixture() (DelegationRecord, CurrentInstallation, ManagedRestrictions, InstallerContractEvidence) {
	exe := installation.ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: `C:\Users\me\AppData\Local\Cercano\cercano.exe`,
		ResolvedRoot: `C:\Users\me\AppData\Local\Cercano`,
	}
	choco := installation.ManagerEvidence{
		Manager:          installation.OwnerChocolatey,
		Present:          true,
		PackageInstalled: true,
		PackageName:      "cercano",
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            installation.ScopeUser,
	}
	class := installation.Classify(exe, []installation.ManagerEvidence{choco}, installation.SelfManagedEvidence{})
	rec := DelegationRecord{
		SchemaVersion:        DelegationSchemaVersion,
		InstallID:            "install-a",
		CanonicalRoot:        exe.ResolvedRoot,
		Owner:                string(installation.OwnerChocolatey),
		Platform:             "windows",
		Arch:                 "amd64",
		Scope:                string(installation.ScopeUser),
		ReleaseChannel:       "stable",
		Source:               "tuf",
		FeedID:               "feed-stable-main",
		ConsentRecorded:      true,
		RegistrationRetained: true,
		ContractVersion:      "1",
	}
	// The current observation is DELIBERATELY BOUND: it independently
	// reports the release channel and the concrete feed identity the
	// record must corroborate ("tuf" is the source kind, not a feed).
	cur := CurrentInstallation{
		InstallID:      "install-a",
		Class:          class,
		ReleaseChannel: "stable",
		FeedID:         "feed-stable-main",
	}
	// The administrator-policy probe explicitly ran and read an allowance.
	restrictions := ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedAllowed}
	installer := InstallerContractEvidence{
		SupportedVersions: []string{"1", "2"},
		ProbeVerified:     true,
		ObservedVersion:   "1",
	}
	return rec, cur, restrictions, installer
}

func TestEvaluateDelegation_FullyCorroboratedRecordAllowed(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	decision := EvaluateDelegation(rec, cur, restrictions, installer)
	if !decision.Allowed {
		t.Fatalf("fully corroborated delegation refused: %q", decision.Refusal)
	}
	if decision.Refusal != RefusalNone {
		t.Fatalf("allowed decision carried refusal %q", decision.Refusal)
	}
	e := decision.Evidence
	if e.Owner != installation.OwnerChocolatey || e.Platform != "windows" || e.Scope != installation.ScopeUser {
		t.Fatalf("evidence bindings wrong: %+v", e)
	}
	if e.Root != cur.Class.Identity.Root || e.Executable != cur.Class.Identity.Executable || e.Arch != cur.Class.Identity.Arch {
		t.Fatalf("evidence not bound to current identity: %+v vs %+v", e, cur.Class.Identity)
	}
	if e.Source != installation.DelegatedUpdateSource || e.ReleaseChannel != "stable" || e.ContractVersion != "1" {
		t.Fatalf("evidence contract fields wrong: %+v", e)
	}
	if e.FeedID != cur.FeedID {
		t.Fatalf("evidence feed = %q, want the observed feed %q", e.FeedID, cur.FeedID)
	}
	// The corroborated evidence must survive the classifier's independent
	// re-verification against the observed executable and manager evidence.
	exe := installation.ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: cur.Class.Identity.Executable,
		ResolvedRoot: cur.Class.Identity.Root,
	}
	classified := installation.ClassifyWithDelegation(exe, []installation.ManagerEvidence{{
		Manager:          installation.OwnerChocolatey,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            installation.ScopeUser,
	}}, installation.SelfManagedEvidence{}, decision.Evidence)
	if classified.SelfUpdateRole != installation.SelfUpdateRoleDelegate {
		t.Fatalf("corroborated evidence not honored by classifier: %+v", classified)
	}
}

func TestEvaluateDelegation_ManagedRestrictionsFailClosed(t *testing.T) {
	rec, cur, _, installer := delegatedChocoFixture()
	cases := []struct {
		name         string
		restrictions ManagedRestrictions
		wantRefusal  DelegationRefusal
	}{
		{"prohibited", ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedProhibited}, RefusalManagedProhibited},
		{"unknown", ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedUnknown}, RefusalManagedUnknown},
		{"missing-stance", ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedUnset}, RefusalManagedUnknown},
		{"unrecognized-stance", ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedValue("maybe")}, RefusalManagedUnknown},
		{"allowed", ManagedRestrictions{Observed: true, Managed: true, SelfUpdate: ManagedAllowed}, RefusalNone},
		{"unmanaged-ignores-stance", ManagedRestrictions{Observed: true, Managed: false, SelfUpdate: ManagedUnset}, RefusalNone},
		// The zero/unobserved value must NOT count as a known-unmanaged
		// machine: fail closed instead.
		{"zero-value-unobserved", ManagedRestrictions{}, RefusalManagedUnknown},
		{"zero-value-unobserved-explicit", ManagedRestrictions{Observed: false, Managed: false}, RefusalManagedUnknown},
		// Contradictory prohibited/unknown stances deny even when the
		// probe reports the machine unmanaged.
		{"unmanaged-prohibited", ManagedRestrictions{Observed: true, Managed: false, SelfUpdate: ManagedProhibited}, RefusalManagedProhibited},
		{"unmanaged-unknown", ManagedRestrictions{Observed: true, Managed: false, SelfUpdate: ManagedUnknown}, RefusalManagedUnknown},
	}
	for _, tc := range cases {
		decision := EvaluateDelegation(rec, cur, tc.restrictions, installer)
		if tc.wantRefusal == RefusalNone {
			if !decision.Allowed {
				t.Fatalf("%s: refused with %q, want allowed", tc.name, decision.Refusal)
			}
			continue
		}
		if decision.Allowed {
			t.Fatalf("%s: allowed despite %q stance", tc.name, tc.restrictions.SelfUpdate)
		}
		if decision.Refusal != tc.wantRefusal {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.wantRefusal)
		}
		if !decision.Evidence.IsZero() {
			t.Fatalf("%s: refused decision carried evidence: %+v", tc.name, decision.Evidence)
		}
	}
}

func TestEvaluateDelegation_RecordFlagsFailClosed(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	cases := []struct {
		name        string
		mutate      func(*DelegationRecord)
		wantRefusal DelegationRefusal
	}{
		{"revoked", func(r *DelegationRecord) { r.Revoked = true }, RefusalRevoked},
		{"consent-not-recorded", func(r *DelegationRecord) { r.ConsentRecorded = false }, RefusalConsentNotRecorded},
		{"registration-dropped", func(r *DelegationRecord) { r.RegistrationRetained = false }, RefusalRegistrationNotRetained},
		{"record-invalid-empty-install-id", func(r *DelegationRecord) { r.InstallID = "" }, RefusalRecordInvalid},
		{"record-invalid-non-tuf-source", func(r *DelegationRecord) { r.Source = "github" }, RefusalRecordInvalid},
		{"record-invalid-empty-root", func(r *DelegationRecord) { r.CanonicalRoot = "" }, RefusalRecordInvalid},
		{"record-invalid-unknown-owner", func(r *DelegationRecord) { r.Owner = "winget" }, RefusalRecordInvalid},
		{"record-invalid-unknown-scope", func(r *DelegationRecord) { r.Scope = "global" }, RefusalRecordInvalid},
		{"record-invalid-empty-contract", func(r *DelegationRecord) { r.ContractVersion = "" }, RefusalRecordInvalid},
		{"record-invalid-empty-channel", func(r *DelegationRecord) { r.ReleaseChannel = "" }, RefusalRecordInvalid},
	}
	for _, tc := range cases {
		mutated := rec
		tc.mutate(&mutated)
		decision := EvaluateDelegation(mutated, cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: allowed", tc.name)
		}
		if decision.Refusal != tc.wantRefusal {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.wantRefusal)
		}
	}
}

func TestEvaluateDelegation_ClassificationNeverOverridden(t *testing.T) {
	rec, _, restrictions, installer := delegatedChocoFixture()

	// Ambiguous: Chocolatey and Homebrew both prove ownership of the same
	// resolved executable (a conflict the record must not resolve).
	exe := installation.ExecutableEvidence{
		Platform:     "windows",
		Arch:         "amd64",
		ResolvedPath: `C:\Users\me\AppData\Local\Cercano\cercano.exe`,
		ResolvedRoot: `C:\Users\me\AppData\Local\Cercano`,
	}
	choco := installation.ManagerEvidence{
		Manager:          installation.OwnerChocolatey,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            installation.ScopeUser,
	}
	brew := installation.ManagerEvidence{
		Manager:          installation.OwnerHomebrew,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            installation.ScopeUser,
	}
	conflicting := installation.Classify(exe, []installation.ManagerEvidence{choco, brew}, installation.SelfManagedEvidence{})

	// Unknown: manager present with installed package but no owned-file
	// proof.
	unknown := installation.Classify(exe, []installation.ManagerEvidence{{
		Manager:          installation.OwnerChocolatey,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       nil,
		Scope:            installation.ScopeUser,
	}}, installation.SelfManagedEvidence{})

	// Machine-wide Chocolatey ownership.
	machine := installation.Classify(exe, []installation.ManagerEvidence{{
		Manager:          installation.OwnerChocolatey,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       []string{exe.ResolvedPath},
		Scope:            installation.ScopeMachine,
	}}, installation.SelfManagedEvidence{})

	// Homebrew ownership on macOS.
	darwinExe := installation.ExecutableEvidence{
		Platform:     "darwin",
		Arch:         "arm64",
		ResolvedPath: "/opt/homebrew/Cellar/cercano/0.7.0/bin/cercano",
		ResolvedRoot: "/opt/homebrew/Cellar/cercano/0.7.0",
	}
	homebrew := installation.Classify(darwinExe, []installation.ManagerEvidence{{
		Manager:          installation.OwnerHomebrew,
		Present:          true,
		PackageInstalled: true,
		OwnedFiles:       []string{darwinExe.ResolvedPath},
		Scope:            installation.ScopeUser,
	}}, installation.SelfManagedEvidence{})

	// Development build.
	devExe := exe
	devExe.IsDevelopmentBuild = true
	development := installation.Classify(devExe, []installation.ManagerEvidence{choco}, installation.SelfManagedEvidence{})

	cases := []struct {
		name        string
		class       installation.Classification
		wantRefusal DelegationRefusal
	}{
		{"ambiguous-conflict", conflicting, RefusalClassificationNotActionable},
		{"no-ownership-proof", unknown, RefusalClassificationNotActionable},
		{"development", development, RefusalClassificationNotActionable},
		{"machine-scope", machine, RefusalMachineScope},
		{"homebrew-owner", homebrew, RefusalClassificationNotChocolatey},
	}
	for _, tc := range cases {
		cur := CurrentInstallation{InstallID: rec.InstallID, Class: tc.class}
		decision := EvaluateDelegation(rec, cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: record overrode classifier outcome %+v", tc.name, tc.class)
		}
		if decision.Refusal != tc.wantRefusal {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.wantRefusal)
		}
	}
}

func TestEvaluateDelegation_IdentityBindingFailClosed(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	cases := []struct {
		name string
		cur  CurrentInstallation
	}{
		{"different-install-id", CurrentInstallation{InstallID: "install-b", Class: cur.Class, ReleaseChannel: "stable", FeedID: "feed-stable-main"}},
		{"missing-install-id", CurrentInstallation{InstallID: "", Class: cur.Class, ReleaseChannel: "stable", FeedID: "feed-stable-main"}},
		{"stale-root", CurrentInstallation{InstallID: cur.InstallID, Class: installation.Classification{
			Identity: installation.Identity{
				Owner:      installation.OwnerChocolatey,
				Scope:      installation.ScopeUser,
				Platform:   "windows",
				Arch:       "amd64",
				Source:     "chocolatey",
				Executable: cur.Class.Identity.Executable,
				Root:       `C:\Users\me\AppData\Local\CercanoOld`,
			},
			Status: installation.StatusActionable,
			Reason: installation.ReasonIdentified,
		}, ReleaseChannel: "stable", FeedID: "feed-stable-main"}},
		{"arch-mismatch", CurrentInstallation{InstallID: cur.InstallID, Class: installation.Classification{
			Identity: installation.Identity{
				Owner:      installation.OwnerChocolatey,
				Scope:      installation.ScopeUser,
				Platform:   "windows",
				Arch:       "arm64",
				Source:     "chocolatey",
				Executable: cur.Class.Identity.Executable,
				Root:       cur.Class.Identity.Root,
			},
			Status: installation.StatusActionable,
			Reason: installation.ReasonIdentified,
		}}},
	}
	for _, tc := range cases {
		decision := EvaluateDelegation(rec, tc.cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: unbound record allowed", tc.name)
		}
		if decision.Refusal != RefusalUnboundIdentity {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, RefusalUnboundIdentity)
		}
	}
	// Record-side mismatches are equally unbound.
	mutations := map[string]func(*DelegationRecord){
		"record-other-install":  func(r *DelegationRecord) { r.InstallID = "install-b" },
		"record-other-root":     func(r *DelegationRecord) { r.CanonicalRoot = `C:\Users\me\AppData\Local\CercanoOld` },
		"record-other-arch":     func(r *DelegationRecord) { r.Arch = "arm64" },
		"record-other-owner":    func(r *DelegationRecord) { r.Owner = string(installation.OwnerHomebrew) },
		"record-machine-scope":  func(r *DelegationRecord) { r.Scope = string(installation.ScopeMachine) },
		"record-other-platform": func(r *DelegationRecord) { r.Platform = "linux" },
	}
	for name, mutate := range mutations {
		mutated := rec
		mutate(&mutated)
		decision := EvaluateDelegation(mutated, cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: unbound record allowed", name)
		}
	}
}

func TestEvaluateDelegation_ContractEvidenceFailClosed(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	cases := []struct {
		name        string
		mutate      func(*InstallerContractEvidence)
		wantRefusal DelegationRefusal
	}{
		{"unsupported-version", func(i *InstallerContractEvidence) { i.SupportedVersions = []string{"2", "3"} }, RefusalUnsupportedContract},
		{"no-supported-versions", func(i *InstallerContractEvidence) { i.SupportedVersions = nil }, RefusalUnsupportedContract},
		{"probe-unverified", func(i *InstallerContractEvidence) { i.ProbeVerified = false }, RefusalContractUnverified},
		{"probe-empty-version", func(i *InstallerContractEvidence) { i.ObservedVersion = "" }, RefusalContractUnverified},
		{"probe-other-version", func(i *InstallerContractEvidence) { i.ObservedVersion = "2" }, RefusalContractUnverified},
	}
	for _, tc := range cases {
		mutated := installer
		tc.mutate(&mutated)
		decision := EvaluateDelegation(rec, cur, restrictions, mutated)
		if decision.Allowed {
			t.Fatalf("%s: allowed", tc.name)
		}
		if decision.Refusal != tc.wantRefusal {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.wantRefusal)
		}
	}
}

func TestEvaluateDelegation_StorageProbeBooleansAreNotAuthority(t *testing.T) {
	// A record claiming consent and a probe claiming verification are only
	// corroborations; withholding any distinct current evidence must
	// still refuse. This test pins the fail-closed shape of the API: the
	// record plus a single boolean is never sufficient input.
	rec, cur, _, _ := delegatedChocoFixture()
	// Nothing but the record: no fresh probe, no supported versions. The
	// administrator policy is explicitly observed-and-unmanaged (a real
	// probe result), so the refusal must come from the missing contract
	// evidence — the record alone is never sufficient authority.
	decision := EvaluateDelegation(rec, cur, ManagedRestrictions{Observed: true, Managed: false}, InstallerContractEvidence{})
	if decision.Allowed {
		t.Fatal("record alone with no contract evidence allowed delegation")
	}
}

func TestReconcilePackageVersion(t *testing.T) {
	cases := []struct {
		name       string
		installed  string
		packageRec string
		want       ReconciliationStatus
		wantDrift  bool
		wantAvoid  bool
	}{
		{"equal", "0.9.0", "0.9.0", ReconcileAligned, false, false},
		// The normal delegated self-update case: the application moved
		// ahead of the Chocolatey record. Drift exists and a replacement
		// driven by the recorded (older) version would install older
		// content: the installer must avoid it.
		{"record-lags-installed", "0.9.0", "0.8.0", ReconcileRecordLags, true, true},
		// The other direction: the package record claims a version the
		// application does not actually have. Drift, refused until
		// explicitly reconciled through the manager's own operations.
		{"record-ahead-refused", "0.9.0", "0.10.0", ReconcileRecordAheadMismatch, true, false},
		{"missing-installed", "", "0.9.0", ReconcileInvalid, false, false},
		{"missing-record", "0.9.0", "", ReconcileInvalid, false, false},
		{"padded-installed", " 0.9.0", "0.9.0", ReconcileInvalid, false, false},
		{"padded-record", "0.9.0", "0.9.0 ", ReconcileInvalid, false, false},
		{"junk-installed", "latest", "0.9.0", ReconcileInvalid, false, false},
		{"overflow-component", "99999999999999999999.0.0", "0.9.0", ReconcileInvalid, false, false},
		{"truncated-two-components", "0.9", "0.9.0", ReconcileInvalid, false, false},
		{"prerelease-installed", "0.9.0-beta.1", "0.9.0", ReconcileInvalid, false, false},
		{"prerelease-record", "0.9.0", "0.9.0-rc.1", ReconcileInvalid, false, false},
		{"build-metadata-record", "0.9.0", "0.9.0+build.7", ReconcileInvalid, false, false},
	}
	for _, tc := range cases {
		got := ReconcilePackageVersion(tc.installed, tc.packageRec)
		if got.Status != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, got.Status, tc.want)
		}
		if got.Drift != tc.wantDrift {
			t.Errorf("%s: drift = %v, want %v", tc.name, got.Drift, tc.wantDrift)
		}
		if got.InstallerMustAvoidOlderReplacement != tc.wantAvoid {
			t.Errorf("%s: installer-must-avoid-older-replacement = %v, want %v", tc.name, got.InstallerMustAvoidOlderReplacement, tc.wantAvoid)
		}
	}
	// The optional "v" prefix is part of the currently published shape.
	if got := ReconcilePackageVersion("v0.9.0", "0.9.0"); got.Status != ReconcileAligned {
		t.Errorf("v-prefix installed: status = %q, want %q", got.Status, ReconcileAligned)
	}
}
