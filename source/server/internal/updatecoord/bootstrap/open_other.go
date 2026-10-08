//go:build !unix

package bootstrap

import "os"

// openSourceFile is a plain read-only open on this platform family (Go's
// Windows open path has no O_NOFOLLOW or O_NONBLOCK equivalents). Symlinks
// and other reparse points are rejected by the Lstat type pre-check, and
// the post-open regular-file check rejects anything that slipped through;
// the residual same-user swap race is not claimed to be defended (the same
// stance as the launch package's output open). No chmod is ever applied.
func openSourceFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}
