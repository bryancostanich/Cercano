package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrReceiptStale reports that a prepared receipt no longer describes its
// staged objects: the staging directory or staged image identity changed,
// or the length/digest of the staged bytes no longer matches the receipt.
// A stale receipt must never be executed.
var ErrReceiptStale = errors.New("bootstrap: prepared receipt no longer matches its staged files")

// Revalidate proves one prepared receipt still describes its staged image
// BEFORE that image is executed by any later boundary.
//
// It checks, in order:
//
//  1. The staging directory still exists as a directory and has the SAME
//     identity (os.SameFile against the identity captured by Prepare) —
//     not merely the same path, so a replaced or re-bound directory is
//     detected.
//  2. The staged image path still names a regular file with the same
//     identity as the file Prepare created, and the same length.
//  3. The staged bytes still hash to the receipt's SHA-256 digest, via the
//     same bounded re-read used during preparation (identity is re-checked
//     around the read so an in-place mutation during the read is caught).
//
// Revalidation is integrity, not publisher authentication: the caller
// remains responsible for source trust and state-directory protection.
// It performs no execution and no cleanup; a stale receipt is the caller's
// to discard through its normal staging cleanup.
func (p *PreparedImage) Revalidate(ctx context.Context) error {
	if p == nil {
		return ErrReceiptStale
	}
	if ctx == nil {
		return ErrInvalidRequest
	}
	if p.dirInfo == nil || p.fileInfo == nil {
		// A receipt without remembered identities was not produced by
		// Prepare; refuse to treat paths alone as proof.
		return ErrReceiptStale
	}
	dir, err := os.Lstat(p.stagingDir)
	if err != nil {
		return errors.Join(ErrReceiptStale, err)
	}
	if !dir.IsDir() || !os.SameFile(dir, p.dirInfo) {
		return fmt.Errorf("%w: staging directory identity changed", ErrReceiptStale)
	}
	named, err := os.Lstat(p.path)
	if err != nil {
		return errors.Join(ErrReceiptStale, err)
	}
	if !named.Mode().IsRegular() || !os.SameFile(named, p.fileInfo) {
		return fmt.Errorf("%w: staged image identity changed", ErrReceiptStale)
	}
	if named.Size() != p.length {
		return fmt.Errorf("%w: staged image length changed", ErrReceiptStale)
	}
	if err := verifyCopy(ctx, p.path, p.sha256, p.length); err != nil {
		if errors.Is(err, ErrCanceled) {
			return err
		}
		return errors.Join(ErrReceiptStale, err)
	}
	return nil
}
