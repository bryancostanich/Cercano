package activation

import (
	"errors"
	"math"

	"cercano/source/server/internal/updatecoord/state"
)

// ObservedState classifies one observation of the selection file. The zero
// value is ObservedUnknown: an observation nobody classified proves nothing.
// Absent is a first-class state, distinct from unreadable and malformed,
// and is produced only by Observe classifying the reader's absent result.
type ObservedState int

const (
	ObservedUnknown ObservedState = iota
	ObservedAbsent
	ObservedUnreadable
	ObservedMalformed
	ObservedPresent
)

func (o ObservedState) String() string {
	switch o {
	case ObservedAbsent:
		return "absent"
	case ObservedUnreadable:
		return "unreadable"
	case ObservedMalformed:
		return "malformed"
	case ObservedPresent:
		return "present"
	default:
		return "unknown"
	}
}

// Observed is one observation of the selection file: a classification and,
// only when present, the descriptor.
type Observed struct {
	State     ObservedState
	Selection *Selection
}

// Observe classifies a ReadSelection result so the executor never
// string-matches errors. An unexpected error classifies as unreadable:
// untrustworthy observations refuse automatic changes.
func Observe(sel Selection, err error) Observed {
	switch {
	case err == nil:
		copied := sel
		return Observed{State: ObservedPresent, Selection: &copied}
	case errors.Is(err, ErrSelectionAbsent):
		return Observed{State: ObservedAbsent}
	case errors.Is(err, ErrSelectionUnreadable):
		return Observed{State: ObservedUnreadable}
	case errors.Is(err, ErrSelectionMalformed):
		return Observed{State: ObservedMalformed}
	default:
		return Observed{State: ObservedUnreadable}
	}
}

// ProvenFacts carries only externally PROVEN facts. Every field defaults to
// "unproven": a false means unknown, never healthy or verified, and an
// unknown fact never licenses an automatic change.
type ProvenFacts struct {
	// HealthFailed reports a proven failure of the target's post-switch
	// health verification. False means no proven failure, never a proven
	// success; health success comes only from the journal's
	// health-verified checkpoint.
	HealthFailed bool
}

// Action is the typed next safe action for the executor: a decision, not an
// effect. This package performs nothing.
type Action string

const (
	// ActionBeforeSwitch: the observed selection is exactly the journal's
	// pre-switch expectation (the recorded prior selection, or no
	// selection for an explicit first install); the recorded switch may
	// proceed.
	ActionBeforeSwitch Action = "before-switch"
	// ActionTargetAwaitHealth: the target selection is proven active (by
	// the selection file, never inferred from a pointer or version); the
	// next step is health verification.
	ActionTargetAwaitHealth Action = "target-selected-await-health"
	// ActionCleanupPermitted: the journal records health verified for the
	// active target; the superseded version may be cleaned up. Licensed
	// only by the durable health-verified (or cleanup-pending)
	// checkpoint, never by the pointer or version.
	ActionCleanupPermitted Action = "cleanup-permitted"
	// ActionRestorePrior: the recorded prior selection must be kept or
	// restored. Signals the need for the exact prior proof only; it is
	// returned solely for an explicit recorded prior selection and never
	// licenses guessing a path or inventing a prior version.
	ActionRestorePrior Action = "restore-prior-needed"
	// These are recovery needs/observations, never deletion authorization.
	// The executor must still prove candidate exit and exact selection ownership.
	ActionRestoreAbsence  Action = "restore-absence-needed"
	ActionAbsenceObserved Action = "absence-observed-await-confirmation"
	// ActionCompleteConsistent: the journal is complete (or restored) and
	// the observed selection agrees with its final state.
	ActionCompleteConsistent Action = "complete-consistent"
	// ActionAmbiguous: no automatic change is proven safe; a human or an
	// explicit recovery decision must resolve.
	ActionAmbiguous Action = "ambiguity-manual-recovery"
)

// Reason is a machine-readable explanation paired with each Action.
type Reason string

const (
	ReasonAbsenceRestored                Reason = "absence-restored-consistent"
	ReasonAbsenceObserved                Reason = "absence-observed-await-confirmation"
	ReasonAbsenceOutstanding             Reason = "absence-restoration-outstanding"
	ReasonAbsenceContradicted            Reason = "absence-restored-but-target-present"
	ReasonFirstInstallBeforeSwitch       Reason = "first-install-before-switch"
	ReasonPriorSelectionActive           Reason = "prior-selection-still-active"
	ReasonTargetSelectedAwaitHealth      Reason = "target-selected-await-health"
	ReasonHealthVerifiedCleanupPermitted Reason = "health-verified-cleanup-permitted"
	ReasonCleanupPendingRetry            Reason = "cleanup-pending-retry"
	ReasonCompleteConsistent             Reason = "complete-consistent"
	ReasonRestoredConsistent             Reason = "restored-consistent"
	ReasonRestoreCommittedNotEffective   Reason = "restore-committed-not-effective"
	ReasonRestoreEffective               Reason = "restore-already-effective"
	ReasonHealthFailedRestorePrior       Reason = "health-failed-restore-prior"
	ReasonJournalInvalid                 Reason = "journal-invalid"
	ReasonObservationUnclassified        Reason = "observation-unclassified"
	ReasonSelectionUnreadable            Reason = "selection-unreadable"
	ReasonSelectionMalformed             Reason = "selection-malformed"
	ReasonSelectionMissing               Reason = "selection-missing"
	ReasonForeignInstallation            Reason = "foreign-installation"
	ReasonSelectionUnrelated             Reason = "selection-unrelated"
	ReasonTargetWithoutSwitchIntent      Reason = "target-selection-without-switch-intent"
	ReasonFutureGeneration               Reason = "future-generation"
	ReasonGenerationMismatch             Reason = "generation-mismatch"
	ReasonDigestMismatch                 Reason = "digest-mismatch"
	ReasonStageIdentifierMismatch        Reason = "stage-identifier-mismatch"
	ReasonSelectedNotEffective           Reason = "switch-recorded-but-not-effective"
	ReasonRestoreRecordedButTargetActive Reason = "restore-recorded-but-target-active"
	ReasonHealthFailedNoPrior            Reason = "health-failed-no-prior-selection"
	ReasonContradictoryFacts             Reason = "contradictory-facts"
)

// Result is the typed decision: the next safe action and why.
type Result struct {
	Action Action
	Reason Reason
}

func ambiguous(reason Reason) Result { return Result{ActionAmbiguous, reason} }

// Reconcile is a pure function over a validated activation journal, one
// observation of the selection file, and externally proven facts. It reads
// nothing, writes nothing, never mutates its inputs, and returns the typed
// next safe action.
//
// Contract:
//
//   - The journal must be the state package's validated product, including
//     its self-declared installation binding; anything invalid is
//     ambiguity, never an automatic change.
//   - The observation must be classified by Observe. An unclassified
//     observation is unknown; a present descriptor must re-pass
//     Selection.Validate before any matching, so a future or malformed
//     descriptor that merely names the target's fields authorizes nothing.
//   - The switch is intent-before-effect: the journal's switch-intent
//     checkpoint precedes the selection change, so a target selection
//     observed at the prepared checkpoint is an effect without intent —
//     ambiguity, not the health, cleanup, or restore path. The legal
//     recovery (switch-intent or later) is preserved.
//   - Health success is only the journal's health-verified-or-later
//     checkpoint, never inferred from the selection file. Restore targets
//     only the journal's explicit recorded prior selection; the prior's
//     staged identifier is not a journal fact, and no journal field is
//     invented to make an ambiguous case decidable.
func Reconcile(journal state.ActivationJournal, observed Observed, facts ProvenFacts) Result {
	if err := state.ValidateActivationJournal(journal, journal.InstallID); err != nil {
		return ambiguous(ReasonJournalInvalid)
	}
	// The installation identifier must also be the safe opaque component
	// the store enforces; a self-consistent but malformed binding is not
	// a validated journal.
	if err := state.ValidateInstallID(journal.InstallID); err != nil {
		return ambiguous(ReasonJournalInvalid)
	}
	switch observed.State {
	case ObservedAbsent:
		return reconcileAbsent(journal)
	case ObservedPresent:
		// A present descriptor is trusted only if it is a valid one;
		// matching journal fields is meaningless for a future or
		// malformed descriptor.
		if observed.Selection == nil || observed.Selection.Validate() != nil {
			return ambiguous(ReasonSelectionMalformed)
		}
		return reconcilePresent(journal, *observed.Selection, facts)
	case ObservedUnreadable:
		return ambiguous(ReasonSelectionUnreadable)
	case ObservedMalformed:
		return ambiguous(ReasonSelectionMalformed)
	default:
		// ObservedUnknown (the zero value) or anything else: nobody
		// classified this observation, so nothing is proven.
		return ambiguous(ReasonObservationUnclassified)
	}
}

// hasPrior reports whether the journal records an explicit prior selection
// (the only rollback path there is).
func hasPrior(j state.ActivationJournal) bool { return j.PriorSelectedVersion != "" }

// matchesTarget binds the observed selection to the journal's TARGET
// selection: same installation, target version, staged identifier, verified
// digest, at the successor generation of the recorded prior generation. A
// prior generation at the int64 bound has no successor and never matches.
func matchesTarget(j state.ActivationJournal, s Selection) bool {
	if j.PriorSelectionGeneration == math.MaxInt64 {
		return false
	}
	return s.InstallID == j.InstallID &&
		s.SelectedVersion == j.TargetVersion &&
		s.StagedVersionDir == j.StagedVersionDir &&
		s.VerifiedArtifactSHA256 == j.VerifiedArtifactSHA256 &&
		s.Generation == j.PriorSelectionGeneration+1
}

// matchesPrior binds the observed selection to the journal's recorded PRIOR
// selection: same installation, version, generation, and digest. The
// prior's staged identifier is NOT a recorded journal fact, so this is a
// partial binding by definition. False when no explicit prior is recorded.
func matchesPrior(j state.ActivationJournal, s Selection) bool {
	if !hasPrior(j) {
		return false
	}
	return s.InstallID == j.InstallID &&
		s.SelectedVersion == j.PriorSelectedVersion &&
		s.Generation == j.PriorSelectionGeneration &&
		s.VerifiedArtifactSHA256 == j.PriorSelectionDigest
}

// reconcileAbsent decides for an ABSENT selection. With an explicit prior
// selection recorded the prior must exist, so absence is never benign;
// only an explicit first install before its first switch continues.
func reconcileAbsent(j state.ActivationJournal) Result {
	if hasPrior(j) {
		return ambiguous(ReasonSelectionMissing)
	}
	switch j.Checkpoint {
	case state.JournalAbsenceIntent:
		return Result{ActionAbsenceObserved, ReasonAbsenceObserved}
	case state.JournalAbsenceRestored:
		return Result{ActionCompleteConsistent, ReasonAbsenceRestored}
	case state.JournalPrepared, state.JournalSwitchIntent:
		// Explicit first install with no prior selection and no selection
		// file yet: the switch has not happened. No rollback pretense.
		return Result{ActionBeforeSwitch, ReasonFirstInstallBeforeSwitch}
	default:
		return ambiguous(ReasonSelectionMissing)
	}
}

// reconcilePresent decides for a PRESENT, already re-validated selection.
func reconcilePresent(j state.ActivationJournal, s Selection, facts ProvenFacts) Result {
	if s.InstallID != j.InstallID {
		return ambiguous(ReasonForeignInstallation)
	}
	if matchesTarget(j, s) {
		return reconcileTarget(j, facts)
	}
	if matchesPrior(j, s) {
		return reconcilePriorObserved(j, facts)
	}
	return ambiguous(mismatchReason(j, s))
}

// mismatchReason explains a present selection matching neither target nor
// prior: a version-near miss gets its precise reason, anything else is
// unrelated. Every case refuses automatic changes.
func mismatchReason(j state.ActivationJournal, s Selection) Reason {
	switch s.SelectedVersion {
	case j.TargetVersion:
		// The version is the target but some binding fact is wrong. A
		// generation ABOVE the recorded successor is future; anything
		// else is a mismatch. With the generation right, the remaining
		// misses are the staged identifier and the digest.
		if j.PriorSelectionGeneration != math.MaxInt64 && s.Generation > j.PriorSelectionGeneration+1 {
			return ReasonFutureGeneration
		}
		if s.Generation != j.PriorSelectionGeneration+1 {
			return ReasonGenerationMismatch
		}
		if s.StagedVersionDir != j.StagedVersionDir {
			return ReasonStageIdentifierMismatch
		}
		return ReasonDigestMismatch
	case j.PriorSelectedVersion:
		// Near-miss on the prior identity the journal recorded.
		if s.Generation != j.PriorSelectionGeneration {
			return ReasonGenerationMismatch
		}
		return ReasonDigestMismatch
	default:
		return ReasonSelectionUnrelated
	}
}

// reconcileTarget decides when the observed selection IS the target. The
// selection file proves the switch took effect, never health: only the
// journal's health-verified-or-later checkpoint licenses cleanup, and a
// proven health failure routes to the explicit restore path only while no
// durable record contradicts it.
func reconcileTarget(j state.ActivationJournal, facts ProvenFacts) Result {
	switch j.Checkpoint {
	case state.JournalPrepared:
		// The switch is write-intent-before-effect: the durable
		// switch-intent checkpoint must precede the selection change. A
		// target selection while the journal is still at prepared is an
		// effect without recorded intent — ambiguity, never the health,
		// cleanup, or restore path, whatever the facts say.
		return ambiguous(ReasonTargetWithoutSwitchIntent)
	case state.JournalSwitchIntent, state.JournalSelected:
		if facts.HealthFailed {
			// The target is proven bad before any verified-health record.
			// Restoring requires an explicit prior selection; a failed
			// first install has nothing to restore.
			if hasPrior(j) {
				return Result{ActionRestorePrior, ReasonHealthFailedRestorePrior}
			}
			return Result{ActionRestoreAbsence, ReasonHealthFailedNoPrior}
		}
		// The file is the stronger evidence: the target is selected, so
		// the next step is health verification (the journal may be
		// advanced to selected by the executor).
		return Result{ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth}
	case state.JournalHealthVerified, state.JournalCleanupPending, state.JournalComplete:
		if facts.HealthFailed {
			// A durable verified-health record contradicted by a proven
			// failure: a human decides.
			return ambiguous(ReasonContradictoryFacts)
		}
		switch j.Checkpoint {
		case state.JournalHealthVerified:
			return Result{ActionCleanupPermitted, ReasonHealthVerifiedCleanupPermitted}
		case state.JournalCleanupPending:
			return Result{ActionCleanupPermitted, ReasonCleanupPendingRetry}
		default:
			return Result{ActionCompleteConsistent, ReasonCompleteConsistent}
		}
	case state.JournalAbsenceIntent:
		return Result{ActionRestoreAbsence, ReasonAbsenceOutstanding}
	case state.JournalAbsenceRestored:
		return ambiguous(ReasonAbsenceContradicted)
	case state.JournalRollbackIntent:
		// The restore is committed but has not taken effect; the file
		// proves the restore is still outstanding.
		return Result{ActionRestorePrior, ReasonRestoreCommittedNotEffective}
	case state.JournalRestored:
		// The journal claims the restore completed, but the file still
		// points at the target: contradiction.
		return ambiguous(ReasonRestoreRecordedButTargetActive)
	default:
		return ambiguous(ReasonJournalInvalid)
	}
}

// reconcilePriorObserved decides when the observed selection IS the
// recorded prior. The restore target is exactly the journal's explicit
// prior selection, never invented.
func reconcilePriorObserved(j state.ActivationJournal, facts ProvenFacts) Result {
	switch j.Checkpoint {
	case state.JournalPrepared, state.JournalSwitchIntent:
		// The switch has not been committed: the prior being active is
		// simply unchanged.
		if facts.HealthFailed {
			// Health verification happens after the switch; a proven
			// failure before any committed switch is a contradiction.
			return ambiguous(ReasonContradictoryFacts)
		}
		return Result{ActionBeforeSwitch, ReasonPriorSelectionActive}
	case state.JournalSelected:
		if facts.HealthFailed {
			// The switch was committed but the prior is what is active;
			// with the target proven bad, keep the prior and drive the
			// restore path.
			return Result{ActionRestorePrior, ReasonRestoreEffective}
		}
		// The journal claims the switch was committed but the file never
		// moved: the claim is not effective.
		return ambiguous(ReasonSelectedNotEffective)
	case state.JournalHealthVerified, state.JournalCleanupPending, state.JournalComplete:
		if facts.HealthFailed {
			return ambiguous(ReasonContradictoryFacts)
		}
		// Verified-health claims about a target that is NOT the active
		// selection are not effective either.
		return ambiguous(ReasonSelectedNotEffective)
	case state.JournalRollbackIntent:
		// The restore is committed and the file proves it already took
		// effect; the executor commits the restored checkpoint.
		return Result{ActionRestorePrior, ReasonRestoreEffective}
	case state.JournalRestored:
		return Result{ActionCompleteConsistent, ReasonRestoredConsistent}
	default:
		return ambiguous(ReasonJournalInvalid)
	}
}
