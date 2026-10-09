//go:build windows

package selection

import (
	"errors"

	"golang.org/x/sys/windows"
)

// closedHandleStatErr reports whether serr is the exact error this
// platform's (*os.File).Stat returns for a handle that has already been
// closed. On Windows, File.Stat goes straight to the raw handle
// (GetFileType), and a closed handle reports the NATIVE
// ERROR_INVALID_HANDLE — Windows never maps it to fs.ErrClosed, which is
// why a Unix-only ErrClosed expectation fails the native job. The check
// deliberately accepts ONLY that native closed-handle error — never a
// blanket non-nil check — so an unexpected live-handle success or a
// foreign error (for example a handle that was never closed) still fails
// the proof.
func closedHandleStatErr(serr error) bool {
	return serr != nil && errors.Is(serr, windows.ERROR_INVALID_HANDLE)
}
