//go:build darwin || linux

package exclusion

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func tryLock(f *os.File, mode Mode) error {
	flags := unix.LOCK_NB | unix.LOCK_SH
	if mode == Update {
		flags = unix.LOCK_NB | unix.LOCK_EX
	}
	err := unix.Flock(int(f.Fd()), flags)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return errBusy
	}
	return err
}
func unlock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
