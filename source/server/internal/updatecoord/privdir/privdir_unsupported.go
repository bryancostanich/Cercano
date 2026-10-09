//go:build !windows && !unix

package privdir

import "errors"

// ensure is the fail-closed stub for platforms with no approved
// private-directory policy. No platform policy is invented or expanded
// here; callers are simply refused.
func ensure(dir string) (created bool, err error) {
	return false, errors.New(dir + ": " + ErrUnsupportedPlatform.Error())
}
