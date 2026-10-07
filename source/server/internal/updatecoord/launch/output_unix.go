//go:build unix

package launch

import (
	"os"
	"syscall"
)

// openOutputFile opens an output log without following a symlink at the
// final component and without blocking on a FIFO or device swapped in
// between the pre-check and the open: O_NOFOLLOW makes a symlink fail
// with ELOOP, O_NONBLOCK makes a FIFO open return instead of hanging
// until a reader appears (the post-open regular-file check then rejects
// it). Regular-file I/O ignores O_NONBLOCK. New files are 0600.
func openOutputFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
}
