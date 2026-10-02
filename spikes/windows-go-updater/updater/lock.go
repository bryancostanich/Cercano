package updater

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/flock"
)

// handle is the platform lock handle (flock on POSIX, LockFileEx on Windows,
// both provided by gofrs/flock — a single lock-owner across processes on
// either OS).
type handle = *flock.Flock

// lockfileCreate takes an OS-level lock at path, creating the file if needed.
// blocking=true retries once per second until acquired; otherwise a single
// non-blocking attempt is made. The stale parameter is accepted for API
// symmetry: gofrs/flock locks do not go stale while a live process holds
// them, and a crashed holder's lock is released by the OS at process exit.
// (The lock FILE lingers after a crash; RunWithLock removes it on exit, and
// re-locking a leftover file is safe because the OS lock is gone.)
func lockfileCreate(path string, blocking bool, stale time.Duration) (handle, error) {
	l := flock.New(path)
	var (
		ok  bool
		err error
	)
	if blocking {
		ctx, cancel := context.WithTimeout(context.Background(), stale)
		defer cancel()
		ok, err = l.TryLockContext(ctx, time.Second)
	} else {
		ok, err = l.TryLock()
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("update lock is held by another process")
	}
	return l, nil
}

// lockfileClose releases the lock (the caller separately removes the file).
func lockfileClose(h handle) error {
	if h == nil {
		return nil
	}
	return h.Unlock()
}
