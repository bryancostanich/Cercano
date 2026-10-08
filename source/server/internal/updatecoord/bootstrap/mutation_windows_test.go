//go:build windows

package bootstrap

import (
	"errors"
	"syscall"
)

// ERROR_SHARING_VIOLATION is not exported by Go's syscall package, so it
// is named here by its native value.
const errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION

// mutationPreventedByOS reports whether err is a native Windows error
// proving the OS itself refused the adversarial rename: openSourceFile
// holds the source for the copy WITHOUT FILE_SHARE_DELETE, so Windows
// blocks rebinding or moving the pathname while the handle is open
// (MoveFileEx surfaces ERROR_SHARING_VIOLATION or ERROR_ACCESS_DENIED).
// These two errnos — and only these — count as "mutation prevented";
// any other error is unexpected and must fail the test. A nil error is a
// real mutation and is handled by the caller, never by this predicate.
func mutationPreventedByOS(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errSharingViolation || errno == syscall.ERROR_ACCESS_DENIED
}
