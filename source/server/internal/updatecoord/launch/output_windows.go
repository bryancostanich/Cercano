//go:build windows

package launch

import "os"

// openOutputFile: Go's Windows open path has no O_NOFOLLOW or O_NONBLOCK
// equivalents. Symlinks (and any other reparse point) are rejected by the
// Lstat type pre-check, and the post-open regular-file check rejects
// anything that slipped through; the residual same-user swap race is
// outside the documented threat model (see openOutputStream). New files
// are 0600.
func openOutputFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}
