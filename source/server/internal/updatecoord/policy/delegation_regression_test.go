package policy

import (
	"testing"
)

// Regression tests from the bounded review. They are pure like the rest of
// the package: no filesystem, no configuration, no package manager, no
// network, no production keys.

// unknownSchemaRecord returns an otherwise fully corroborated record whose
// schema version is an unknown nonzero value delivered as a DIRECT struct.
// DecodeDelegationRecord already refuses such a record; EvaluateDelegation
// must refuse it too instead of trusting in-process callers.
func unknownSchemaRecord() (DelegationRecord, CurrentInstallation, ManagedRestrictions, InstallerContractEvidence) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	rec.SchemaVersion = DelegationSchemaVersion + 7
	return rec, cur, restrictions, installer
}

func TestEvaluateDelegation_UnknownSchemaVersionDirectStructRefused(t *testing.T) {
	rec, cur, restrictions, installer := unknownSchemaRecord()
	decision := EvaluateDelegation(rec, cur, restrictions, installer)
	if decision.Allowed {
		t.Fatal("unknown nonzero schema version delivered as a direct struct was allowed")
	}
	if decision.Evidence.IsZero() {
		return // refused: nothing more to check
	}
	t.Fatalf("refused decision still carried evidence: %+v", decision.Evidence)
}

func TestDelegationRecord_EncodeRefusesToCoerceUnknownSchemaVersion(t *testing.T) {
	// Zero means "unset/new": stamping the current version is deliberate.
	unset := validDelegationRecord()
	unset.SchemaVersion = 0
	if _, err := unset.Encode(); err != nil {
		t.Fatalf("Encode refused an unset (zero) schema version: %v", err)
	}
	current := validDelegationRecord()
	current.SchemaVersion = DelegationSchemaVersion
	if _, err := current.Encode(); err != nil {
		t.Fatalf("Encode refused the current schema version: %v", err)
	}
	unknown := validDelegationRecord()
	unknown.SchemaVersion = 99
	if _, err := unknown.Encode(); err == nil {
		t.Fatal("Encode silently coerced an unknown nonzero schema version into the current one")
	}
}

func TestEvaluateDelegation_UnobservedManagedPolicyIsNotKnownUnmanaged(t *testing.T) {
	// The zero/unobserved administrator-policy value must never be read as
	// "this machine is known to be unmanaged": only an explicit probe
	// result may establish that, and contradictory prohibited/unknown
	// stances deny even when Managed is false.
	rec, cur, _, installer := delegatedChocoFixture()
	cases := []struct {
		name         string
		restrictions ManagedRestrictions
		want         DelegationRefusal
	}{
		{"zero-value-probe-never-ran", ManagedRestrictions{}, RefusalManagedUnknown},
		{"unobserved-with-stray-allowance", ManagedRestrictions{Managed: false, SelfUpdate: ManagedAllowed}, RefusalManagedUnknown},
		{"unmanaged-but-prohibited", ManagedRestrictions{Managed: false, SelfUpdate: ManagedProhibited}, RefusalManagedProhibited},
		{"unmanaged-but-unknown-stance", ManagedRestrictions{Managed: false, SelfUpdate: ManagedUnknown}, RefusalManagedUnknown},
	}
	for _, tc := range cases {
		decision := EvaluateDelegation(rec, cur, tc.restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: unobserved/contradictory policy allowed delegation", tc.name)
		}
		if decision.Refusal != tc.want {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.want)
		}
	}
}

func TestReconcilePackageVersion_StrictStableVersionValidation(t *testing.T) {
	// CompareVersions is deliberately lax; the reconciliation policy must
	// not let junk, truncated, prerelease, or overflowing strings reach it
	// and be silently compared as zeros. Prereleases are rejected until
	// the release pipeline explicitly supports them.
	cases := []struct {
		name              string
		installed, record string
	}{
		{"junk-installed", "banana", "0.0.0"},
		{"junk-record", "0.0.0", "banana"},
		{"two-component-installed", "0.9", "0.9.0"},
		{"four-component-record", "0.9.0", "0.9.0.1"},
		{"prerelease-installed", "0.9.0-beta", "0.9.0"},
		{"prerelease-record", "0.9.0", "0.9.0-rc.1"},
		{"build-metadata-installed", "0.9.0+build.1", "0.9.0"},
		{"overflow-installed", "99999999999999999999.0.0", "0.0.0"},
		{"overflow-record", "0.0.0", "18446744073709551616.0.0"},
		{"negative-component", "-1.0.0", "0.0.0"},
		{"hex-component", "0x1.0.0", "0.0.0"},
		{"leading-plus", "+1.0.0", "0.0.0"},
	}
	for _, tc := range cases {
		got := ReconcilePackageVersion(tc.installed, tc.record)
		if got.Status != ReconcileInvalid {
			t.Errorf("%s: (%q, %q): status = %q, want invalid", tc.name, tc.installed, tc.record, got.Status)
		}
	}
	// The exact shape the release pipeline publishes today stays valid,
	// with an optional "v" prefix.
	if got := ReconcilePackageVersion("v1.2.3", "1.2.3"); got.Status != ReconcileAligned {
		t.Errorf("optional v prefix rejected: %q", got.Status)
	}
	if got := ReconcilePackageVersion("1.2.3", "1.2.3"); got.Status != ReconcileAligned || got.Drift || got.InstallerMustAvoidOlderReplacement {
		t.Errorf("aligned case reported drift: %+v", got)
	}
	// Record lags the installed app: drift, and the installer must avoid
	// replacing newer content with the older recorded version.
	if got := ReconcilePackageVersion("1.2.3", "1.1.0"); got.Status != ReconcileRecordLags || !got.Drift || !got.InstallerMustAvoidOlderReplacement {
		t.Errorf("record-lags case wrong: %+v", got)
	}
	// Record ahead of the actually installed app: drift, no
	// avoid-older-replacement, and no claim that the app may set the
	// Chocolatey record to an arbitrary version.
	if got := ReconcilePackageVersion("1.1.0", "1.2.3"); got.Status != ReconcileRecordAheadMismatch || !got.Drift || got.InstallerMustAvoidOlderReplacement {
		t.Errorf("record-ahead-mismatch case wrong: %+v", got)
	}
}
