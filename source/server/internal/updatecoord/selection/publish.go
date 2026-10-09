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
// caller must pass in. This primitive takes no lock itself and makes NO
// filesystem-CAS claim against uncooperative writers: the re-read-before-
// publish check detects cooperating conflicts, and the create path fails
// rather than clobbering an unexpected file, but an uncooperative writer
// racing inside the publish window is outside the model and is never
// silently tolerated by a false "atomic" claim.
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
	// testHookBeforeCommit, if non-nil, runs after staging and the
	// re-read check but BEFORE the platform commit; a returned error is
	// a pre-commit failure: staging is cleaned, nothing is committed.
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
// The caller must hold the installation exclusion lock (lock) across the
// observe-expect-publish sequence; this function does not take it and does
// not verify the holder. dir must be an already-provisioned private
// directory — it is verified with the privdir guard and NEVER provisioned
// or repaired here (unsafe ACLs/permissions are refused unchanged, not
// rewritten).
//
// Protocol: verify the private-directory guard → re-read the destination
// and refuse on any conflict → stage the canonical strict-JSON encoding in
// a unique O_EXCL private tempfile (write/sync/close) → platform commit
// (Unix rename + directory fsync, or on Windows MoveFileEx with
// MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH — create uses the
// no-clobber variants; see the platform files for the documented flush
// limits) → re-read to confirm the observed value.
func Publish(ctx context.Context, dir string, lock *exclusion.Handle, expected Expected, next activation.Selection) (Result, error) {
	var zero Result
	if ctx == nil {
		return zero, fmt.Errorf("%w: nil context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrCanceled, err)
	}
	if lock == nil {
		return zero, fmt.Errorf("%w: caller-held installation exclusion handle required", ErrInvalidRequest)
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
	dest := filepath.Join(dir, FileName)

	// Private-directory guard: refuse an absent directory (this package
	// provisions nothing — the caller provisions), then verify through
	// privdir, which refuses unsafe roots unchanged and never repairs
	// an unsafe ACL.
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return zero, fmt.Errorf("%w: directory does not exist; publication verifies but never provisions", ErrUnsafeDirectory)
		}
		return zero, fmt.Errorf("%w: %w", ErrUnsafeDirectory, err)
	}
	created, err := privdir.Ensure(dir)
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrUnsafeDirectory, err)
	}
	if created {
		return zero, fmt.Errorf("%w: directory vanished and was recreated by the guard; refusing", ErrUnsafeDirectory)
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
	// write, sync, close.
	tmp, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		return zero, fmt.Errorf("selection: create staging tempfile: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			cleanupOwnedTemp(tmp.Name(), tmpIdentity(tmp))
		}
	}()
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
	staged, err := tmp.Stat()
	if err != nil {
		return zero, fmt.Errorf("selection: staging identity: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return zero, fmt.Errorf("selection: close staging tempfile: %w", err)
	}

	if testHookBeforeCommit != nil {
		if herr := testHookBeforeCommit(); herr != nil {
			return zero, fmt.Errorf("selection: pre-commit failure point: %w", herr)
		}
	}

	// Platform commit. After this succeeds the selection IS published;
	// every later failure becomes durability-uncertain, never a
	// rollback.
	if expected.Absent {
		err = commitCreateSelection(tmp.Name(), dest)
	} else {
		err = commitReplaceSelection(tmp.Name(), dest)
	}
	if err != nil {
		return zero, err // pre-commit failure: rename/link/move itself failed; staging cleaned by the deferred cleanup
	}
	committed = true

	var retained []string
	if expected.Absent {
		// Create paths that publish via link (Unix) leave the staging
		// NAME in place; remove exactly this operation's file, by
		// identity, never by pattern or recursion. Windows moves the
		// name; its remove is a no-op.
		if rerr := removeOwnedTempAfterCommit(tmp.Name(), staged); rerr != nil {
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

// tmpIdentity captures the staging file's identity from its open handle;
// nil is tolerated (the removal then requires only a regular non-link
// entry at the exact staging path).
func tmpIdentity(f *os.File) os.FileInfo {
	if f == nil {
		return nil
	}
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	return info
}

// removeOwnedTempAfterCommit is the post-commit cleanup: identical
// identity discipline, but a failure is only reported (the commit stands).
func removeOwnedTempAfterCommit(path string, identity os.FileInfo) error {
	return cleanupOwnedTemp(path, identity)
}

func sameStagingIdentity(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
