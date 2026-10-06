package operation

// RestoreSnapshot validates a persisted Record against the model's OWN
// invariants — the exact schema version, a known state, present ordered
// timestamps, complete failure details, and model flags that are consistent
// with the recorded state — and returns the equivalent model Snapshot.
//
// This is the restore boundary for the durable-operation adapter: the
// adapter hands a validated persisted record back to the PURE model and
// then drives every further transition through the model's Apply, so the
// transition table exists exactly once. A record that fails any invariant
// is refused with a MachineInvalidRecord error; it is never silently
// corrected, reset, or treated as absent. An unknown state is refused, not
// coerced into a default.
func RestoreSnapshot(rec Record) (Snapshot, error) {
	if rec.SchemaVersion != RecordSchemaVersion {
		return Snapshot{}, invalidRecord("the record's schema version is not supported")
	}
	if rec.ID <= 0 {
		return Snapshot{}, invalidRecord("the record's operation identifier is not positive")
	}
	if rec.InstallationID == "" || rec.TargetVersion == "" {
		return Snapshot{}, invalidRecord("the record is missing its installation identity or target version")
	}
	if _, known := transitions[rec.State]; !known {
		return Snapshot{}, invalidRecord("the record's state is not a known operation state")
	}
	if rec.CreatedAt.IsZero() || rec.UpdatedAt.IsZero() || rec.UpdatedAt.Before(rec.CreatedAt) {
		return Snapshot{}, invalidRecord("the record's timestamps are missing or out of order")
	}
	if (rec.FailureCode == "") != (rec.FailureUserReason == "") {
		return Snapshot{}, invalidRecord("the record's failure details are incomplete")
	}

	snap := Snapshot{
		ID:                        rec.ID,
		InstallationID:            rec.InstallationID,
		TargetVersion:             rec.TargetVersion,
		State:                     rec.State,
		ActiveWork:                rec.ActiveWork,
		ConsentRecorded:           rec.ConsentRecorded,
		RecoveryRequested:         rec.RecoveryRequested,
		RecoveryNeeded:            rec.RecoveryNeeded,
		ResumeAdmission:           rec.ResumeAdmission,
		HealthVerified:            rec.HealthVerified,
		SuccessWithPendingCleanup: rec.SuccessWithPendingCleanup,
		Failure:                   Failure{Code: MachineCode(rec.FailureCode), UserReason: rec.FailureUserReason},
		HasFailure:                rec.FailureCode != "",
		CreatedAt:                 rec.CreatedAt,
		UpdatedAt:                 rec.UpdatedAt,
	}

	// Persisted-flag invariants: each flag is true only in states the model
	// can actually record it in. These are derived from the transition
	// table, not invented here, so a record contradicting the model is
	// provably not one the model ever produced.
	if snap.HasFailure && !recordedFailureStates[snap.State] {
		return Snapshot{}, invalidRecord("the record carries a failure outside the states the model records failures in")
	}
	if snap.ActiveWork && !recordedActiveWorkStates[snap.State] {
		return Snapshot{}, invalidRecord("the record claims active work outside the states the model holds it in")
	}
	if snap.RecoveryRequested && !protectedFromCancel[snap.State] {
		return Snapshot{}, invalidRecord("the record claims a pending recovery request outside the protected region")
	}
	if snap.RecoveryNeeded && snap.State != StateFailed {
		return Snapshot{}, invalidRecord("the record claims a blocked recovery outside a failed operation")
	}
	if snap.HealthVerified && !recordedHealthVerifiedStates[snap.State] {
		return Snapshot{}, invalidRecord("the record claims verified health outside the states that follow health success")
	}
	// Converse of the health invariant: cleanup and complete are reachable
	// only through health success, so a record claiming either without
	// verified health cannot have been produced by this model.
	if (snap.State == StateCleanup || snap.State == StateComplete) && !snap.HealthVerified {
		return Snapshot{}, invalidRecord("the record claims cleanup or completion without verified health")
	}
	if snap.SuccessWithPendingCleanup && snap.State != StateComplete {
		return Snapshot{}, invalidRecord("the record claims success with pending cleanup outside a complete operation")
	}
	if snap.ResumeAdmission && !recordedAdmissionRestoredStates[snap.State] {
		return Snapshot{}, invalidRecord("the record claims restored admission outside a cancel or defer outcome")
	}
	if snap.ConsentRecorded && !recordedConsentStates[snap.State] {
		return Snapshot{}, invalidRecord("the record claims work-cancel consent in a state the model never records it in")
	}
	return snap, nil
}

// Restore installs a previously persisted operation snapshot as the
// installation's current operation, revalidated through the same
// persisted-record invariants (via its Record projection, so a restored
// snapshot and its persisted form can never disagree). The store's
// identifier sequence is moved past the restored identifier, so a later
// allocation can never collide with or regress below it. A snapshot whose
// record fails validation is refused; the store is left unchanged.
func (s *Store) Restore(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	checked, err := RestoreSnapshot(snap.Record())
	if err != nil {
		return err
	}
	if checked != snap {
		return invalidRecord("the snapshot does not match its persistence projection")
	}
	if snap.ID <= s.lastID {
		return failureErr(MachineInvalidIDSequence,
			"the restored operation identifier must be newer than every identifier already used")
	}
	cp := snap
	s.ops[snap.InstallationID] = &cp
	s.lastID = snap.ID
	return nil
}

// invalidRecord builds the standard persisted-record refusal.
func invalidRecord(userReason string) *Error {
	return failureErr(MachineInvalidRecord, userReason)
}

// Persisted-flag invariant tables. Each set is exactly the collection of
// states the transition table and Apply can record the flag true in; a
// record outside the set cannot have been produced by this model.
var (
	// recordedFailureStates: a failure is recorded entering StateFailed
	// (EventFail, EventHealthFail, backend-failed) and survives the
	// explicit EventRecover into StateRecovered; it is never cleared.
	recordedFailureStates = map[State]bool{StateFailed: true, StateRecovered: true}
	// recordedActiveWorkStates: work becomes active only in StateWaiting
	// and is cleared only by EventIdle, so it can survive into draining,
	// into the pre-activation terminal outcomes reached from waiting or
	// draining (cancel/defer/fail), back through defer/resume into the
	// pre-activation states (ready, downloading, verifying), and — after a
	// pre-activation failure — into recovered via the explicit recover.
	recordedActiveWorkStates = map[State]bool{
		StateWaiting:     true,
		StateReady:       true,
		StateDownloading: true,
		StateVerifying:   true,
		StateDraining:    true,
		StateCancelled:   true,
		StateDeferred:    true,
		StateFailed:      true,
		StateRecovered:   true,
	}
	// recordedHealthVerifiedStates: health verification is recorded only by
	// EventHealthSuccess entering StateCleanup, and survives the edges out
	// of cleanup (complete, failed) — including the explicit recover out of
	// a cleanup failure into recovered.
	recordedHealthVerifiedStates = map[State]bool{
		StateCleanup:   true,
		StateComplete:  true,
		StateFailed:    true,
		StateRecovered: true,
	}
	// recordedAdmissionRestoredStates: the model-level admission-restored
	// flag is set by pre-activation cancel/defer and cleared by resume.
	recordedAdmissionRestoredStates = map[State]bool{
		StateCancelled: true,
		StateDeferred:  true,
	}
	// recordedConsentStates: work-cancel consent is recorded entering
	// StateDraining and is never cleared, so it survives every state
	// reachable from draining.
	recordedConsentStates = map[State]bool{
		StateDraining:    true,
		StateInstalling:  true,
		StateRestarting:  true,
		StateHealthCheck: true,
		StateCleanup:     true,
		StateComplete:    true,
		StateFailed:      true,
		StateRecovered:   true,
		StateCancelled:   true,
	}
)
