package activation

import (
	"errors"
	"math"

	"cercano/source/server/internal/updatecoord/state"
)

// ObservedState classifies what was observed about the selection file.
// Absent is a first-class state, distinct from unreadable and malformed.
type ObservedState int

const (
	ObservedAbsent ObservedState = iota
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
// only when present, the validated descriptor.
type Observed struct {
	State     ObservedState
	Selection *Selection
}

// Observe classifies a ReadSelection result into the typed observation
// reconciliation consumes, so the executor never string-matches errors. An
// unexpected error classifies as unreadable (refuse automatic changes).
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

// ProvenFacts carries only externally PROVEN facts the caller has already
// established. Every field defaults to "unproven": a false never means
// healthy or verified, it means UNKNOWN, and an unknown fact never licenses
// an automatic change.
type ProvenFacts struct {
	// HealthFailed reports a PROVEN failure of the target's
	// post-switch health verification (for example, an externally run
	// startup/health check that failed). False means no proven failure,
	// never a proven success — health SUCCESS is only ever taken from the
	// journal's health-verified checkpoint.
	HealthFailed bool
}

// Action is the typed next safe action for the executor. It is a decision,
// not an effect: this package performs nothing.
type Action string

const (
	// ActionBeforeSwitch: unchanged. The observed selection is exactly the
	// journal's pre-switch expectation (the recorded prior selection, or
	// no selection for an explicit first install). The executor may
	// proceed with the recorded switch when it chooses; nothing needs
	// recovering.
	ActionBeforeSwitch Action = "before-switch"
	// ActionTargetAwaitHealth: the target selection is proven active; the
	// next step is health verification. This is proven by the SELECTION
	// FILE, never inferred from a pointer or version alone.
	ActionTargetAwaitHealth Action = "target-selected-await-health"
	// ActionCleanupPermitted: the journal records health VERIFIED for the
	// active target selection, so the superseded version may be cleaned
	// up. Cleanup is licensed only by the durable health-verified (or the
	// later cleanup-pending) checkpoint — never by the pointer or version.
	ActionCleanupPermitted Action = "cleanup-permitted"
	// ActionRestorePrior: the recorded prior selection must be kept or
	// restored (and the journal driven down the restore path). This
	// action is only ever returned for an EXPLICIT prior selection; there
	// is no pretending a prior version exists for a first install.
	ActionRestorePrior Action = "restore-prior-needed"
	// ActionCompleteConsistent: the journal is complete (or restored) and
	// the observed selection agrees with its final state. Nothing to do.
	ActionCompleteConsistent Action = "complete-consistent"
	// ActionAmbiguous: no automatic change is proven safe. A human (or an
	// explicit recovery decision) must resolve the situation.
	ActionAmbiguous Action = "ambiguity-manual-recovery"
)

// Reason is a machine-readable explanation paired with each Action.
type Reason string

const (
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
	ReasonSelectionUnreadable            Reason = "selection-unreadable"
	ReasonSelectionMalformed             Reason = "selection-malformed"
	ReasonSelectionMissing               Reason = "selection-missing"
	ReasonForeignInstallation            Reason = "foreign-installation"
	ReasonSelectionUnrelated             Reason = "selection-unrelated"
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

// Reconcile is a PURE function over a validated activation journal, one
// observation of the selection file, and externally proven facts. It
// returns the typed next safe action and reason. It reads nothing, writes
// nothing, and never mutates its inputs.
//
// Contract and limits:
//
//   - The journal must be a journal the state package validated
//     (LoadActivationJournal's product); it is revalidated against its own
//     recorded installation binding here, and anything invalid is
//     ambiguity, never an automatic change.
//   - Health success is ONLY the journal's health-verified-or-later
//     checkpoint; it is never inferred from the selection pointing at the
//     target. Restore is ONLY the explicit prior selection (recorded by the
//     journal) — never from a checkpoint alone, and a first install
//     (explicit prior-none) never pretends a prior version exists.
//   - A selection matching the TARGET is bound to every journal fact the
//     journal records (installation, version, staged identifier, verified
//     digest) and to the NEXT generation after the recorded prior
//     generation. Anything unclassifiable, mismatched, or carrying a
//     future generation (beyond the recorded prior generation's successor)
//     is ambiguity: unproven facts refuse automatic changes. No new
//     journal fields are invented to make an ambiguous case decidable.
//   - The prior selection's own staged identifier is NOT a recorded journal
//     fact; prior matching binds installation, version, generation, and
//     digest only.
func Reconcile(journal state.ActivationJournal, observed Observed, facts ProvenFacts) Result {
	// The journal must be exactly what the state package validates —
	// including its self-declared installation binding — or nothing is
	// proven and every path below stays closed.
	if err := state.ValidateActivationJournal(journal, journal.InstallID); err != nil {
		return ambiguous(ReasonJournalInvalid)
	}
	// The installation identifier itself must be the safe opaque component
	// the store enforces; a self-consistent but malformed binding is not a
	// validated journal.
	if err := state.ValidateInstallID(journal.InstallID); err != nil {
		return ambiguous(ReasonJournalInvalid)
	}
	switch observed.State {
	case ObservedAbsent:
		return reconcileAbsent(journal)
	case ObservedPresent:
		if observed.Selection == nil {
			// Defensive: a present state without a descriptor is a
			// caller bug, treated as malformed.
			return ambiguous(ReasonSelectionMalformed)
		}
		return reconcilePresent(journal, *observed.Selection, facts)
	case ObservedUnreadable:
		return ambiguous(ReasonSelectionUnreadable)
	case ObservedMalformed:
		return ambiguous(ReasonSelectionMalformed)
	default:
		return ambiguous(ReasonSelectionUnreadable)
	}
}

// hasPrior reports whether the journal records an explicit prior selection
// (the only rollback path there is).
func hasPrior(j state.ActivationJournal) bool { return j.PriorSelectedVersion != "" }

// matchesTarget binds the observed selection to the journal's TARGET
// selection: same installation, target version, staged identifier, and
// verified artifact digest, at the successor generation of the recorded
// prior generation. A prior generation at the int64 bound cannot have a
// successor and simply never matches.
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
// selection: same installation, version, generation, and artifact digest.
// False when the journal records no explicit prior selection.
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
// selection recorded, absence is never benign — the prior selection must
// exist — so only an explicit first install before its first switch
// continues; everything past the pre-switch checkpoints is ambiguity.
func reconcileAbsent(j state.ActivationJournal) Result {
	if hasPrior(j) {
		return ambiguous(ReasonSelectionMissing)
	}
	switch j.Checkpoint {
	case state.JournalPrepared, state.JournalSwitchIntent:
		// Explicit first install with no prior selection and no selection
		// file yet: the switch has not happened. No rollback pretense.
		return Result{ActionBeforeSwitch, ReasonFirstInstallBeforeSwitch}
	default:
		return ambiguous(ReasonSelectionMissing)
	}
}

// reconcilePresent decides for a PRESENT validated selection.
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

// mismatchReason explains a present selection that matches neither the
// target nor the recorded prior: a version-near miss is given its precise
// mismatch reason (a FUTURE generation above all others); anything else is
// unrelated. All of them refuse automatic changes.
func mismatchReason(j state.ActivationJournal, s Selection) Reason {
	switch s.SelectedVersion {
	case j.TargetVersion:
		// The version is the target but some binding fact is wrong. A
		// generation ABOVE the recorded successor generation is future;
		// anything else generation-wise is a plain mismatch. (A prior
		// generation at the int64 bound has no successor, so every
		// observed generation is a mismatch.) With the generation right,
		// the remaining misses are the staged identifier and the digest.
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
		// Near-miss on the prior: the prior identity is exactly what the
		// journal recorded.
		if s.Generation != j.PriorSelectionGeneration {
			return ReasonGenerationMismatch
		}
		return ReasonDigestMismatch
	default:
		return ReasonSelectionUnrelated
	}
}

// reconcileTarget decides when the observed selection IS the target. The
// selection file is proof the switch took effect, but NEVER proof of
// health: only the journal's health-verified-or-later checkpoint licenses
// cleanup, and a PROVEN health failure routes to the explicit restore path
// only while no durable record contradicts it.
func reconcileTarget(j state.ActivationJournal, facts ProvenFacts) Result {
	switch j.Checkpoint {
	case state.JournalPrepared, state.JournalSwitchIntent, state.JournalSelected:
		if facts.HealthFailed {
			// The target is proven bad before any verified-health record
			// exists. Restoring requires an explicit prior selection; a
			// failed first install has nothing to restore and must not
			// pretend one exists.
			if hasPrior(j) {
				return Result{ActionRestorePrior, ReasonHealthFailedRestorePrior}
			}
			return ambiguous(ReasonHealthFailedNoPrior)
		}
		// The file is the stronger evidence: the target is selected, so
		// the next step is health verification (and the journal may be
		// advanced to selected by the executor).
		return Result{ActionTargetAwaitHealth, ReasonTargetSelectedAwaitHealth}
	case state.JournalHealthVerified, state.JournalCleanupPending, state.JournalComplete:
		if facts.HealthFailed {
			// A durable verified-health record contradicted by a proven
			// failure is a contradiction; a human decides.
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
	case state.JournalRollbackIntent:
		// The restore is committed but has NOT taken effect: the target is
		// still the active selection. Restore is needed; the restore is
		// NOT inferred from the checkpoint — the file proves the restore
		// is still outstanding.
		return Result{ActionRestorePrior, ReasonRestoreCommittedNotEffective}
	case state.JournalRestored:
		// The journal claims the restore completed, but the file still
		// points at the target: contradiction, no automatic resolution.
		return ambiguous(ReasonRestoreRecordedButTargetActive)
	default:
		return ambiguous(ReasonJournalInvalid)
	}
}

// reconcilePriorObserved decides when the observed selection IS the
// recorded prior. The restore target must never be invented: it is
// exactly the journal's explicit prior selection.
func reconcilePriorObserved(j state.ActivationJournal, facts ProvenFacts) Result {
	switch j.Checkpoint {
	case state.JournalPrepared, state.JournalSwitchIntent:
		// The switch has not been committed: the prior selection being
		// active is simply unchanged.
		if facts.HealthFailed {
			// A proven health failure with no committed switch is a
			// contradiction (health verification happens after the
			// switch); refuse automatic changes.
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
		// moved: the claim is not effective. No health inference, no
		// automatic change.
		return ambiguous(ReasonSelectedNotEffective)
	case state.JournalHealthVerified, state.JournalCleanupPending, state.JournalComplete:
		if facts.HealthFailed {
			// A durable verified-health record contradicted by a proven
			// failure is a contradiction; a human decides.
			return ambiguous(ReasonContradictoryFacts)
		}
		// Verified-health claims about a target that is NOT the active
		// selection are not effective either.
		return ambiguous(ReasonSelectedNotEffective)
	case state.JournalRollbackIntent:
		// The restore is committed and the file proves it already took
		// effect: the executor commits the restored checkpoint. The
		// restore was never inferred from the checkpoint alone.
		return Result{ActionRestorePrior, ReasonRestoreEffective}
	case state.JournalRestored:
		// Journal and file agree the prior is restored: consistent and
		// complete.
		return Result{ActionCompleteConsistent, ReasonRestoredConsistent}
	default:
		return ambiguous(ReasonJournalInvalid)
	}
}
