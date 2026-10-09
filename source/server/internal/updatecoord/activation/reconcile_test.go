package activation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/updatecoord/state"
)

const (
	testInstall = "test-install"
	testTarget  = "9.9.9"
	testPrior   = "8.8.8"
)

var (
	targetDigest = strings.Repeat("a", 64)
	priorDigest  = strings.Repeat("b", 64)
	otherDigest  = strings.Repeat("c", 64)
)

// journalWithPrior is the canonical explicit-prior journal: target 9.9.9
// replacing a prior complete selection (8.8.8, generation 3).
func journalWithPrior(cp state.JournalCheckpoint) state.ActivationJournal {
	return state.ActivationJournal{
		SchemaVersion:            state.ActivationJournalSchemaVersion,
		InstallID:                testInstall,
		OpID:                     1,
		TargetVersion:            testTarget,
		StagedVersionDir:         "staged-" + testTarget,
		VerifiedArtifactSHA256:   targetDigest,
		PriorSelectedVersion:     testPrior,
		PriorSelectionGeneration: 3,
		PriorSelectionDigest:     priorDigest,
		Checkpoint:               cp,
	}
}

// journalFirstInstall is the explicit prior-none journal: a first install
// never pretends a prior version exists.
func journalFirstInstall(cp state.JournalCheckpoint) state.ActivationJournal {
	return state.ActivationJournal{
		SchemaVersion:          state.ActivationJournalSchemaVersion,
		InstallID:              testInstall,
		OpID:                   1,
		TargetVersion:          testTarget,
		StagedVersionDir:       "staged-" + testTarget,
		VerifiedArtifactSHA256: targetDigest,
		Checkpoint:             cp,
	}
}

// selectionFor builds a validated selection descriptor.
func selectionFor(install, version, staged string, gen int64, digest string) Selection {
	return Selection{
		SchemaVersion:          SelectionSchemaVersion,
		InstallID:              install,
		Generation:             gen,
		SelectedVersion:        version,
		StagedVersionDir:       staged,
		VerifiedArtifactSHA256: digest,
	}
}

func targetSelection() Selection {
	return selectionFor(testInstall, testTarget, "staged-"+testTarget, 4, targetDigest)
}

func priorSelection() Selection {
	// The prior selection's staged identifier is not a recorded journal
	// fact; only installation, version, generation, and digest bind.
	return selectionFor(testInstall, testPrior, "staged-"+testPrior, 3, priorDigest)
}

func present(s Selection) Observed {
	copied := s
	return Observed{State: ObservedPresent, Selection: &copied}
}

var allCheckpoints = []state.JournalCheckpoint{
	state.JournalPrepared, state.JournalSwitchIntent, state.JournalSelected,
	state.JournalHealthVerified, state.JournalCleanupPending, state.JournalComplete,
	state.JournalRollbackIntent, state.JournalRestored,
}

// TestReconcileTableWithPriorSelection covers EVERY checkpoint of an
// explicit-prior journal against every observation class: the target
// selection, the prior selection, absent, unrelated, foreign, malformed,
// unreadable, and every generation/digest/staged mismatch. Health is never
// inferred: the await-health action appears before the verified checkpoint
// and only the verified-or-later checkpoints license cleanup.
func TestReconcileTableWithPriorSelection(t *testing.T) {
	cases := map[string]Observed{
		"target":      present(targetSelection()),
		"prior":       present(priorSelection()),
		"absent":      {State: ObservedAbsent},
		"unrelated":   present(selectionFor(testInstall, "7.7.7", "staged-7.7.7", 9, otherDigest)),
		"foreign":     present(selectionFor("other-install", testTarget, "staged-"+testTarget, 4, targetDigest)),
		"malformed":   {State: ObservedMalformed},
		"unreadable":  {State: ObservedUnreadable},
		"future-gen":  present(selectionFor(testInstall, testTarget, "staged-"+testTarget, 6, targetDigest)),
		"stale-gen":   present(selectionFor(testInstall, testTarget, "staged-"+testTarget, 2, targetDigest)),
		"stage-miss":  present(selectionFor(testInstall, testTarget, "staged-other", 4, targetDigest)),
		"digest-miss": present(selectionFor(testInstall, testTarget, "staged-"+testTarget, 4, otherDigest)),
		"prior-gen":   present(selectionFor(testInstall, testPrior, "staged-"+testPrior, 4, priorDigest)),
		"prior-dgst":  present(selectionFor(testInstall, testPrior, "staged-"+testPrior, 3, otherDigest)),
	}
	// want[checkpoint][case] = (action, reason). Unlisted combinations are
	// ambiguous/manual-recovery with the case's default reason.
	wantTarget := map[state.JournalCheckpoint]Result{
		state.JournalPrepared:       {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalSwitchIntent:   {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalSelected:       {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalHealthVerified: {ActionCleanupPermitted, ReasonHealthVerifiedCleanupPermitted},
		state.JournalCleanupPending: {ActionCleanupPermitted, ReasonCleanupPendingRetry},
		state.JournalComplete:       {ActionCompleteConsistent, ReasonCompleteConsistent},
		state.JournalRollbackIntent: {ActionRestorePrior, ReasonRestoreCommittedNotEffective},
		state.JournalRestored:       {ActionAmbiguous, ReasonRestoreRecordedButTargetActive},
	}
	wantPrior := map[state.JournalCheckpoint]Result{
		state.JournalPrepared:       {ActionBeforeSwitch, ReasonPriorSelectionActive},
		state.JournalSwitchIntent:   {ActionBeforeSwitch, ReasonPriorSelectionActive},
		state.JournalSelected:       {ActionAmbiguous, ReasonSelectedNotEffective},
		state.JournalHealthVerified: {ActionAmbiguous, ReasonSelectedNotEffective},
		state.JournalCleanupPending: {ActionAmbiguous, ReasonSelectedNotEffective},
		state.JournalComplete:       {ActionAmbiguous, ReasonSelectedNotEffective},
		state.JournalRollbackIntent: {ActionRestorePrior, ReasonRestoreEffective},
		state.JournalRestored:       {ActionCompleteConsistent, ReasonRestoredConsistent},
	}
	// Default ambiguity reason per observation case, used whenever the
	// case is not target/prior (mismatch and foreign cases carry their own
	// reason; absent, malformed, unreadable carry theirs).
	ambiguity := map[string]Reason{
		"absent":      ReasonSelectionMissing,
		"unrelated":   ReasonSelectionUnrelated,
		"foreign":     ReasonForeignInstallation,
		"malformed":   ReasonSelectionMalformed,
		"unreadable":  ReasonSelectionUnreadable,
		"future-gen":  ReasonFutureGeneration,
		"stale-gen":   ReasonGenerationMismatch,
		"stage-miss":  ReasonStageIdentifierMismatch,
		"digest-miss": ReasonDigestMismatch,
		"prior-gen":   ReasonGenerationMismatch,
		"prior-dgst":  ReasonDigestMismatch,
	}
	for _, cp := range allCheckpoints {
		for name, observed := range cases {
			got := Reconcile(journalWithPrior(cp), observed, ProvenFacts{})
			var want Result
			switch name {
			case "target":
				want = wantTarget[cp]
			case "prior":
				want = wantPrior[cp]
			default:
				want = Result{ActionAmbiguous, ambiguity[name]}
			}
			if got != want {
				t.Errorf("%s/%s: got (%s, %s), want (%s, %s)", cp, name,
					got.Action, got.Reason, want.Action, want.Reason)
			}
		}
	}
}

// TestReconcileTableFirstInstall covers the explicit prior-none journal
// against the same observation classes. No rollback branch exists for a
// first install: nothing pretends a prior version, and a proven health
// failure with no prior is manual recovery.
func TestReconcileTableFirstInstall(t *testing.T) {
	cases := map[string]Observed{
		"target":     present(targetSelection()), // generation 1 is prior-none's successor
		"absent":     {State: ObservedAbsent},
		"unrelated":  present(selectionFor(testInstall, "7.7.7", "staged-7.7.7", 9, otherDigest)),
		"foreign":    present(selectionFor("other-install", testTarget, "staged-"+testTarget, 1, targetDigest)),
		"malformed":  {State: ObservedMalformed},
		"unreadable": {State: ObservedUnreadable},
		"future-gen": present(selectionFor(testInstall, testTarget, "staged-"+testTarget, 5, targetDigest)),
	}
	// With prior generation 0, the target's expected generation is 1.
	wantTarget := map[state.JournalCheckpoint]Result{
		state.JournalPrepared:       {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalSwitchIntent:   {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalSelected:       {ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth},
		state.JournalHealthVerified: {ActionCleanupPermitted, ReasonHealthVerifiedCleanupPermitted},
		state.JournalCleanupPending: {ActionCleanupPermitted, ReasonCleanupPendingRetry},
		state.JournalComplete:       {ActionCompleteConsistent, ReasonCompleteConsistent},
		// Rollback-intent/restored with no explicit prior cannot occur in
		// a validated journal; hand-built ones must be refused as invalid.
		state.JournalRollbackIntent: {ActionAmbiguous, ReasonJournalInvalid},
		state.JournalRestored:       {ActionAmbiguous, ReasonJournalInvalid},
	}
	ambiguity := map[string]Reason{
		"absent":     ReasonSelectionMissing,
		"unrelated":  ReasonSelectionUnrelated,
		"foreign":    ReasonForeignInstallation,
		"malformed":  ReasonSelectionMalformed,
		"unreadable": ReasonSelectionUnreadable,
		"future-gen": ReasonFutureGeneration,
	}
	for _, cp := range allCheckpoints {
		for name, observed := range cases {
			// The "target" case uses generation 1, the successor of the
			// explicit prior-none generation 0.
			if name == "target" {
				observed.Selection.Generation = 1
			}
			got := Reconcile(journalFirstInstall(cp), observed, ProvenFacts{})
			want := Result{ActionAmbiguous, ReasonJournalInvalid}
			if cp != state.JournalRollbackIntent && cp != state.JournalRestored {
				// A first-install journal cannot legally hold rollback or
				// restored checkpoints at all: every observation at those
				// checkpoints must be refused as an invalid journal before
				// the observation is even consulted.
				want = Result{ActionAmbiguous, ambiguity[name]}
				if name == "target" {
					want = wantTarget[cp]
				} else if name == "absent" &&
					(cp == state.JournalPrepared || cp == state.JournalSwitchIntent) {
					// An explicit first install before its first switch: the
					// absent selection is the expected unchanged state.
					want = Result{ActionBeforeSwitch, ReasonFirstInstallBeforeSwitch}
				}
			}
			if got != want {
				t.Errorf("%s/%s: got (%s, %s), want (%s, %s)", cp, name,
					got.Action, got.Reason, want.Action, want.Reason)
			}
		}
	}
}

// TestReconcileProvenHealthFailure covers the externally proven
// health-failure fact. A failure routes to the explicit restore path ONLY
// before any durable verified-health record; a failure contradicting the
// verified-health checkpoints is manual recovery, and a failed first
// install has nothing to restore.
func TestReconcileProvenHealthFailure(t *testing.T) {
	// The successor-generation target matching each journal's prior.
	targetForPrior := present(targetSelection())
	targetForNone := present(targetSelection())
	targetForNone.Selection.Generation = 1
	cases := []struct {
		checkpoint state.JournalCheckpoint
		wantPrior  Result
		wantNone   Result
	}{
		// A file-proven target selection plus a proven health failure is
		// consistent with a crash after the switch but before the
		// journal's checkpoint caught up: restore the explicit prior.
		{checkpoint: state.JournalPrepared,
			wantPrior: Result{ActionRestorePrior, ReasonHealthFailedRestorePrior},
			wantNone:  Result{ActionAmbiguous, ReasonHealthFailedNoPrior}},
		{checkpoint: state.JournalSwitchIntent,
			wantPrior: Result{ActionRestorePrior, ReasonHealthFailedRestorePrior},
			wantNone:  Result{ActionAmbiguous, ReasonHealthFailedNoPrior}},
		{checkpoint: state.JournalSelected,
			wantPrior: Result{ActionRestorePrior, ReasonHealthFailedRestorePrior},
			wantNone:  Result{ActionAmbiguous, ReasonHealthFailedNoPrior}},
		// Durable verified-health records are not overridden by a later
		// failure fact: manual recovery, never a silent contradiction.
		{checkpoint: state.JournalHealthVerified,
			wantPrior: Result{ActionAmbiguous, ReasonContradictoryFacts},
			wantNone:  Result{ActionAmbiguous, ReasonContradictoryFacts}},
		{checkpoint: state.JournalCleanupPending,
			wantPrior: Result{ActionAmbiguous, ReasonContradictoryFacts},
			wantNone:  Result{ActionAmbiguous, ReasonContradictoryFacts}},
		{checkpoint: state.JournalComplete,
			wantPrior: Result{ActionAmbiguous, ReasonContradictoryFacts},
			wantNone:  Result{ActionAmbiguous, ReasonContradictoryFacts}},
	}
	for _, tc := range cases {
		if got := Reconcile(journalWithPrior(tc.checkpoint), targetForPrior, ProvenFacts{HealthFailed: true}); got != tc.wantPrior {
			t.Errorf("prior/target %s: got (%s, %s), want (%s, %s)", tc.checkpoint,
				got.Action, got.Reason, tc.wantPrior.Action, tc.wantPrior.Reason)
		}
		if got := Reconcile(journalFirstInstall(tc.checkpoint), targetForNone, ProvenFacts{HealthFailed: true}); got != tc.wantNone {
			t.Errorf("first/target %s: got (%s, %s), want (%s, %s)", tc.checkpoint,
				got.Action, got.Reason, tc.wantNone.Action, tc.wantNone.Reason)
		}
	}
	// Prior selection observed with a proven failure: keep the prior and
	// drive the restore path once the switch was committed; contradiction
	// before that; the restore path itself ignores the stale failure fact.
	priorWant := map[state.JournalCheckpoint]Result{
		state.JournalPrepared:       {ActionAmbiguous, ReasonContradictoryFacts},
		state.JournalSwitchIntent:   {ActionAmbiguous, ReasonContradictoryFacts},
		state.JournalSelected:       {ActionRestorePrior, ReasonRestoreEffective},
		state.JournalHealthVerified: {ActionAmbiguous, ReasonContradictoryFacts},
		state.JournalCleanupPending: {ActionAmbiguous, ReasonContradictoryFacts},
		state.JournalComplete:       {ActionAmbiguous, ReasonContradictoryFacts},
		state.JournalRollbackIntent: {ActionRestorePrior, ReasonRestoreEffective},
		state.JournalRestored:       {ActionCompleteConsistent, ReasonRestoredConsistent},
	}
	for _, cp := range allCheckpoints {
		got := Reconcile(journalWithPrior(cp), present(priorSelection()), ProvenFacts{HealthFailed: true})
		if want := priorWant[cp]; got != want {
			t.Errorf("prior-observed %s with failure: got (%s, %s), want (%s, %s)", cp,
				got.Action, got.Reason, want.Action, want.Reason)
		}
	}
}

// TestReconcileRefusesInvalidJournal: a journal that fails the state
// package's own validation (unknown checkpoint, mismatched prior block,
// wrong schema) is ambiguity, never an automatic change.
func TestReconcileRefusesInvalidJournal(t *testing.T) {
	mutations := map[string]func(*state.ActivationJournal){
		"unknown checkpoint": func(j *state.ActivationJournal) { j.Checkpoint = state.JournalCheckpoint("time-travel") },
		"schema version":     func(j *state.ActivationJournal) { j.SchemaVersion = 99 },
		"partial prior":      func(j *state.ActivationJournal) { j.PriorSelectionGeneration = 0 },
		"prior digest":       func(j *state.ActivationJournal) { j.PriorSelectionDigest = "nothex" },
		"bad install id":     func(j *state.ActivationJournal) { j.InstallID = "../escape" },
		"absolute staged":    func(j *state.ActivationJournal) { j.StagedVersionDir = "/abs" },
		"target digest":      func(j *state.ActivationJournal) { j.VerifiedArtifactSHA256 = strings.Repeat("A", 64) },
	}
	observations := []Observed{present(targetSelection()), present(priorSelection()), {State: ObservedAbsent}}
	for name, mutate := range mutations {
		for _, observed := range observations {
			j := journalWithPrior(state.JournalSelected)
			mutate(&j)
			if got := Reconcile(j, observed, ProvenFacts{}); got != (Result{ActionAmbiguous, ReasonJournalInvalid}) {
				t.Errorf("%s: got (%s, %s), want journal-invalid ambiguity", name, got.Action, got.Reason)
			}
		}
	}
	// A prior-none journal that somehow claims the rollback path is
	// invalid: restore without a prior selection would pretend a version.
	j := journalFirstInstall(state.JournalRollbackIntent)
	if got := Reconcile(j, present(targetSelection()), ProvenFacts{}); got != (Result{ActionAmbiguous, ReasonJournalInvalid}) {
		t.Errorf("prior-none rollback: got (%s, %s)", got.Action, got.Reason)
	}
}

// TestReconcilePurity verifies the documented contract that Reconcile never
// mutates its inputs, and that an unproven fact (false) never behaves as a
// proven success.
func TestReconcilePurity(t *testing.T) {
	j := journalWithPrior(state.JournalSelected)
	observed := present(targetSelection())
	facts := ProvenFacts{HealthFailed: true}
	_ = Reconcile(j, observed, facts)
	if facts.HealthFailed != true {
		t.Fatal("facts mutated")
	}
	if *observed.Selection != targetSelection() {
		t.Fatal("observed selection mutated")
	}
	if j != journalWithPrior(state.JournalSelected) {
		t.Fatal("journal mutated")
	}
	// Unproven (false) health never licenses cleanup: only the durable
	// verified checkpoint does.
	got := Reconcile(journalWithPrior(state.JournalSelected), observed, ProvenFacts{})
	if got.Action != ActionTargetAwaitHealth {
		t.Fatalf("unproven health misused: %+v", got)
	}
}

// TestReconcileWithStoreLoadedJournal binds the whole contract end to end
// against the real state store: a journal written and loaded through the
// store's validated path reconciles with the file reader's observation.
// All state lives in a temporary directory the test owns.
func TestReconcileWithStoreLoadedJournal(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(t.TempDir(), testInstall)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := state.NewAdapter(store)
	snap, err := adapter.Start(ctx, testTarget)
	if err != nil {
		t.Fatal(err)
	}

	// Insert at prepared, then legally advance to switch-intent and
	// selected via the store's revision CAS.
	j := journalWithPrior(state.JournalPrepared)
	j.OpID = snap.ID
	rev, err := store.SaveActivationJournal(ctx, 0, j)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := writeSelectionFile(t, dir, "selection.json", selectionJSONFor(priorSelection()))
	sel, rerr := ReadSelection(path)
	loaded, _, err := store.LoadActivationJournal(ctx, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := Reconcile(loaded, Observe(sel, rerr), ProvenFacts{})
	if got != (Result{ActionBeforeSwitch, ReasonPriorSelectionActive}) {
		t.Fatalf("store-loaded prepared journal: got (%s, %s)", got.Action, got.Reason)
	}

	for _, cp := range []state.JournalCheckpoint{state.JournalSwitchIntent, state.JournalSelected} {
		j.Checkpoint = cp
		if rev, err = store.SaveActivationJournal(ctx, rev, j); err != nil {
			t.Fatal(err)
		}
	}
	loaded, _, err = store.LoadActivationJournal(ctx, snap.ID)
	if err != nil || loaded.Checkpoint != state.JournalSelected {
		t.Fatalf("advance failed: %v", err)
	}
	path = writeSelectionFile(t, dir, "selection.json", selectionJSONFor(targetSelection()))
	sel, rerr = ReadSelection(path)
	got = Reconcile(loaded, Observe(sel, rerr), ProvenFacts{})
	if got != (Result{ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth}) {
		t.Fatalf("store-loaded selected journal: got (%s, %s)", got.Action, got.Reason)
	}
}

// selectionJSONFor renders a selection descriptor the way the strict parser
// expects; used only to round-trip descriptors through the file reader.
func selectionJSONFor(s Selection) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}
