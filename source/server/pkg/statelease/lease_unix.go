//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package statelease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquire(root string, exclusive bool) (*Lease, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("state lease requires an absolute state directory")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create state lease directory: %w", err)
	}
	dir, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open state lease directory: %w", err)
	}
	defer unix.Close(dir)
	var ds unix.Stat_t
	if err = unix.Fstat(dir, &ds); err != nil {
		return nil, fmt.Errorf("inspect state lease directory: %w", err)
	}
	if ds.Uid != uint32(os.Getuid()) || ds.Mode&0022 != 0 {
		return nil, fmt.Errorf("state lease directory must be owned by the current user and not writable by others")
	}
	fd, err := unix.Openat(dir, ".setup-state.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open state lease: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(root, ".setup-state.lock"))
	fail := func(err error) (*Lease, error) { file.Close(); return nil, err }
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return fail(fmt.Errorf("inspect state lease: %w", err))
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 || st.Nlink != 1 {
		return fail(fmt.Errorf("state lease must be a private, singly linked regular file owned by the current user"))
	}
	mode := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		mode = unix.LOCK_EX | unix.LOCK_NB
	}
	if err = unix.Flock(fd, mode); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return fail(ErrBusy)
		}
		return fail(fmt.Errorf("acquire state lease: %w", err))
	}
	return &Lease{file: file}, nil
}
