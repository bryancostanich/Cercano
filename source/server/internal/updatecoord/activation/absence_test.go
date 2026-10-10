package activation

import (
	"cercano/source/server/internal/updatecoord/state"
	"testing"
)

func TestReconcileAbsenceRestoration(t *testing.T) {
	target := selectionFor(testInstall, testTarget, "staged-"+testTarget, 1, targetDigest)
	for _, tc := range []struct {
		cp   state.JournalCheckpoint
		obs  Observed
		want Action
	}{
		{state.JournalAbsenceIntent, Observed{State: ObservedAbsent}, ActionAbsenceObserved},
		{state.JournalAbsenceRestored, Observed{State: ObservedAbsent}, ActionCompleteConsistent},
		{state.JournalAbsenceIntent, Observe(target, nil), ActionRestoreAbsence},
		{state.JournalAbsenceRestored, Observe(target, nil), ActionAmbiguous},
		{state.JournalAbsenceIntent, Observed{}, ActionAmbiguous},
		{state.JournalAbsenceRestored, Observed{}, ActionAmbiguous},
	} {
		j := journalFirstInstall(tc.cp)
		if r := Reconcile(j, tc.obs, ProvenFacts{}); r.Action != tc.want {
			t.Fatalf("%s/%v: %+v", tc.cp, tc.obs.State, r)
		}
		// A prior version always rules out this branch, even when missing on disk.
		if r := Reconcile(journalWithPrior(tc.cp), tc.obs, ProvenFacts{}); r.Action != ActionAmbiguous {
			t.Fatalf("prior version accepted: %+v", r)
		}
	}
	foreign := target
	foreign.InstallID = "other-install"
	if r := Reconcile(journalFirstInstall(state.JournalAbsenceIntent), Observe(foreign, nil), ProvenFacts{}); r.Action != ActionAmbiguous {
		t.Fatal("foreign selection", r)
	}
}
