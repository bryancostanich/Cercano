//go:build darwin || linux

package enterprise

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// LockConnection prevents separate CLI processes from rotating the same refresh
// credential or changing the active connection concurrently. Keep the file in
// place: unlinking it would let another process lock a different inode.
func LockConnection(path string) (func(), error) {
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return nil, ErrCredentialStore
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrCredentialStore
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another Cercano host owns the enterprise connection")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }, nil
}
