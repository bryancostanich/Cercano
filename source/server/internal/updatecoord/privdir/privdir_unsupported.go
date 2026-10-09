//go:build !windows && !unix

package privdir

import (
	"errors"
	"fmt"
)

// ensure is the fail-closed stub for platforms with no approved
// private-directory policy. No platform policy is invented or expanded
// here; callers are simply refused.
func ensure(dir string) (created bool, err error) {
	return false, errors.New(dir + ": " + ErrUnsupportedPlatform.Error())
}

// verifyExistingDir is the fail-closed verify-only stub for platforms
// with no approved private-directory policy.
func verifyExistingDir(dir string) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedPlatform, dir)
}
