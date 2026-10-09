// Package exclusion provides installation-scoped OS file locks, not a service.
// Callers supply one stable, trusted per-installation state directory. They must
// use the same directory across versions and establish its ownership/permissions.
// Locks are never unlinked or taken over based on age. No live paths are chosen.
package exclusion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type Mode int

const (
	Launch Mode = iota + 1 // Shared, held only through coordinated launch admission.
	Update                 // Exclusive, held by the one-shot utility during its protected operation.
)

var ErrUnsafePath = errors.New("unsafe exclusion path")
var errBusy = errors.New("exclusion busy")

// Guarded-use refusal sentinels. Callers match with errors.Is; every
// refusal leaves the handle and its lock file untouched.
var (
	// ErrNilHandle: the receiver is a nil pointer or a zero-value Handle.
	// No lease is proven, so no identity is assumed from it.
	ErrNilHandle = errors.New("exclusion: nil or zero-value exclusion handle")
	// ErrClosedHandle: Close has been requested (or has already run) on
	// this instance, so the lease is no longer live.
	ErrClosedHandle = errors.New("exclusion: exclusion handle is closed or closing")
	// ErrSharedHandle: the handle is a shared Launch lease, not the
	// exclusive Update lease a guarded use requires.
	ErrSharedHandle = errors.New("exclusion: shared launch handle does not hold the exclusive update lock")
	// ErrForeignDirectory: the handle is bound to a different directory
	// than the one the caller named; the installation association does
	// not hold. The binding is PROVEN because Acquire records the
	// directory it locked — it is never inferred from a nil value.
	ErrForeignDirectory = errors.New("exclusion: handle is bound to a different directory")
	// ErrNilCallback: GuardUpdate was called with a nil callback. The
	// guard refuses instead of letting a nil call panic mid-guard.
	ErrNilCallback = errors.New("exclusion: nil guarded-use callback")
	// ErrReplacedLock: the lock file or the directory holding it no
	// longer matches what the handle acquired (renamed or replaced while
	// the OS lock keeps pinning the old inode), so the pathname world
	// cannot prove the lease a guarded use is about to rely on.
	ErrReplacedLock = errors.New("exclusion: lock file or directory no longer matches acquisition")
)

// Handle owns one acquired OS lock. Close is idempotent. Process death releases
// the OS lock, but its persistent pathname must remain. A parent must not hold
// Update while waiting for a child that needs the same lock.
type Handle struct {
	file *os.File
	once sync.Once
	err  error

	// mode and directory are recorded at acquisition time: a Handle can
	// PROVE which lease it holds and for which directory, so a guarded
	// use never has to fake identity from a nil or zero-value handle.
	// dirInfo is the directory's identity as acquired; a guarded use
	// re-proves the pathname world against it before every callback.
	mode      Mode
	directory string
	dirInfo   os.FileInfo

	mu        sync.Mutex
	cond      *sync.Cond
	guardBusy bool // a GuardUpdate callback currently holds this handle's guard slot
	closeWait bool // Close requested: new guards are refused from here
}

// Close releases the lock. It is idempotent, and a second call returns the
// same result as the first. While a GuardUpdate callback is running, Close
// blocks until the callback returns (normally or by panic): the lease is never
// released under a live guard. A Close that is pending also refuses every
// guarded use still waiting for the guard slot, so Close never blocks behind
// a guard that will never run. A nil receiver and a zero-value Handle are
// both a safe no-op returning nil: neither ever held a lease, so repeated
// closes stay idempotent and never touch an unheld descriptor.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.closeWait = true
	if h.cond == nil {
		h.cond = sync.NewCond(&h.mu)
	}
	// Wake queued guard waiters so they observe the pending Close and
	// refuse; Close then waits only for the one live callback.
	h.cond.Broadcast()
	for h.guardBusy {
		h.cond.Wait()
	}
	h.mu.Unlock()
	h.once.Do(func() {
		if h.file == nil {
			return // a zero-value handle never held a lease
		}
		h.err = errors.Join(unlock(h.file), h.file.Close())
	})
	return h.err
}

// GuardUpdate verifies that h is a live, exclusive Update-mode handle
// acquired for exactly directory, then runs fn under that proof. Guarded
// uses on one handle serialize: concurrent callers on the same lease would
// otherwise race each other's expected-state check and the last writer would
// silently win, so a second callback waits for the first to finish rather
// than overlapping it. Close is prevented until fn returns or panics: the
// OS lease cannot be released while the guarded use is mid-flight. This
// takes no new OS lock — it guards the one the caller already holds.
//
// Refusals (all without running fn): a nil or zero-value handle
// (ErrNilHandle), a nil callback (ErrNilCallback), a handle that is closed
// or whose Close is pending (ErrClosedHandle), a shared Launch lease
// (ErrSharedHandle), a handle whose recorded acquisition directory is not
// exactly directory (ErrForeignDirectory), or a handle whose lock file or
// directory no longer matches what was acquired — the pathname was renamed
// or replaced while the OS lock keeps pinning the old inode
// (ErrReplacedLock). The directory is caller-supplied, not defaulted:
// passing it is how the caller asserts the installation association, and
// the check proves the handle was acquired for that very directory.
//
// fn's error is propagated unchanged. If fn panics, the panic is not
// swallowed and the guard is released as the panic unwinds, so Close is
// blocked no longer than the panic itself.
func (h *Handle) GuardUpdate(directory string, fn func() error) error {
	if h == nil || h.file == nil {
		return fmt.Errorf("%w", ErrNilHandle)
	}
	if fn == nil {
		return fmt.Errorf("%w", ErrNilCallback)
	}
	if err := h.beginGuard(directory); err != nil {
		return err
	}
	defer h.endGuard()
	if err := h.verifyLeaseIdentity(); err != nil {
		return err
	}
	return fn()
}

// verifyLeaseIdentity re-proves, right before a guarded callback runs, that
// the pathname world still matches the inode the OS lock pins: the lock
// file named at directory/update.lock must still be the regular file the
// held descriptor opened, and the directory must still be the inode
// recorded at acquisition. This detects a renamed or replaced update.lock
// or directory while the OS lock keeps the old inode, so a guarded use
// never runs against a world that no longer names its lease. It is a
// check, not a defense: a same-user writer able to swap paths can also
// swap them again between this check and the callback, so no hostile
// same-user claim is made here.
func (h *Handle) verifyLeaseIdentity() error {
	opened, err := h.file.Stat()
	if err != nil {
		return fmt.Errorf("%w: held lock file: %w", ErrReplacedLock, err)
	}
	named, err := os.Lstat(filepath.Join(h.directory, "update.lock"))
	if err != nil {
		return fmt.Errorf("%w: named lock file: %w", ErrReplacedLock, err)
	}
	parent, err := os.Lstat(h.directory)
	if err != nil {
		return fmt.Errorf("%w: lock directory: %w", ErrReplacedLock, err)
	}
	if !opened.Mode().IsRegular() || !named.Mode().IsRegular() || named.Mode()&os.ModeSymlink != 0 ||
		!parent.Mode().IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(opened, named) || !os.SameFile(parent, h.dirInfo) {
		return fmt.Errorf("%w", ErrReplacedLock)
	}
	return nil
}

func (h *Handle) beginGuard(directory string) error {
	if h == nil || h.file == nil {
		return fmt.Errorf("%w", ErrNilHandle)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closeWait:
		return fmt.Errorf("%w", ErrClosedHandle)
	case h.mode != Update:
		return fmt.Errorf("%w", ErrSharedHandle)
	case directory == "" || directory != h.directory:
		return fmt.Errorf("%w: bound to %q, requested %q", ErrForeignDirectory, h.directory, directory)
	}
	// Guarded uses serialize on this handle. A queued waiter is refused
	// as soon as Close is pending, so Close never waits behind a guard
	// that will never run and no deadlock can form.
	for h.guardBusy {
		if h.closeWait {
			return fmt.Errorf("%w", ErrClosedHandle)
		}
		if h.cond == nil {
			h.cond = sync.NewCond(&h.mu)
		}
		h.cond.Wait()
	}
	if h.closeWait {
		return fmt.Errorf("%w", ErrClosedHandle)
	}
	h.guardBusy = true
	return nil
}

func (h *Handle) endGuard() {
	h.mu.Lock()
	h.guardBusy = false
	if h.cond != nil {
		h.cond.Broadcast()
	}
	h.mu.Unlock()
}

// Acquire waits with context cancellation. This new namespace is not yet wired
// into legacy agent auto-launch or Homebrew hooks: all participants must adopt
// it together. An unknown or old launcher is not magically excluded.
func Acquire(ctx context.Context, directory string, mode Mode) (*Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mode != Launch && mode != Update {
		return nil, errors.New("invalid exclusion mode")
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrUnsafePath
	}
	dir, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafePath
	}
	if runtime.GOOS != "windows" && dir.Mode().Perm()&0022 != 0 {
		return nil, ErrUnsafePath
	}
	path := filepath.Join(directory, "update.lock")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsafePath
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, ErrUnsafePath
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	// This checks the opened object against the pathname; it does not defend a
	// deliberately hostile same-user owner replacing the trusted parent afterward.
	verify := func() error {
		opened, e := file.Stat()
		if e != nil {
			return e
		}
		named, e := os.Lstat(path)
		if e != nil {
			return e
		}
		parent, e := os.Lstat(directory)
		if e != nil {
			return e
		}
		if !opened.Mode().IsRegular() || !named.Mode().IsRegular() || named.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, named) || !os.SameFile(parent, dir) {
			return ErrUnsafePath
		}
		return nil
	}
	if err = verify(); err != nil {
		file.Close()
		return nil, err
	}
	for {
		if err = ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err = tryLock(file, mode)
		if err == nil {
			if e := ctx.Err(); e != nil {
				_ = unlock(file)
				file.Close()
				return nil, e
			}
			if e := verify(); e != nil {
				_ = unlock(file)
				file.Close()
				return nil, e
			}
			// dir is the directory identity observed at acquisition time (and
			// re-proved by verify); the held file descriptor pins the lock
			// file's inode. A guarded use re-checks both against the live
			// pathname world before every callback.
			return &Handle{file: file, mode: mode, directory: directory, dirInfo: dir}, nil
		}
		if !errors.Is(err, errBusy) {
			file.Close()
			return nil, fmt.Errorf("acquire exclusion: %w", err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
