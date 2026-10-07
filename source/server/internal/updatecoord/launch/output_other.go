//go:build !unix && !windows

package launch

import "os"

// openOutputFile is a plain open: this platform family has no
// nofollow/nonblocking flags and is refused earlier by
// detachedSysProcAttr anyway. New files are 0600.
func openOutputFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}
