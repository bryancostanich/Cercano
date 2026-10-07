//go:build windows

package agentclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// The lock file uses 0600 mode bits for symmetry with the Unix implementation,
// but mode bits carry no Windows ACL meaning; directory ACL enforcement is a
// separate enrollment requirement, not something this file claims to provide.

func acquireAutoLaunchLock() (*os.File, error) {
	return acquireLaunchLock(context.Background(), os.TempDir())
}

// AcquireAutoLaunchLock coordinates an upgrade with CLI auto-launch. tempDir
// must be an absolute directory supplied by the caller (e.g. from the running
// agent's environment). The returned release function must always be called.
func AcquireAutoLaunchLock(ctx context.Context, tempDir string) (func(), error) {
	f, err := acquireLaunchLock(ctx, tempDir)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { releaseAutoLaunchLock(f) }) }, nil
}

func acquireLaunchLock(ctx context.Context, tempDir string) (*os.File, error) {
	// Check the context before touching the filesystem so a canceled caller
	// never creates the lock file at all.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(tempDir) {
		return nil, fmt.Errorf("launch lock directory must be absolute")
	}
	path := filepath.Join(tempDir, "cercano-agent-launch.lock")
	if err := preflightLaunchLockPath(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open launch lock: %w", err)
	}
	// Recheck the pathname as well as the opened target. A normal open can
	// follow a link, so target attributes alone do not prove there was no link.
	verify := func() error {
		opened, e := f.Stat()
		if e != nil {
			return e
		}
		named, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !opened.Mode().IsRegular() || !named.Mode().IsRegular() || named.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, named) {
			return errors.New("launch lock pathname changed")
		}
		attr, e := launchLockFileAttributes(f)
		if e != nil {
			return e
		}
		if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("launch lock is a reparse point")
		}
		return nil
	}
	if err = verify(); err != nil {
		f.Close()
		return nil, err
	}

	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		locked, err := tryLockLaunchFile(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("acquire launch lock: %w", err)
		}
		if locked {
			if err = ctx.Err(); err != nil {
				releaseAutoLaunchLock(f)
				return nil, err
			}
			if err = verify(); err != nil {
				releaseAutoLaunchLock(f)
				return nil, err
			}
			return f, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// preflightLaunchLockPath refuses to lock a path that is not a plain regular
// file (symlink, directory, device, etc.). It does not create anything.
func preflightLaunchLockPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect launch lock path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("launch lock path %s is not a regular file", path)
	}
	return nil
}

func launchLockFileAttributes(f *os.File) (uint32, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, err
	}
	return info.FileAttributes, nil
}

// tryLockLaunchFile attempts a nonblocking exclusive lock of byte 0 only.
func tryLockLaunchFile(f *os.File) (bool, error) {
	var overlap windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

// releaseAutoLaunchLock is nil-safe and idempotent. The lock is never
// "unlinked": the file stays on disk and only the byte-range lock is dropped.
// It refuses to touch the handle of a file that is already closed, so a stale
// handle value (which the runtime may have reused for another file) is never
// unlocked a second time.
func releaseAutoLaunchLock(f *os.File) {
	if f == nil {
		return
	}
	if _, err := f.Stat(); err != nil {
		// Already closed (or otherwise unusable): nothing to unlock.
		_ = f.Close()
		return
	}
	var overlap windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlap)
	_ = f.Close()
}
