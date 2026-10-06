package policy

import (
	"testing"

	"cercano/source/server/internal/updatecoord/installation"
)

// Regression tests from the bounded review for the release-channel and
// feed-identity binding. "tuf" names the SOURCE KIND, not a specific feed:
// the record and the distinct current observation must both name the same
// stable opaque feed identity, and the record's release channel must match
// the currently observed one. The explicit supported-contract probe
// (InstallerContractEvidence) is unchanged.

func TestEvaluateDelegation_BoundToCurrentReleaseChannelAndFeed(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	// The fixture is deliberately bound: cur observes channel "stable" and
	// feed "feed-stable-main", and the record names the same feed.
	if cur.ReleaseChannel != rec.ReleaseChannel {
		t.Fatalf("fixture not deliberately bound: cur channel %q, record channel %q", cur.ReleaseChannel, rec.ReleaseChannel)
	}
	if cur.FeedID == "" || rec.FeedID != cur.FeedID {
		t.Fatalf("fixture not deliberately bound: cur feed %q, record feed %q", cur.FeedID, rec.FeedID)
	}

	// Sanity: the deliberately bound fixture is allowed.
	if decision := EvaluateDelegation(rec, cur, restrictions, installer); !decision.Allowed {
		t.Fatalf("deliberately bound fixture refused: %q", decision.Refusal)
	}

	// Current-side mismatches are unbound: the record is bound to what is
	// observed NOW, not to itself.
	currentCases := []struct {
		name string
		cur  CurrentInstallation
	}{
		{"other-observed-channel", boundCurrent(cur, "beta", cur.FeedID)},
		{"missing-observed-channel", boundCurrent(cur, "", cur.FeedID)},
		{"other-observed-feed", boundCurrent(cur, cur.ReleaseChannel, "feed-other")},
		{"missing-observed-feed", boundCurrent(cur, cur.ReleaseChannel, "")},
	}
	for _, tc := range currentCases {
		decision := EvaluateDelegation(rec, tc.cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: record not bound to current observation was allowed", tc.name)
		}
		if decision.Refusal != RefusalUnboundIdentity {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, RefusalUnboundIdentity)
		}
	}

	// Record-side mismatches are equally unbound, and a record with no
	// feed identity at all is structurally invalid.
	recordCases := []struct {
		name   string
		mutate func(*DelegationRecord)
		want   DelegationRefusal
	}{
		{"other-record-feed", func(r *DelegationRecord) { r.FeedID = "feed-other" }, RefusalUnboundIdentity},
		{"other-record-channel", func(r *DelegationRecord) { r.ReleaseChannel = "beta" }, RefusalUnboundIdentity},
		{"missing-record-feed", func(r *DelegationRecord) { r.FeedID = "" }, RefusalRecordInvalid},
	}
	for _, tc := range recordCases {
		mutated := rec
		tc.mutate(&mutated)
		decision := EvaluateDelegation(mutated, cur, restrictions, installer)
		if decision.Allowed {
			t.Fatalf("%s: unbound record allowed", tc.name)
		}
		if decision.Refusal != tc.want {
			t.Fatalf("%s: refusal = %q, want %q", tc.name, decision.Refusal, tc.want)
		}
	}
}

func boundCurrent(cur CurrentInstallation, channel, feed string) CurrentInstallation {
	out := cur
	out.ReleaseChannel = channel
	out.FeedID = feed
	return out
}

func TestEvaluateDelegation_EvidenceCarriesFeedIdentity(t *testing.T) {
	rec, cur, restrictions, installer := delegatedChocoFixture()
	decision := EvaluateDelegation(rec, cur, restrictions, installer)
	if !decision.Allowed {
		t.Fatalf("bound fixture refused: %q", decision.Refusal)
	}
	if decision.Evidence.FeedID != cur.FeedID {
		t.Fatalf("evidence feed = %q, want the observed feed %q", decision.Evidence.FeedID, cur.FeedID)
	}
	// The corroborated evidence must survive the classifier's independent
	// re-verification (which now requires the feed identity structurally).
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
		t.Fatalf("feed-bound evidence not honored by classifier: %+v", classified)
	}
}

func TestEvaluateDelegation_ObservedFlagDistinguishesProbeResult(t *testing.T) {
	// Explicit tri-state: Observed=true + Managed=false is the only value
	// that means "the probe ran and read no management". Unobserved values
	// never count as known-unmanaged.
	rec, cur, _, installer := delegatedChocoFixture()
	if decision := EvaluateDelegation(rec, cur, ManagedRestrictions{}, installer); decision.Allowed {
		t.Fatal("zero-value restrictions counted as known-unmanaged")
	}
	observed := ManagedRestrictions{Observed: true, Managed: false, SelfUpdate: ManagedUnset}
	if decision := EvaluateDelegation(rec, cur, observed, installer); !decision.Allowed {
		t.Fatalf("observed unmanaged machine refused: %q", decision.Refusal)
	}
	// Even with Observed set, a contradictory prohibited stance denies.
	prohibited := observed
	prohibited.SelfUpdate = ManagedProhibited
	if decision := EvaluateDelegation(rec, cur, prohibited, installer); decision.Allowed || decision.Refusal != RefusalManagedProhibited {
		t.Fatalf("prohibited stance on unmanaged machine ignored: %+v", decision)
	}
}

func TestReconcilePackageVersion_ReportsDriftAndOlderReplacementAvoidance(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		record    string
		want      ReconciliationStatus
		wantDrift bool
		wantAvoid bool
	}{
		// No drift: the record already matches reality.
		{"aligned", "1.2.3", "1.2.3", ReconcileAligned, false, false},
		{"aligned-v-prefix", "v1.2.3", "1.2.3", ReconcileAligned, false, false},
		// Drift: the record lags the actually installed application. A
		// package-manager replacement driven by the record would install
		// an OLDER version over newer content: the installer must avoid
		// the older replacement. The function does NOT claim the app may
		// set the Chocolatey record to the installed version.
		{"record-lags", "1.3.0", "1.2.3", ReconcileRecordLags, true, true},
		// Drift in the other direction: the record claims a version the
		// application does not have. Nothing here downgrades; it is an
		// ahead mismatch refused until explicitly reconciled.
		{"record-ahead", "1.2.3", "1.3.0", ReconcileRecordAheadMismatch, true, false},
		{"invalid", "junk", "1.2.3", ReconcileInvalid, false, false},
	}
	for _, tc := range cases {
		got := ReconcilePackageVersion(tc.installed, tc.record)
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
}
