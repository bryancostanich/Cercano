package activation

import (
	"strings"
	"testing"

	"cercano/source/server/internal/updatecoord/state"
)

// Regressions for unproven input. None of these observations proves what
// the journal needs proven: an unclassified observation is not a proven
// absent selection, a descriptor that fails Selection.Validate is not a
// proven target selection even when its other fields match, and a target
// selection observed while the journal is still at the prepared checkpoint
// is an effect without the durable switch intent that must precede it.
// Every case must be ambiguity — no automatic action, at any checkpoint,
// under any facts.

// invalidTargetDescriptors builds present observations that match the
// journal's target binding in every field Selection.Validate does not
// check (or that miss one checked field), so matching alone must never
// authorize an automatic action.
func invalidTargetDescriptors() map[string]Selection {
	base := targetSelection()
	mutations := map[string]func(*Selection){
		"future schema version": func(s *Selection) { s.SchemaVersion = SelectionSchemaVersion + 1 },
		"zero schema version":   func(s *Selection) { s.SchemaVersion = 0 },
		"generation zero":       func(s *Selection) { s.Generation = 0 },
		"empty version":         func(s *Selection) { s.SelectedVersion = "" },
		"absolute staged dir":   func(s *Selection) { s.StagedVersionDir = "/abs" },
		"nonhex digest":         func(s *Selection) { s.VerifiedArtifactSHA256 = strings.Repeat("g", 64) },
	}
	out := map[string]Selection{}
	for name, mutate := range mutations {
		s := base
		mutate(&s)
		out[name] = s
	}
	return out
}

// TestReconcileZeroObservationNeverProvesAbsent: the zero Observed is an
// unclassified observation. Absence is proven only by Observe classifying
// ErrSelectionAbsent; a zero value flowing into Reconcile must not be
// treated as a proven absent selection and license a first install's
// before-switch action.
func TestReconcileZeroObservationNeverProvesAbsent(t *testing.T) {
	if got := (Observed{}).State; got != ObservedUnknown {
		t.Fatalf("zero ObservedState: got %s, want ObservedUnknown", got)
	}
	for _, cp := range []state.JournalCheckpoint{state.JournalPrepared, state.JournalSwitchIntent} {
		for _, facts := range []ProvenFacts{{}, {HealthFailed: true}} {
			got := Reconcile(journalFirstInstall(cp), Observed{}, facts)
			if want := (Result{ActionAmbiguous, ReasonObservationUnclassified}); got != want {
				t.Errorf("%s / zero observation: got (%s, %s), want (%s, %s): the zero observation is unclassified, not a proven absent selection", cp, got.Action, got.Reason, want.Action, want.Reason)
			}
		}
	}
}

// TestReconcileUnprovenObservationRefusesAutomaticAction: the full matrix of
// unproven observations (unclassified, present-but-invalid, malformed,
// unreadable, absent against a journal that records a prior selection) across
// every checkpoint and both fact sets must refuse every automatic action,
// with the exact refusal reason for each unproven class.
func TestReconcileUnprovenObservationRefusesAutomaticAction(t *testing.T) {
	unproven := map[string]Observed{
		"zero observation":           {},
		"present without descriptor": {State: ObservedPresent},
		"zero descriptor":            present(Selection{}),
		"malformed":                  {State: ObservedMalformed},
		"unreadable":                 {State: ObservedUnreadable},
	}
	reasons := map[string]Reason{
		"zero observation":           ReasonObservationUnclassified,
		"present without descriptor": ReasonSelectionMalformed,
		"zero descriptor":            ReasonSelectionMalformed,
		"malformed":                  ReasonSelectionMalformed,
		"unreadable":                 ReasonSelectionUnreadable,
	}
	for name, s := range invalidTargetDescriptors() {
		unproven["invalid descriptor: "+name] = present(s)
		reasons["invalid descriptor: "+name] = ReasonSelectionMalformed
	}
	for _, cp := range allCheckpoints {
		for name, observed := range unproven {
			for _, facts := range []ProvenFacts{{}, {HealthFailed: true}} {
				want := Result{ActionAmbiguous, reasons[name]}
				// A first-install journal at the rollback checkpoints is
				// itself invalid: the journal check refuses before the
				// observation is even consulted.
				if cp == state.JournalRollbackIntent || cp == state.JournalRestored {
					want = Result{ActionAmbiguous, ReasonJournalInvalid}
				}
				wantPrior := Result{ActionAmbiguous, reasons[name]}
				if got := Reconcile(journalWithPrior(cp), observed, facts); got != wantPrior {
					t.Errorf("prior journal %s / %s / %+v: got (%s, %s), want (%s, %s)", cp, name, facts, got.Action, got.Reason, wantPrior.Action, wantPrior.Reason)
				}
				if got := Reconcile(journalFirstInstall(cp), observed, facts); got != want {
					t.Errorf("first-install journal %s / %s / %+v: got (%s, %s), want (%s, %s)", cp, name, facts, got.Action, got.Reason, want.Action, want.Reason)
				}
			}
		}
		// A journal that records an explicit prior selection proves the
		// prior must exist: an absent observation is unproven input at
		// every checkpoint, never a benign state.
		for _, facts := range []ProvenFacts{{}, {HealthFailed: true}} {
			got := Reconcile(journalWithPrior(cp), Observed{State: ObservedAbsent}, facts)
			if want := (Result{ActionAmbiguous, ReasonSelectionMissing}); got != want {
				t.Errorf("prior journal %s / absent / %+v: got (%s, %s), want (%s, %s)", cp, facts, got.Action, got.Reason, want.Action, want.Reason)
			}
		}
	}
}

// TestReconcilePreparedTargetSelectionIsEffectWithoutIntent: the switch is
// intent-before-effect (the journal's switch-intent checkpoint precedes the
// selection change). A target selection observed while the journal is still
// at prepared is an effect without recorded intent: ambiguity, never the
// health path, cleanup, or restore — with or without a proven health
// failure. The legal crash recovery at switch-intent and later is preserved.
func TestReconcilePreparedTargetSelectionIsEffectWithoutIntent(t *testing.T) {
	priorTarget := present(targetSelection())
	firstTarget := present(targetSelection())
	firstTarget.Selection.Generation = 1
	for _, facts := range []ProvenFacts{{}, {HealthFailed: true}} {
		cases := map[string]struct {
			j        state.ActivationJournal
			observed Observed
		}{
			"prior journal":         {journalWithPrior(state.JournalPrepared), priorTarget},
			"first-install journal": {journalFirstInstall(state.JournalPrepared), firstTarget},
		}
		for name, tc := range cases {
			want := Result{ActionAmbiguous, ReasonTargetWithoutSwitchIntent}
			if got := Reconcile(tc.j, tc.observed, facts); got != want {
				t.Errorf("%s / %+v: got (%s, %s), want (%s, %s): the target cannot be effective before the durable switch intent", name, facts, got.Action, got.Reason, want.Action, want.Reason)
			}
		}
	}
	// Legal recovery preserved: once the durable switch intent exists, an
	// observed target is a crash after the committed effect.
	if got := Reconcile(journalWithPrior(state.JournalSwitchIntent), priorTarget, ProvenFacts{}); got != (Result{ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth}) {
		t.Errorf("switch-intent / target: got (%s, %s), want await-health", got.Action, got.Reason)
	}
	if got := Reconcile(journalWithPrior(state.JournalSwitchIntent), priorTarget, ProvenFacts{HealthFailed: true}); got != (Result{ActionRestorePrior, ReasonHealthFailedRestorePrior}) {
		t.Errorf("switch-intent / target / failed: got (%s, %s), want restore-prior", got.Action, got.Reason)
	}
}
