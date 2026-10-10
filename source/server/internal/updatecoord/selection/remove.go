package selection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/privdir"
	"cercano/source/server/internal/updatecoord/state"
)

// Kept private and not called by production recovery until candidate-completion
// proof is integrated. A matching descriptor or journal checkpoint is NOT proof
// that a candidate process has stopped. This primitive only changes the one
// fixed selection entry under a live guard; never directories or user data.
// Like publication, it assumes a protected parent and cooperating writers.
var testHookBeforeRemove func() error
var testHookAfterRemove func() error

func removeExactGuarded(ctx context.Context, guard exclusion.GuardSession, dir string, expected Expected) (Result, error) {
	if ctx == nil || expected.Absent || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || expected.Selection.Validate() != nil || !state.IsLowerHexSHA256(expected.Digest) {
		return Result{}, ErrInvalidRequest
	}
	release, err := guard.ClaimPublication(dir)
	if err != nil {
		return Result{}, errors.Join(ErrInvalidRequest, err)
	}
	defer release()
	if err = privdir.VerifyExisting(dir); err != nil {
		return Result{}, errors.Join(ErrUnsafeDirectory, err)
	}
	if err = ctx.Err(); err != nil {
		return Result{}, errors.Join(ErrCanceled, err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		return Result{}, errors.Join(ErrUnsafeDirectory, err)
	}
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil || !parentInfo.IsDir() {
		return Result{}, ErrUnsafeDirectory
	}
	path := filepath.Join(dir, FileName)
	named, err := os.Lstat(path)
	if err != nil {
		return Result{}, errors.Join(ErrConflict, err)
	}
	if !named.Mode().IsRegular() {
		return Result{}, ErrDestinationUnsafe
	}
	f, err := openRemovalCandidate(path)
	if err != nil {
		return Result{}, errors.Join(ErrDestinationUnsafe, err)
	}
	defer f.Close()
	pinned, err := f.Stat()
	if err != nil || !pinned.Mode().IsRegular() || !os.SameFile(named, pinned) {
		return Result{}, ErrDestinationUnsafe
	}
	verify := func() error {
		namedParent, e := os.Lstat(dir)
		if e != nil || !namedParent.IsDir() || !os.SameFile(parentInfo, namedParent) {
			return ErrUnsafeDirectory
		}

		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxSelectionBytes+1))
		if err != nil {
			return err
		}
		observed, err := activation.ParseSelection(data)
		if err != nil {
			return errors.Join(ErrConflict, err)
		}
		hash := sha256.Sum256(data)
		if observed != expected.Selection || hex.EncodeToString(hash[:]) != expected.Digest {
			return ErrConflict
		}
		current, err := os.Lstat(path)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(pinned, current) {
			return ErrDestinationUnsafe
		}
		return nil
	}
	if err = verify(); err != nil {
		return Result{}, err
	}
	if testHookBeforeRemove != nil {
		if err = testHookBeforeRemove(); err != nil {
			return Result{}, err
		}
	}
	if err = ctx.Err(); err != nil {
		return Result{}, errors.Join(ErrCanceled, err)
	}
	if err = verify(); err != nil {
		return Result{}, err
	}
	if err = os.Remove(path); err != nil {
		return Result{}, err
	}
	// From here on the effect happened. Never report it as a precommit refusal.
	uncertain := func(err error) (Result, error) {
		return Result{State: CommitDurabilityUncertain, Detail: err.Error()}, nil
	}
	// Windows delete-pending entries become absent only once this identity
	// handle closes. The removal already happened; close failure is uncertain.
	if err = f.Close(); err != nil {
		return uncertain(err)
	}
	if testHookAfterRemove != nil {
		if err = testHookAfterRemove(); err != nil {
			return uncertain(err)
		}
	}
	if err = syncSelectionDirectory(dir); err != nil {
		return uncertain(err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		if err == nil {
			err = ErrConflict
		}
		return uncertain(err)
	}
	return Result{State: CommitConfirmed}, nil
}
