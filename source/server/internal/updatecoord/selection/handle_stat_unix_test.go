//go:build unix

package selection

import (
	"errors"
	"io/fs"
)

// closedHandleStatErr reports whether serr is the exact error this
// platform's (*os.File).Stat returns for a handle that has already been
// closed. On Unix, a closed file's Stat reports fs.ErrClosed. The check
// deliberately accepts ONLY that error — never a blanket non-nil check —
// so an unexpected live-handle success or a foreign error (for example a
// handle that was never closed) still fails the proof.
func closedHandleStatErr(serr error) bool {
	return serr != nil && errors.Is(serr, fs.ErrClosed)
}
