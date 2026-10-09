// Package activationtxn is the bounded activation SWITCH transaction: the
// one internal orchestration that connects the durable activation journal
// (the state package's already-prepared, immutable-facts record), the
// launcher-readable selection publisher (the selection package), and the
// pure reconciler (the activation package) into a single lease-guarded,
// intent-before-effect protocol.
//
// Contract: Switch runs the whole critical section — journal re-read,
// durable switch-intent, publication, target readback, durable selected
// acknowledgement — inside ONE exclusion.Handle.GuardUpdateSession on the
// caller's HELD exclusive update lease for EXACTLY req.Directory, which
// must be the STORE's own directory (req.Store.Directory()): the oneshot
// architecture acquires the lease on the store directory, so a request
// naming a different — even a valid private — directory is a wrong
// installation association and is refused before any observation or write.
// The publication runs under the SAME guard through
// selection.PublishGuarded, which proves the live, directory-bound guard
// via the minted, callback-scoped capability (never a caller assertion);
// no nested self-guard exists and no fresh-lease gap opens between intent
// and effect. The target is derived entirely from the journal's immutable
// facts; every reentry is classified by the reconciler first; anything
// ambiguous, mismatched, foreign, or beyond this transaction's checkpoints
// is refused with NOTHING written. No health probing, completion,
// rollback, restore, cleanup, or delete is performed or faked here; the
// journal is left reconcilable on every failure path.
package activationtxn

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/selection"
	"cercano/source/server/internal/updatecoord/state"
)

// Refusal sentinels. Callers match with errors.Is; every refusal names the
// machine-readable reason and performs no write.
var (
	// ErrInvalidRequest: the request itself is malformed — nil store or
	// lease, a non-positive operation ID, a directory that is not an
	// explicit cleaned absolute path, or an exclusion refusal proving the
	// lease (nil, closed, shared, foreign, or replaced) before the
	// transaction ran. Nothing was observed, written, or published.
	ErrInvalidRequest = errors.New("activationtxn: invalid switch request")
	// ErrStaleOperation: the named operation is not the installation's
	// CURRENT operation (or its target no longer matches), so no journal
	// of this transaction may attach to it.
	ErrStaleOperation = errors.New("activationtxn: operation is not the installation's current operation")
	// ErrJournalMissing: the operation has no prepared activation
	// journal. This transaction creates journals nowhere; the caller must
	// prepare the journal first.
	ErrJournalMissing = errors.New("activationtxn: no prepared activation journal for the operation")
	// ErrPriorReceiptMismatch: the caller's prior receipt does not bind
	// to the journal's recorded prior-selection block (or the journal
	// records an explicit prior and no complete receipt was supplied).
	ErrPriorReceiptMismatch = errors.New("activationtxn: prior receipt does not bind to the journal's recorded prior selection")
	// ErrAmbiguousState: the reconciler classified the observed selection
	// against the journal as admitting no automatic action (an effect
	// without intent, a mismatch, a foreign or malformed selection, or a
	// contradiction). The journal is left as it was for reconciliation.
	ErrAmbiguousState = errors.New("activationtxn: observed selection admits no automatic action")
	// ErrReadbackMismatch: the actual selection read back after the
	// publication (or before the acknowledgement) does not match the
	// ENTIRE trusted target descriptor, so the selected checkpoint is
	// NOT acknowledged. The journal stays at switch-intent, which the
	// reconciler can still classify.
	ErrReadbackMismatch = errors.New("activationtxn: selection readback does not match the entire trusted target descriptor")
	// ErrUnsupportedCheckpoint: the journal sits at a checkpoint outside
	// this switch transaction's scope (health verified, cleanup pending,
	// complete, rollback intent, or restored). No health, completion,
	// rollback, or restore is performed or faked here.
	ErrUnsupportedCheckpoint = errors.New("activationtxn: journal checkpoint is outside this switch transaction's scope")
	// ErrCommitUncertain: the publication took effect (its commit was
	// never rolled back) but its durability could not be proven. The
	// journal stays at switch-intent; re-running this transaction
	// re-observes and acknowledges without a second write if the target
	// is proven active.
	ErrCommitUncertain = errors.New("activationtxn: selection commit durability uncertain")
)

// PriorReceipt is the caller's complete, trusted description of the
// recorded prior selection: the full descriptor plus the SHA-256 of the
// prior file's RAW bytes. It is required exactly when the journal records
// an explicit prior selection, and it is bound to the journal on
// installation, prior version, prior generation, and artifact digest; the
// staged identifier and raw digest are proven against the live file by
// the publisher's re-read (see the package documentation).
type PriorReceipt struct {
	Selection activation.Selection
	Digest    string
}

// Request names every authority of one switch transaction explicitly.
type Request struct {
	// Store is the open updater state store holding the operation record
	// and the activation journal.
	Store *state.Store
	// Directory is the explicit, caller-provisioned private publication
	// directory. It is used exactly as given — never defaulted — and it
	// doubles as the installation-association assertion for the lease.
	Directory string
	// Lock is the HELD exclusive update lease. Switch guards the entire
	// transaction on it and acquires no OS lock itself.
	Lock *exclusion.Handle
	// OpID is the operation the journal belongs to; it must be the
	// installation's current operation.
	OpID int64
	// Prior is the complete prior-selection receipt (see PriorReceipt).
	Prior PriorReceipt
}

// Result reports what one Switch call did. It is a record of durable facts
// and protocol steps, never a health measurement: AwaitHealth names the
// protocol's next step (health verification, performed elsewhere), and no
// field claims the target is healthy.
type Result struct {
	// Target is the entire trusted target descriptor derived from the
	// journal's immutable facts.
	Target activation.Selection
	// Checkpoint is the journal checkpoint after this call.
	Checkpoint state.JournalCheckpoint
	// JournalRevision is the journal revision observed after this call.
	JournalRevision int64
	// Published reports whether THIS call performed the selection
	// publication commit (false on the acknowledge-without-rewrite and
	// idempotent reentry paths).
	Published bool
	// Acknowledged reports whether THIS call durably recorded the
	// selected checkpoint after proving the target readback.
	Acknowledged bool
	// AwaitHealth reports that the journal is at selected with the target
	// proven active, so health verification is the next protocol step.
	AwaitHealth bool
	// SelectionDigest is the SHA-256 of the published canonical encoding,
	// set only when THIS call measured it via the publisher; it is the
	// value a future transaction records in its prior receipt.
	SelectionDigest string
}

// Package-private test hooks (never exported): fixed failure points for
// the in-package tests, mirroring the selection package's discipline. All
// run inside the caller-held exclusion guard.
var (
	// testHookJournalLoaded, if non-nil, runs immediately after the
	// journal is loaded inside the guard and before any
	// classification-driven write; a returned error aborts the
	// transaction with nothing written by it.
	testHookJournalLoaded func(j state.ActivationJournal, revision int64) error
	// testHookAfterPublish, if non-nil, runs after a confirmed
	// publication but before the target readback and acknowledgement; a
	// returned error aborts without acknowledging.
	testHookAfterPublish func() error
)

// Switch performs the bounded activation switch transaction on the
// caller's HELD exclusive update lease: re-read the current operation and
// journal inside the lease, durably record switch-intent BEFORE any
// publication (unless it is already recorded or later), publish the target
// selection through the selection package's capability-validated guarded
// entry point, read the actual file back, and durably acknowledge
// selected ONLY when the readback matches the entire trusted target
// descriptor.
//
// The whole critical section runs inside one
// exclusion.Handle.GuardUpdateSession on req.Lock for exactly
// req.Directory, which must be the store's own directory
// (req.Store.Directory()) — the installation association the existing
// oneshot architecture already establishes (store directory == lease
// directory == publication directory). The guard session capability
// minted there is passed to selection.PublishGuarded, which validates it
// against the handle's live state, so the publication is proven — not
// assumed — to run under the caller's live, directory-bound lease; no
// nested self-guard exists (which would deadlock) and Close is pinned
// until the transaction returns.
//
// Every reentry is classified by the reconciler first: prepared or
// switch-intent with the prior (or an explicit first-install absence)
// still observed may (re)publish; switch-intent with the target already
// active is acknowledged WITHOUT a second file write; selected with the
// target active is an idempotent return whose result names health
// verification as the next step. Anything ambiguous, mismatched, foreign,
// unreadable, or beyond this transaction's checkpoints is refused with
// NOTHING written — no fake completion, health claim, rollback, or delete
// ever happens here.
//
// Cancellation and commit uncertainty are always reconcilable: the journal
// names durable intent (prepared, switch-intent, or selected) and the
// caller re-runs Switch or reconciles. The error of a refused guarded use
// (nil, closed, shared, foreign, or replaced lease, or a wrong
// store/directory association) is a typed ErrInvalidRequest wrapping the
// exclusion sentinel, with nothing run or written.
func Switch(ctx context.Context, req Request) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	var (
		ran bool
		res Result
	)
	gerr := req.Lock.GuardUpdateSession(req.Directory, func(guard exclusion.GuardSession) error {
		ran = true
		r, rerr := runGuarded(ctx, req, guard)
		res = r
		return rerr
	})
	if gerr != nil {
		if !ran {
			return Result{}, fmt.Errorf("%w: %w", ErrInvalidRequest, gerr)
		}
		return res, gerr
	}
	return res, nil
}

// validateRequest checks the request's shape. It dereferences nothing:
// the lease is proven only through GuardUpdateSession, which refuses nil
// handles. The store/directory association is proven against the store's
// own recorded directory — never trusted from the caller's string.
func validateRequest(req Request) error {
	if req.Store == nil {
		return fmt.Errorf("%w: store must be named explicitly", ErrInvalidRequest)
	}
	if req.Lock == nil {
		return fmt.Errorf("%w: exclusion handle must be named explicitly", ErrInvalidRequest)
	}
	if req.OpID <= 0 {
		return fmt.Errorf("%w: operation id must be positive", ErrInvalidRequest)
	}
	if strings.ContainsRune(req.Directory, 0) || !filepath.IsAbs(req.Directory) || filepath.Clean(req.Directory) != req.Directory {
		return fmt.Errorf("%w: directory must be an explicit cleaned absolute path", ErrInvalidRequest)
	}
	// Installation association: the publication directory must be the
	// STORE's own directory, exactly as the store recorded it at Open
	// time (the same strict identity the oneshot architecture acquires
	// its lease on). Without this proof a caller holding one
	// installation's lease could publish another store's operation into
	// any other valid private directory.
	if req.Directory != req.Store.Directory() {
		return fmt.Errorf("%w: directory %q is not the store's own state directory %q", ErrInvalidRequest, req.Directory, req.Store.Directory())
	}
	return nil
}

// runGuarded is the transaction body. It runs ONLY inside the caller-held
// exclusion guard for req.Directory, so every read and write below is
// covered by the one lease; guard is the capability minted by that
// GuardUpdateSession call and is validated again by the publisher.
func runGuarded(ctx context.Context, req Request, guard exclusion.GuardSession) (Result, error) {
	// Authority: the named operation must be the installation's CURRENT
	// operation, re-read inside the lease — never a stale or superseded
	// one, and never a writer that pre-dates the lease.
	snap, found, err := state.NewAdapter(req.Store).Snapshot(ctx)
	if err != nil {
		return Result{}, err
	}
	if !found || snap.ID != req.OpID {
		return Result{}, fmt.Errorf("%w: requested operation %d", ErrStaleOperation, req.OpID)
	}

	// The already-prepared journal, re-read inside the lease. A missing
	// journal is a refusal: this transaction creates journals nowhere.
	journal, revision, err := req.Store.LoadActivationJournal(ctx, req.OpID)
	if err != nil {
		if errors.Is(err, state.ErrRecordNotFound) {
			return Result{}, fmt.Errorf("%w: operation %d: %w", ErrJournalMissing, req.OpID, err)
		}
		return Result{}, err
	}
	if err := state.ValidateActivationJournal(journal, req.Store.InstallID()); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrAmbiguousState, err)
	}
	// Defense in depth: the load validates the journal against the store
	// installation; the target must also still match the current
	// operation's target (SaveActivationJournal enforces this on every
	// write, but a mismatch must be refused BEFORE any file effect).
	if journal.TargetVersion != snap.TargetVersion {
		return Result{}, fmt.Errorf("%w: journal target %q does not match operation %d target %q", ErrStaleOperation, journal.TargetVersion, snap.ID, snap.TargetVersion)
	}
	if testHookJournalLoaded != nil {
		if herr := testHookJournalLoaded(journal, revision); herr != nil {
			return Result{}, herr
		}
	}

	// The target is derived ENTIRELY from the journal's immutable facts;
	// no arbitrary path, version, or digest is ever accepted from the
	// caller.
	target, err := targetSelection(journal)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrAmbiguousState, err)
	}

	// One observation of the selection file, taken inside the same lease
	// and classified exactly as the reconciler requires.
	observed := activation.Observe(activation.ReadSelection(filepath.Join(req.Directory, selection.FileName)))
	decision := activation.Reconcile(journal, observed, activation.ProvenFacts{})

	switch journal.Checkpoint {
	case state.JournalPrepared:
		if decision.Action != activation.ActionBeforeSwitch {
			return Result{}, refuseDecision(decision)
		}
		expected, err := expectedPrior(req, journal)
		if err != nil {
			return Result{}, err
		}
		// Durable intent BEFORE any effect.
		intent, intentRev, err := advance(ctx, req, journal, revision, state.JournalSwitchIntent)
		if err != nil {
			return Result{}, err
		}
		return publishAndAcknowledge(ctx, req, guard, intent, intentRev, expected, target)

	case state.JournalSwitchIntent:
		switch decision.Action {
		case activation.ActionTargetAwaitHealth:
			// The effect already happened; acknowledge WITHOUT a second
			// file write, on the readback observed under this lease.
			return acknowledge(ctx, req, journal, revision, target, observed, false, "")
		case activation.ActionBeforeSwitch:
			// The durable intent is already recorded; retry the
			// publication (the prior or explicit absence is still
			// observed) and then acknowledge.
			expected, err := expectedPrior(req, journal)
			if err != nil {
				return Result{}, err
			}
			return publishAndAcknowledge(ctx, req, guard, journal, revision, expected, target)
		default:
			return Result{}, refuseDecision(decision)
		}

	case state.JournalSelected:
		if decision.Action == activation.ActionTargetAwaitHealth {
			// Idempotent reentry: nothing to write. The result names the
			// next protocol step; it never claims health.
			return Result{
				Target:          target,
				Checkpoint:      state.JournalSelected,
				JournalRevision: revision,
				AwaitHealth:     true,
			}, nil
		}
		return Result{}, refuseDecision(decision)

	default:
		// Health verified, cleanup pending, complete, rollback intent,
		// or restored: outside this transaction's scope. No health,
		// completion, rollback, or restore is performed or faked.
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedCheckpoint, journal.Checkpoint)
	}
}

// targetSelection derives the entire trusted target descriptor from the
// journal's immutable facts: the successor generation of the recorded
// prior generation, the journal's target version, staged identifier, and
// verified artifact digest. A prior generation at the int64 bound has no
// successor and is refused.
func targetSelection(j state.ActivationJournal) (activation.Selection, error) {
	if j.PriorSelectionGeneration == math.MaxInt64 {
		return activation.Selection{}, errors.New("recorded prior generation has no successor")
	}
	target := activation.Selection{
		SchemaVersion:          activation.SelectionSchemaVersion,
		InstallID:              j.InstallID,
		Generation:             j.PriorSelectionGeneration + 1,
		SelectedVersion:        j.TargetVersion,
		StagedVersionDir:       j.StagedVersionDir,
		VerifiedArtifactSHA256: j.VerifiedArtifactSHA256,
	}
	if err := target.Validate(); err != nil {
		return activation.Selection{}, err
	}
	return target, nil
}

// expectedPrior builds the publication's required previous state from the
// journal's prior-selection block and the caller's complete prior receipt.
// The receipt is bound to the journal field by field; its staged
// identifier and raw digest are proven against the live file by the
// publisher's own re-read under the same guard (see the package
// documentation for the binding model).
func expectedPrior(req Request, journal state.ActivationJournal) (selection.Expected, error) {
	if journal.PriorSelectedVersion == "" {
		// Explicit first install: the journal records NO prior, so no
		// receipt may be supplied, and the required previous state is a
		// proven absence — observed under this lease and re-proven by
		// the publisher immediately before the commit.
		if req.Prior != (PriorReceipt{}) {
			return selection.Expected{}, fmt.Errorf("%w: journal records no prior selection but a prior receipt was supplied", ErrPriorReceiptMismatch)
		}
		return selection.Expected{Absent: true}, nil
	}
	if req.Prior == (PriorReceipt{}) {
		return selection.Expected{}, fmt.Errorf("%w: journal records prior selection %q but no complete prior receipt was supplied", ErrPriorReceiptMismatch, journal.PriorSelectedVersion)
	}
	r := req.Prior
	if err := r.Selection.Validate(); err != nil {
		return selection.Expected{}, fmt.Errorf("%w: %v", ErrPriorReceiptMismatch, err)
	}
	if !state.IsLowerHexSHA256(r.Digest) {
		return selection.Expected{}, fmt.Errorf("%w: receipt digest must be a 64-character lowercase hex SHA-256", ErrPriorReceiptMismatch)
	}
	if r.Selection.InstallID != journal.InstallID ||
		r.Selection.SelectedVersion != journal.PriorSelectedVersion ||
		r.Selection.Generation != journal.PriorSelectionGeneration ||
		r.Selection.VerifiedArtifactSHA256 != journal.PriorSelectionDigest {
		return selection.Expected{}, fmt.Errorf("%w: receipt names %q generation %d, journal records prior %q generation %d", ErrPriorReceiptMismatch, r.Selection.SelectedVersion, r.Selection.Generation, journal.PriorSelectedVersion, journal.PriorSelectionGeneration)
	}
	return selection.Expected{Selection: r.Selection, Digest: r.Digest}, nil
}

// publishAndAcknowledge publishes the target under the SAME guard (never a
// nested self-guard): the minted guard capability is re-validated by
// selection.PublishGuarded against the handle's live state, proving the
// publication runs under the caller's live, directory-bound lease. The
// journal is already at switch-intent, so every failure below leaves a
// reconcilable journal and never a fake completion.
func publishAndAcknowledge(ctx context.Context, req Request, guard exclusion.GuardSession, journal state.ActivationJournal, revision int64, expected selection.Expected, target activation.Selection) (Result, error) {
	pub, perr := selection.PublishGuarded(ctx, guard, req.Directory, expected, target)
	if perr != nil {
		// Pre-commit refusal: the destination is unchanged and the journal
		// stays at switch-intent — the prior (or absence) is still
		// observed and the publication may be retried.
		return Result{}, fmt.Errorf("activationtxn: publish under switch-intent: %w", perr)
	}
	if pub.State == selection.CommitDurabilityUncertain {
		// The commit took effect and was never rolled back, but its
		// durability is unproven. No acknowledgement: the journal stays
		// at switch-intent and reentry re-observes (and acknowledges
		// without a second write once the target is proven active).
		return Result{}, fmt.Errorf("%w: %s", ErrCommitUncertain, pub.Detail)
	}
	if testHookAfterPublish != nil {
		if herr := testHookAfterPublish(); herr != nil {
			return Result{}, herr
		}
	}
	// The acknowledgement requires the ACTUAL file, read back under this
	// lease, to match the ENTIRE trusted target descriptor.
	readback := activation.Observe(activation.ReadSelection(filepath.Join(req.Directory, selection.FileName)))
	return acknowledge(ctx, req, journal, revision, target, readback, true, pub.Digest)
}

// acknowledge durably records the selected checkpoint ONLY after the
// observed readback matches the entire trusted target descriptor.
func acknowledge(ctx context.Context, req Request, journal state.ActivationJournal, revision int64, target activation.Selection, readback activation.Observed, published bool, digest string) (Result, error) {
	if readback.State != activation.ObservedPresent || readback.Selection == nil || *readback.Selection != target {
		return Result{}, fmt.Errorf("%w: readback is %s", ErrReadbackMismatch, readback.State)
	}
	_, rev, err := advance(ctx, req, journal, revision, state.JournalSelected)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Target:          target,
		Checkpoint:      state.JournalSelected,
		JournalRevision: rev,
		Published:       published,
		Acknowledged:    true,
		AwaitHealth:     true,
		SelectionDigest: digest,
	}, nil
}

// advance saves the journal's next checkpoint under a compare-and-swap on
// the revision loaded inside this same guard. The store enforces
// operation currency, target match, immutable facts, and checkpoint
// legality inside one transaction; a refused advance writes nothing.
func advance(ctx context.Context, req Request, journal state.ActivationJournal, expectedRevision int64, to state.JournalCheckpoint) (state.ActivationJournal, int64, error) {
	next := journal
	next.Checkpoint = to
	rev, err := req.Store.SaveActivationJournal(ctx, expectedRevision, next)
	if err != nil {
		return state.ActivationJournal{}, 0, fmt.Errorf("activationtxn: record %q checkpoint: %w", to, err)
	}
	return next, rev, nil
}

// refuseDecision reports an ambiguous or refused reconciler classification
// with its machine-readable action and reason.
func refuseDecision(d activation.Result) error {
	return fmt.Errorf("%w: %s: %s", ErrAmbiguousState, d.Action, d.Reason)
}
