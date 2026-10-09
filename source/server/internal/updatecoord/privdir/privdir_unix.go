//go:build unix

package privdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ensure is the Unix half of Ensure. It reuses the same per-user uid/mode
// guard pattern as internal/updatecoord/state: real process credentials,
// 0o700 creation, conservative refusal of anything it cannot prove, and
// never chmodding an existing permissive directory.
func ensure(dir string) (created bool, err error) {
	if err := os.Mkdir(dir, 0o700); err == nil {
		// Readback: prove the created directory actually has the policy.
		if verr := verifyExistingUnix(dir); verr != nil {
			return false, fmt.Errorf("privdir: newly created directory failed verification: %w", verr)
		}
		return true, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("privdir: create directory: %w", err)
	}
	if verr := verifyExistingUnix(dir); verr != nil {
		return false, verr
	}
	return false, nil
}

// verifyExistingUnix verifies an existing directory against the approved
// per-user policy: owned by the current REAL uid (os.Getuid — never an
// environment-supplied name), mode 0o7XX (no group or other access) with
// user write access, and not a symlink. A failing directory is reported
// and left unchanged; this function never chmods or chowns.
func verifyExistingUnix(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("privdir: inspect %q: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q is a symlink", ErrUnsafePath, dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrNotDirectory, dir)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Cannot prove ownership on this platform build: fail closed.
		return fmt.Errorf("%w: owner information unavailable", ErrUnverifiedIdentity)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%w: owned by uid %d, current uid %d", ErrForeignOwner, stat.Uid, os.Getuid())
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		// Permissive mode refused unchanged; this code NEVER chmods.
		return fmt.Errorf("%w: mode %04o grants group or other access", ErrUnsafeACL, perm)
	}
	if perm&0o200 == 0 {
		// Fail closed: a directory the current user cannot write is not a
		// usable private updater state directory.
		return fmt.Errorf("%w: mode %04o grants no user write access", ErrUnsafeACL, perm)
	}
	return nil
}
