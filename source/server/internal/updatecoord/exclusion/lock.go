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

// Handle owns one acquired OS lock. Close is idempotent. Process death releases
// the OS lock, but its persistent pathname must remain. A parent must not hold
// Update while waiting for a child that needs the same lock.
type Handle struct {
	file *os.File
	once sync.Once
	err  error
}

func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	h.once.Do(func() { h.err = errors.Join(unlock(h.file), h.file.Close()) })
	return h.err
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
			return &Handle{file: file}, nil
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
