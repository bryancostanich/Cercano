//go:build unix

package bootstrap

import (
	"os"
	"syscall"
)

// openSourceFile opens an existing file for reading WITHOUT following a
// symlink at the final component and without blocking on a FIFO or device
// swapped in between the pre-check and the open: O_NOFOLLOW makes a symlink
// fail with ELOOP, O_NONBLOCK makes a FIFO open return instead of hanging
// until a writer appears (the post-open regular-file check then rejects
// it). Regular-file I/O ignores O_NONBLOCK. The permission bits are
// irrelevant for an existing file and never modify it: this open is
// read-only and no chmod is ever applied to a source.
func openSourceFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
