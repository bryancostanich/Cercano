// Package selection is the bounded, single-purpose publication primitive
// for the launcher-readable activation selection file.
//
// Scope discipline. This package publishes EXACTLY ONE thing: the fixed
// selection file name inside one explicit, pre-provisioned, privdir-
// verified private directory. The directory is caller-supplied and used
// exactly as given; there is no default or live location, no journal
// advancement, no live-version rename, no process-health probing, no
// agent stop, and no cleanup of anything beyond this operation's own
// staging tempfiles. Reading and reconciling selections stays in the
// activation package.
//
// Caller-held exclusion. Publication is safe only against COOPERATING
// writers — the ones that hold the installation exclusion lock the
// caller must pass in. The handle is proven by the exclusion guarded-use
// API (exclusion.Handle.GuardUpdate): it must be a live, exclusive
// Update-mode lease acquired for exactly this publication directory, and
// the guard prevents Close from releasing the lease until the whole
// observe-stage-commit sequence has returned or panicked. The directory
// binding is proven from what Acquire recorded — never faked from a nil
// handle. This primitive takes no OS lock itself and makes NO
// filesystem-CAS claim against uncooperative writers: the
// re-read-before-publish check detects cooperating conflicts, and the
// create path fails rather than clobbering an unexpected file, but an
// uncooperative writer racing inside the publish window is outside the
// model and is never silently tolerated by a false "atomic" claim.
//
// Commit states. Everything before the platform commit returns a typed
// error with NOTHING changed (owned staging cleaned by identity). After
// the commit the operation NEVER rolls back silently: a durability sync
// or confirmation failure is reported as COMMITTED_BUT_DURABILITY_
// UNCERTAIN (Result.State = CommitDurabilityUncertain), and the caller
// re-observes and decides. Durability is measured to the sync/flush
// boundary only; no power-loss guarantee is claimed or testable here —
// unit tests observe the protocol, not a crash.
package selection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/privdir"
	"cercano/source/server/internal/updatecoord/state"
)

// FileName is the ONLY file name this package publishes or reads, joined
// to the caller's explicit directory. A raw arbitrary destination path is
// never accepted.
const FileName = "selection.json"

// stagingPrefix names this operation's private staging tempfiles inside
// the same directory. Cleanup matches ONLY names this operation created
// and proves by inode identity; RemoveAll is never used.
const stagingPrefix = ".selection-publish-"

// maxSelectionBytes mirrors the activation reader's payload bound; the
// authoritative enforcement lives in activation.ParseSelection, which
// the canonical self-check below re-runs.
const maxSelectionBytes = 4 * 1024

var (
	// ErrInvalidRequest: the request itself is malformed (bad path,
	// nil exclusion handle, invalid or inconsistent expected/new
	// descriptors). Nothing was observed, changed, or staged.
	ErrInvalidRequest = errors.New("selection: invalid publish request")
	// ErrUnsafeDirectory: the explicit directory is not an existing,
	// privdir-verified private directory. This package provisions
	// NOTHING; an absent or unsafe root is refused unchanged.
	ErrUnsafeDirectory = errors.New("selection: publication directory is not a verified private directory")
	// ErrConflict: the re-read before publish did not match the
	// expectation (a cooperating writer moved first, or the expectation
	// was stale). Pre-commit refusal; the destination is unchanged.
	ErrConflict = errors.New("selection: observed selection does not match the expected previous selection")
	// ErrDestinationUnsafe: the destination entry exists but is not a
	// regular non-link file (symlink, directory, device, or identity
	// changed mid-read). Pre-commit refusal; nothing is changed and no
	// symlink is followed or rewritten.
	ErrDestinationUnsafe = errors.New("selection: destination entry is not a regular non-link file")
	// ErrCanceled: the caller's context ended before the commit.
	ErrCanceled = errors.New("selection: publish canceled before commit")
	// ErrStagingRetained: a staging tempfile this operation created
	// could not be proven-safe to remove and was LEFT IN PLACE rather
	// than removed without proof.
	ErrStagingRetained = errors.New("selection: staging cleanup incomplete; retained temporary file")
	// ErrUnsupportedPlatform: no approved publication primitive exists
	// for the build platform.
	ErrUnsupportedPlatform = errors.New("selection: platform has no approved publication primitive")
)

// CommitState is the outcome of one publish. It distinguishes
// COMMITTED-but-durability-uncertain from every pre-commit failure, which
// is a plain error with nothing committed.
type CommitState int

const (
	// CommitConfirmed: the platform commit succeeded, the directory
	// sync/flush succeeded, and a re-read confirmed the observed value
	// is exactly the canonical encoding this operation wrote.
	CommitConfirmed CommitState = iota + 1
	// CommitDurabilityUncertain: the commit took effect (the rename/
	// move succeeded — never rolled back) but the durability sync or
	// the confirmation re-read failed. The caller must re-observe and
	// decide; treat the selection as committed-but-unproven.
	CommitDurabilityUncertain
)

func (c CommitState) String() string {
	switch c {
	case CommitConfirmed:
		return "committed-confirmed"
	case CommitDurabilityUncertain:
		return "committed-but-durability-uncertain"
	default:
		return "unknown"
	}
}

// Expected is the required previous state, proven by the caller: either
// an explicitly proven ABSENCE (first creation) or the exact previous
// descriptor together with the SHA-256 of its raw file bytes. Exactly
// one form may be given.
type Expected struct {
	// Absent must be true ONLY when the caller has explicitly proven
	// absence (for example via activation.ReadSelection returning
	// activation.ErrSelectionAbsent) under the same exclusion hold.
	Absent bool
	// Selection is the expected current descriptor (re-validated
	// here); Digest is the SHA-256 of its raw file bytes.
	Selection activation.Selection
	Digest    string
}

// Result reports the commit outcome. Digest is always the SHA-256 of the
// canonical encoding of the published selection — the exact value the
// caller should record for the NEXT publish's Expected.Digest.
type Result struct {
	State        CommitState
	Digest       string
	Detail       string   // set only when State is CommitDurabilityUncertain
	RetainedTemp []string // staging tempfile basenames left in place (never silently removed without proof)
}

// Package-private test hooks (never exported): fixed failure points for
// the in-package tests. Both run inside the caller-held exclusion and
// touch only the operation's own staging/destination.
var (
	// testHookStagingOpen, if non-nil, runs immediately after the staging
	// tempfile is created, while the owned handle is STILL OPEN; a
	// returned error is an early pre-close failure proving the handle is
	// closed and the owned file cleaned on every such path.
	testHookStagingOpen func(f *os.File) error
	// testHookBeforeCommit, if non-nil, runs after staging and the
	// re-read check but BEFORE the pre-commit recheck and the platform
	// commit; a returned error is a pre-commit failure: staging is
	// cleaned, nothing is committed.
	testHookBeforeCommit func() error
	// testHookAfterCommit, if non-nil, runs immediately AFTER the
	// platform commit succeeded; a returned error forces the
	// durability-uncertain state without any rollback.
	testHookAfterCommit func() error
)

// Publish atomically replaces (or first-creates) the fixed selection file
// in dir with the fully validated next descriptor, requiring that the
// currently observed file exactly matches expected (or is explicitly
// proven absent) on a re-read taken immediately before staging.
//
// The caller must hold the installation exclusion lease across the
// observe-expect-publish sequence, and that hold is PROVEN, not assumed:
// the whole critical section — privdir verification, re-read, staging,
// commit, durability confirmation — runs inside exclusion.Handle
// .GuardUpdate, which refuses a nil or zero-value handle, a closed or
// closing handle, a shared Launch lease, or a handle acquired for a
// different directory (each refusal is a typed ErrInvalidRequest
// wrapping the exclusion sentinel, with nothing observed, staged, or
// committed), and which blocks Close until the section has returned or
// panicked, so the OS lease cannot be released mid-flight. This function
// takes no OS lock itself and makes no filesystem-CAS claim against
// uncooperative writers: the re-read-before-publish check detects
// cooperating conflicts, and the create path fails rather than clobbering
// an unexpected file, but an uncooperative writer racing inside the
// publish window is outside the model and is never silently tolerated
// by a false "atomic" claim.
//
// dir must be an already-provisioned private directory: it is classified
// by privdir.VerifyExisting — the verify-only primitive that refuses an
// absent path (never creating it) and refuses an unsafe policy unchanged
// — so this package provisions nothing, never chmods, and never repairs
// an unsafe ACL. The directory argument of the guarded use doubles as
// the installation-association assertion: it is caller-supplied, never
// defaulted, and the guard proves the lease was acquired for exactly
// that directory.
//
// Protocol (all inside the guard): classify the directory with
// privdir.VerifyExisting → re-read the destination and refuse on any
// conflict → stage the canonical strict-JSON encoding in a unique O_EXCL
// private tempfile (write/sync/close) → platform commit (Unix rename +
// directory fsync, or on Windows MoveFileEx with
// MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH — create uses the
// no-clobber variants; see the platform files for the documented flush
// limits) → re-read to confirm the observed value.
//
// Named results let the staging-cleanup defer JOIN a retention failure
// (ErrStagingRetained) into whatever pre-commit error is being returned:
// an unprovable staging file is never removed silently, and its retention
// is never swallowed.
func Publish(ctx context.Context, dir string, lock *exclusion.Handle, expected Expected, next activation.Selection) (Result, error) {
	var zero Result
	if ctx == nil {
		return zero, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrCanceled, err)
	}
	if strings.ContainsRune(dir, 0) || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return zero, fmt.Errorf("%w: directory must be an explicit cleaned absolute path", ErrInvalidRequest)
	}
	if err := next.Validate(); err != nil {
		return zero, fmt.Errorf("%w: new selection: %w", ErrInvalidRequest, err)
	}
	if err := validateExpected(expected, next); err != nil {
		return zero, err
	}

	// Guarded use. lock is only dereferenced through GuardUpdate, which
	// refuses (rather than dereferences) a nil handle; every refusal
	// there is a malformed request, and the callback's own error is
	// propagated unchanged. `entered` distinguishes a guard refusal from
	// a callback error so the typed publish errors are never re-wrapped.
	var (
		entered bool
		res     Result
	)
	gerr := lock.GuardUpdate(dir, func() error {
		entered = true
		r, perr := publishGuarded(ctx, dir, expected, next)
		res = r
		return perr
	})
	if gerr != nil {
		if !entered {
			return zero, fmt.Errorf("%w: %w", ErrInvalidRequest, gerr)
		}
		return res, gerr
	}
	return res, nil
}

// publishGuarded is the whole observe-stage-commit critical section. It
// runs ONLY inside exclusion.Handle.GuardUpdate, which has already proven
// the caller's live exclusive Update lease for exactly dir and pinned
// Close until it returns, and it starts by re-classifying dir with the
// verify-only privdir primitive so nothing is ever provisioned here.
func publishGuarded(ctx context.Context, dir string, expected Expected, next activation.Selection) (res Result, err error) {
	var zero Result
	dest := filepath.Join(dir, FileName)

	// Private-directory classification, verify-only: privdir.VerifyExisting
	// refuses an absent directory (never creating it — this package
	// provisions nothing, the caller provisions) and refuses an unsafe
	// existing policy unchanged (never chmodding or repairing it), all
	// through one call with no exists-then-provision race window.
	if err := privdir.VerifyExisting(dir); err != nil {
		return zero, fmt.Errorf("%w: %w", ErrUnsafeDirectory, err)
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrCanceled, err)
	}

	// Re-read before publish: conflict refuses, destination unchanged.
	data, absent, err := readSelectionFile(dest)
	if err != nil {
		return zero, err
	}
	if expected.Absent {
		if !absent {
			return zero, fmt.Errorf("%w: an entry exists where proven absence was required", ErrConflict)
		}
	} else {
		if absent {
			return zero, fmt.Errorf("%w: selection is absent but a previous descriptor was expected", ErrConflict)
		}
		if len(data) > maxSelectionBytes {
			return zero, fmt.Errorf("%w: %w: exceeds %d bytes", ErrConflict, activation.ErrSelectionMalformed, maxSelectionBytes)
		}
		current, perr := activation.ParseSelection(data)
		if perr != nil {
			return zero, fmt.Errorf("%w: %w: %w", ErrConflict, activation.ErrSelectionMalformed, perr)
		}
		if current != expected.Selection {
			return zero, fmt.Errorf("%w: observed descriptor differs from the expected previous selection", ErrConflict)
		}
		if digestOf(data) != expected.Digest {
			return zero, fmt.Errorf("%w: observed bytes differ from the expected digest", ErrConflict)
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrCanceled, err)
	}

	// Canonical strict JSON: fixed field order, then a full re-parse
	// self-check so only bytes this package would accept are written.
	canonical, err := json.Marshal(next)
	if err != nil {
		return zero, fmt.Errorf("%w: encode selection: %w", ErrInvalidRequest, err)
	}
	roundTrip, err := activation.ParseSelection(canonical)
	if err != nil || roundTrip != next {
		return zero, fmt.Errorf("%w: canonical encoding failed its own validation", ErrInvalidRequest)
	}

	// Private staging: unique O_EXCL tempfile in the same directory,
	// write, sync, then PIN the identity from the still-open handle and
	// close. Every early return below leaves the handle open; the
	// deferred cleanup closes it on every path (the owned handle must be
	// closed before the delete, which Windows in particular requires) and
	// removes the file ONLY when identity proves ownership.
	tmp, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		return zero, fmt.Errorf("selection: create staging tempfile: %w", err)
	}
	// staged is pinned while the owned handle is valid and is re-proved
	// after close (an inode-ABA guard: a file that merely reuses the
	// staging NAME is never treated as this operation's staging file).
	var staged os.FileInfo
	stagingClosed := false
	committed := false
	defer func() {
		if committed {
			return
		}
		identity := staged
		if !stagingClosed {
			// The handle is still open (an early path): take the exact
			// current identity from it, then close before deleting.
			if info, serr := tmp.Stat(); serr == nil {
				identity = info
			}
			stagingClosed = true
			_ = tmp.Close() //nolint:errcheck // the cleanup error is what gets reported
		}
		if rerr := cleanupOwnedTemp(tmp.Name(), identity); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()
	if testHookStagingOpen != nil {
		if herr := testHookStagingOpen(tmp); herr != nil {
			return zero, fmt.Errorf("selection: staging failure point: %w", herr)
		}
	}
	if err := tmp.Chmod(0o600); err != nil {
		return zero, fmt.Errorf("selection: staging permissions: %w", err)
	}
	if _, err := tmp.Write(canonical); err != nil {
		return zero, fmt.Errorf("selection: write staging tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return zero, fmt.Errorf("selection: sync staging tempfile: %w", err)
	}
	// Identity is captured AFTER the write completes, so size/mode prove
	// the staged bytes are the ones to clean up or publish.
	staged, err = tmp.Stat()
	if err != nil {
		return zero, fmt.Errorf("selection: staging identity: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return zero, fmt.Errorf("selection: close staging tempfile: %w", err)
	}
	stagingClosed = true

	if testHookBeforeCommit != nil {
		if herr := testHookBeforeCommit(); herr != nil {
			return zero, fmt.Errorf("selection: pre-commit failure point: %w", herr)
		}
	}

	// Pre-commit recheck. The hook (and the elapsed staging window) ran
	// arbitrary code, so NOTHING observed earlier still proves anything
	// without re-proof: the context must still be live, the staging file
	// must still be exactly this operation's file holding exactly the
	// canonical bytes (a replaced or rewritten staging file must not be
	// published), and the destination must still match the expectation.
	// The cooperative contract is explicit: same-user writers inside the
	// exclusion are trusted to be THIS protocol; anything else is
	// refused, not published.
	if rerr := recheckBeforeCommit(ctx, dir, tmp.Name(), staged, canonical, expected); rerr != nil {
		return zero, rerr
	}

	// Platform commit. After this succeeds the selection IS published;
	// every later failure becomes durability-uncertain, never a
	// rollback.
	if expected.Absent {
		err = commitCreateSelection(tmp.Name(), dest)
		if err != nil {
			// A link/move that reports failure only AFTER taking effect
			// must not be misreported as not-committed: if the
			// destination is already this operation's staged file, the
			// commit stands and flows through the committed path.
			if stagingCommitted(dest, staged) {
				committed = true
			} else {
				return zero, err // pre-commit failure: nothing provably committed; staging cleaned by the deferred cleanup
			}
		}
	} else {
		err = commitReplaceSelection(tmp.Name(), dest)
		if err != nil {
			return zero, err // pre-commit failure: nothing provably committed; staging cleaned by the deferred cleanup
		}
	}
	committed = true

	var retained []string
	if expected.Absent {
		// Create paths that publish via link (Unix) leave the staging
		// NAME in place; remove exactly this operation's file, by
		// identity, never by pattern or recursion. Windows moves the
		// name; its remove is a no-op. A removal failure is visible in
		// the typed Result (RetainedTemp) and NEVER rolls the commit
		// back or turns it into an error.
		if rerr := updateStagingAfterCommit(dir, tmp.Name(), staged); rerr != nil {
			retained = append(retained, filepath.Base(tmp.Name()))
		}
	}

	var post []error
	if testHookAfterCommit != nil {
		if herr := testHookAfterCommit(); herr != nil {
			post = append(post, herr)
		}
	}
	if serr := syncSelectionDirectory(dir); serr != nil {
		post = append(post, serr)
	}
	if cerr := confirmPublished(dest, canonical, next); cerr != nil {
		post = append(post, cerr)
	}
	published := digestOf(canonical)
	if len(post) > 0 {
		return Result{
			State:        CommitDurabilityUncertain,
			Digest:       published,
			Detail:       errors.Join(post...).Error(),
			RetainedTemp: retained,
		}, nil
	}
	return Result{State: CommitConfirmed, Digest: published, RetainedTemp: retained}, nil
}

// validateExpected enforces exactly one expectation form and the strictly
// increasing generation / same-installation discipline between the
// expected previous and the new selection. The journal's exact
// successor-generation binding is the reconciler's business and is not
// re-imposed here.
func validateExpected(expected Expected, next activation.Selection) error {
	if expected.Absent {
		if expected.Selection != (activation.Selection{}) || expected.Digest != "" {
			return fmt.Errorf("%w: absent expectation must not also name a previous descriptor or digest", ErrInvalidRequest)
		}
		return nil
	}
	if err := expected.Selection.Validate(); err != nil {
		return fmt.Errorf("%w: expected previous selection: %w", ErrInvalidRequest, err)
	}
	if !state.IsLowerHexSHA256(expected.Digest) {
		return fmt.Errorf("%w: expected digest must be a 64-character lowercase hex SHA-256", ErrInvalidRequest)
	}
	if next.InstallID != expected.Selection.InstallID {
		return fmt.Errorf("%w: new selection names installation %q but the expected previous selection names %q", ErrInvalidRequest, next.InstallID, expected.Selection.InstallID)
	}
	if next.Generation <= expected.Selection.Generation {
		return fmt.Errorf("%w: new generation %d does not exceed the expected previous generation %d", ErrInvalidRequest, next.Generation, expected.Selection.Generation)
	}
	return nil
}

// readSelectionFile performs the single-shot, identity-disciplined read of
// the destination: Lstat before (absent is a first-class result), regular
// non-link check, open, handle identity check, bounded read, and Lstat
// after. It deliberately mirrors activation.ReadSelection because the
// digest comparison needs the exact raw bytes from the SAME single read
// that classified the entry; two separate reads would race each other.
func readSelectionFile(dest string) (data []byte, absent bool, err error) {
	before, err := os.Lstat(dest)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("selection: inspect destination: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%w: %w", ErrDestinationUnsafe, activation.ErrSelectionUnreadable)
	}
	f, err := os.Open(dest)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("%w: %w", ErrDestinationUnsafe, activation.ErrSelectionUnreadable)
		}
		return nil, false, fmt.Errorf("selection: open destination: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only; the caller sees the parse result
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, false, fmt.Errorf("%w: %w", ErrDestinationUnsafe, activation.ErrSelectionUnreadable)
	}
	data, err = io.ReadAll(io.LimitReader(f, maxSelectionBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("selection: read destination: %w", err)
	}
	after, err := os.Lstat(dest)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, false, fmt.Errorf("%w: %w", ErrDestinationUnsafe, activation.ErrSelectionUnreadable)
	}
	return data, false, nil
}

// confirmPublished re-reads the destination and proves the OBSERVED value
// is exactly the canonical encoding just committed. A failure here is
// reported (durability-uncertain detail), never rolled back.
func confirmPublished(dest string, canonical []byte, next activation.Selection) error {
	data, absent, err := readSelectionFile(dest)
	if err != nil {
		return fmt.Errorf("selection: confirm published selection: %w", err)
	}
	if absent {
		return errors.New("selection: confirm published selection: destination absent after commit")
	}
	if !bytes.Equal(data, canonical) {
		return errors.New("selection: confirm published selection: observed bytes differ from the published encoding")
	}
	observed, perr := activation.ParseSelection(data)
	if perr != nil || observed != next {
		return fmt.Errorf("selection: confirm published selection: observed descriptor is not the published selection")
	}
	return nil
}

// recheckBeforeCommit re-proves, immediately before the platform commit
// and after the pre-commit hook, that every binding observed earlier
// still holds: the context is live, the staging file is still this
// operation's file holding exactly the canonical bytes, and the
// destination still matches the expectation. A hook that swapped or
// rewrote the staging file, or that changed the destination, must not
// publish unknown state.
func recheckBeforeCommit(ctx context.Context, dir, tmp string, staged os.FileInfo, canonical []byte, expected Expected) error {
	if cerr := ctx.Err(); cerr != nil {
		return errors.Join(ErrCanceled, cerr)
	}
	dest := filepath.Join(dir, FileName)
	if serr := stagingIdentityHolds(tmp, staged, canonical); serr != nil {
		return serr
	}
	return expectedStateHolds(dest, expected)
}

// stagingIdentityHolds re-proves the pinned staging identity (inode, size,
// mode) and that the file still holds exactly the canonical bytes; either
// mismatch is a retention-grade refusal, never a publish.
func stagingIdentityHolds(tmp string, staged os.FileInfo, canonical []byte) error {
	info, err := os.Lstat(tmp)
	if err != nil {
		return fmt.Errorf("selection: staging identity recheck: %w", err)
	}
	if !info.Mode().IsRegular() || !sameStagingIdentity(info, staged) {
		return fmt.Errorf("%w: %s is not the staged file this operation wrote", ErrStagingRetained, filepath.Base(tmp))
	}
	got, rerr := os.ReadFile(tmp)
	if rerr != nil {
		return fmt.Errorf("selection: staging content recheck: %w", rerr)
	}
	if !bytes.Equal(got, canonical) {
		return fmt.Errorf("%w: staged bytes differ from the canonical encoding", ErrStagingRetained)
	}
	return nil
}

// expectedStateHolds re-proves the destination still matches the expected
// previous state immediately before the commit (absent, or the exact
// previous descriptor and raw-byte digest).
func expectedStateHolds(dest string, expected Expected) error {
	data, absent, err := readSelectionFile(dest)
	if err != nil {
		return err
	}
	if expected.Absent {
		if !absent {
			return fmt.Errorf("%w: destination appeared unexpectedly before creation", ErrConflict)
		}
		return nil
	}
	if absent {
		return fmt.Errorf("%w: selection is absent but a previous descriptor was expected", ErrConflict)
	}
	if len(data) > maxSelectionBytes {
		return fmt.Errorf("%w: %w: exceeds %d bytes", ErrConflict, activation.ErrSelectionMalformed, maxSelectionBytes)
	}
	current, perr := activation.ParseSelection(data)
	if perr != nil {
		return fmt.Errorf("%w: %w: %w", ErrConflict, activation.ErrSelectionMalformed, perr)
	}
	if current != expected.Selection {
		return fmt.Errorf("%w: observed descriptor differs from the expected previous selection", ErrConflict)
	}
	if digestOf(data) != expected.Digest {
		return fmt.Errorf("%w: observed bytes differ from the expected digest", ErrConflict)
	}
	return nil
}

// stagingCommitted reports whether the destination is already this
// operation's staged file, proving a commit that reported failure actually
// took effect (for example a link that landed before an error was
// surfaced); such a commit must not be misreported as not-committed.
func stagingCommitted(dest string, staged os.FileInfo) bool {
	if staged == nil {
		return false
	}
	info, err := os.Lstat(dest)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return os.SameFile(info, staged)
}

// cleanupOwnedTemp removes a pre-commit staging tempfile only after
// proving by inode identity that it is the file this operation created.
// Anything unprovable is retained and reported; RemoveAll is never used.
func cleanupOwnedTemp(path string, identity os.FileInfo) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%w: %s: %w", ErrStagingRetained, filepath.Base(path), err)
	}
	if identity != nil && !sameStagingIdentity(info, identity) {
		return fmt.Errorf("%w: %s is not the staged file this operation created", ErrStagingRetained, filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrStagingRetained, filepath.Base(path))
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrStagingRetained, filepath.Base(path), err)
	}
	return nil
}

// updateStagingAfterCommit removes the staging NAME left behind by the
// link-based create commit, but ONLY after re-proving the pinned identity
// against the current entry — the destination is already committed to the
// staged content, so the removal must never touch anything else, and any
// changed entry (an inode-ABA reuse of the staging name) stays in place
// and is reported instead.
func updateStagingAfterCommit(dir, tmp string, identity os.FileInfo) error {
	// The publication directory must still be the verified private root
	// this operation staged in; a swapped root is never touched. This is
	// the verify-only classification (an absent or unsafe directory is
	// refused, never provisioned or repaired), not Ensure.
	if err := privdir.VerifyExisting(dir); err != nil {
		return fmt.Errorf("%w: publication directory no longer provably private", ErrStagingRetained)
	}
	return cleanupOwnedTemp(tmp, identity)
}

func sameStagingIdentity(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
