//go:build windows

package exclusion

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

// All participants lock byte zero. Shared mode is the absence of EXCLUSIVE_LOCK;
// Windows has no LOCKFILE_SHARE_MODE flag. Directory ACL enforcement is a separate
// enrollment requirement: Unix 0600 mode bits do not establish a Windows ACL.
func tryLock(f *os.File, mode Mode) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == Update {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var overlap windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &overlap)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errBusy
	}
	return err
}
func unlock(f *os.File) error {
	var overlap windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlap)
}
